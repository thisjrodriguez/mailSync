package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// maxMessageBytes caps what we are willing to hold in memory for a single
// message. Anything larger is skipped and logged rather than risking the
// process.
const maxMessageBytes = 60 << 20

// wholeMessage is the section we ask for: the entire RFC822 message, with Peek
// so that reading it does not mark the source as read.
var wholeMessage = &imap.FetchItemBodySection{Peek: true}

// keptFlags are the flags worth carrying over. Anything else (\Recent, custom
// keywords, server-specific labels) is dropped: destinations reject or mangle
// them, and none of it is worth failing an APPEND over.
var keptFlags = map[imap.Flag]bool{
	imap.FlagSeen:     true,
	imap.FlagAnswered: true,
	imap.FlagFlagged:  true,
	imap.FlagDraft:    true,
}

type Syncer struct {
	account *Account
	store   *Store
	log     *log.Logger
}

// Run copies every configured folder once: connect, walk the folders, then
// disconnect. Reconnecting each pass costs a second and buys immunity to the
// idle-timeout disconnects that cheap IMAP hosts are fond of.
func (s *Syncer) Run(ctx context.Context) error {
	src, err := connect(ctx, s.account.Source)
	if err != nil {
		return fmt.Errorf("origen: %w", err)
	}
	defer logout(src)

	dst, err := connect(ctx, s.account.Dest)
	if err != nil {
		return fmt.Errorf("destino: %w", err)
	}
	defer logout(dst)

	var failed int
	for _, folder := range s.account.Folders {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := s.syncFolder(ctx, src, dst, folder)
		if recErr := s.store.RecordRun(s.account.Name, folder.From, err); recErr != nil {
			s.log.Printf("[%s] no se pudo guardar el estado de %s: %v", s.account.Name, folder.From, recErr)
		}
		if err != nil {
			failed++
			s.log.Printf("[%s] %s: %v", s.account.Name, folder.From, err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d de %d carpetas fallaron", failed, len(s.account.Folders))
	}
	return nil
}

func (s *Syncer) syncFolder(ctx context.Context, src, dst *imapclient.Client, folder FolderMap) error {
	sel, err := src.Select(folder.From, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return fmt.Errorf("no se pudo abrir la carpeta: %w", err)
	}

	state, err := s.store.FolderState(s.account.Name, folder.From)
	if err != nil {
		return err
	}

	// A changed UIDVALIDITY means the server renumbered the mailbox: every UID
	// we stored is meaningless. We restart from zero and lean on the
	// Message-ID table to avoid copying everything a second time.
	reset := state.UIDValidity != 0 && state.UIDValidity != sel.UIDValidity
	if reset {
		s.log.Printf("[%s] %s: UIDVALIDITY cambió de %d a %d, reiniciando el recorrido (los duplicados se filtran por Message-ID)",
			s.account.Name, folder.From, state.UIDValidity, sel.UIDValidity)
		state.LastUID = 0
	}
	if err := s.store.SetUIDValidity(s.account.Name, folder.From, sel.UIDValidity, reset); err != nil {
		return err
	}

	if err := ensureMailbox(dst, folder.To); err != nil {
		return err
	}

	pending, err := s.pendingUIDs(src, state.LastUID)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	s.log.Printf("[%s] %s: %d mensajes nuevos", s.account.Name, folder.From, len(pending))

	// Metadata for the whole batch in one round trip; bodies one at a time.
	headers, err := s.fetchMetadata(src, pending)
	if err != nil {
		return err
	}

	var copied int
	for _, uid := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		meta, ok := headers[uid]
		if !ok {
			// Deleted between the search and the fetch. Move past it.
			if err := s.store.Advance(s.account.Name, folder.From, uint32(uid), "", false); err != nil {
				return err
			}
			continue
		}
		done, err := s.copyMessage(src, dst, folder, meta)
		if err != nil {
			return fmt.Errorf("UID %d: %w", uid, err)
		}
		if done {
			copied++
		}
	}
	if copied > 0 {
		s.log.Printf("[%s] %s: %d copiados a %s", s.account.Name, folder.From, copied, folder.To)
	}
	return nil
}

// pendingUIDs asks the server for everything above our high-water mark.
func (s *Syncer) pendingUIDs(src *imapclient.Client, lastUID uint32) ([]imap.UID, error) {
	var set imap.UIDSet
	set.AddRange(imap.UID(lastUID+1), 0) // 0 means "*"

	data, err := src.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{set}}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("la búsqueda falló: %w", err)
	}

	// "n:*" always matches the highest UID in the mailbox, even when that UID
	// is below n. Without this filter an idle mailbox would re-copy its last
	// message on every pass.
	var out []imap.UID
	for _, uid := range data.AllUIDs() {
		if uint32(uid) > lastUID {
			out = append(out, uid)
		}
	}
	return out, nil
}

func (s *Syncer) fetchMetadata(src *imapclient.Client, uids []imap.UID) (map[imap.UID]*imapclient.FetchMessageBuffer, error) {
	msgs, err := src.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		UID:          true,
		Flags:        true,
		InternalDate: true,
		RFC822Size:   true,
		Envelope:     true,
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("no se pudieron leer las cabeceras: %w", err)
	}
	out := make(map[imap.UID]*imapclient.FetchMessageBuffer, len(msgs))
	for _, m := range msgs {
		out[m.UID] = m
	}
	return out, nil
}

// copyMessage moves one message across and records the outcome. It reports
// whether the message was actually appended.
func (s *Syncer) copyMessage(src, dst *imapclient.Client, folder FolderMap, meta *imapclient.FetchMessageBuffer) (bool, error) {
	uid := uint32(meta.UID)
	messageID := ""
	if meta.Envelope != nil {
		messageID = meta.Envelope.MessageID
	}

	seen, err := s.store.HasSeen(s.account.Name, messageID)
	if err != nil {
		return false, err
	}
	if seen {
		return false, s.store.Advance(s.account.Name, folder.From, uid, messageID, false)
	}
	if meta.RFC822Size > maxMessageBytes {
		s.log.Printf("[%s] %s: UID %d omitido, %d bytes superan el límite",
			s.account.Name, folder.From, uid, meta.RFC822Size)
		return false, s.store.Advance(s.account.Name, folder.From, uid, messageID, false)
	}

	body, err := s.fetchBody(src, meta.UID)
	if err != nil {
		return false, err
	}
	if body == nil {
		// Vanished mid-copy; nothing to do but move on.
		return false, s.store.Advance(s.account.Name, folder.From, uid, messageID, false)
	}

	date := meta.InternalDate
	if date.IsZero() {
		date = time.Now()
	}
	if err := appendMessage(dst, folder.To, body, keepFlags(meta.Flags), date); err != nil {
		return false, err
	}

	// Recorded only after the APPEND succeeded: a crash before this point
	// re-copies one message, a crash after loses none.
	return true, s.store.Advance(s.account.Name, folder.From, uid, messageID, true)
}

// fetchBody pulls the raw RFC822 message. Nothing is parsed or rewritten: the
// bytes that leave the source are the bytes that reach the destination.
func (s *Syncer) fetchBody(src *imapclient.Client, uid imap.UID) ([]byte, error) {
	msgs, err := src.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{wholeMessage},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("no se pudo descargar el mensaje: %w", err)
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	body := msgs[0].FindBodySection(wholeMessage)
	if len(body) == 0 {
		return nil, fmt.Errorf("el servidor devolvió un cuerpo vacío")
	}
	return body, nil
}

func appendMessage(dst *imapclient.Client, mailbox string, body []byte, flags []imap.Flag, date time.Time) error {
	cmd := dst.Append(mailbox, int64(len(body)), &imap.AppendOptions{Flags: flags, Time: date})
	if _, err := cmd.Write(body); err != nil {
		cmd.Close()
		return fmt.Errorf("no se pudo enviar el mensaje: %w", err)
	}
	if err := cmd.Close(); err != nil {
		return fmt.Errorf("no se pudo cerrar el envío: %w", err)
	}
	if _, err := cmd.Wait(); err != nil {
		return fmt.Errorf("el destino rechazó el mensaje: %w", err)
	}
	return nil
}

func keepFlags(flags []imap.Flag) []imap.Flag {
	var out []imap.Flag
	for _, f := range flags {
		if keptFlags[f] {
			out = append(out, f)
		}
	}
	return out
}

// ensureMailbox creates the destination folder if it is missing. CREATE on an
// existing mailbox is an error we deliberately ignore.
func ensureMailbox(dst *imapclient.Client, mailbox string) error {
	if _, err := dst.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err == nil {
		return nil
	}
	if err := dst.Create(mailbox, nil).Wait(); err != nil {
		if _, selErr := dst.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); selErr != nil {
			return fmt.Errorf("no se pudo crear la carpeta de destino %q: %w", mailbox, err)
		}
	}
	return nil
}
