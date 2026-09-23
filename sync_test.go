package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

type testMessage struct {
	id    string
	body  []byte
	flags []imap.Flag
	date  time.Time
}

func makeMessage(n int, flags ...imap.Flag) testMessage {
	id := fmt.Sprintf("msg-%d@origen.test", n)
	body := []byte(fmt.Sprintf(
		"From: alguien@origen.test\r\nTo: usuario@midominio.com\r\nSubject: Prueba %d\r\nMessage-ID: <%s>\r\n\r\nCuerpo del mensaje %d.\r\n",
		n, id, n))
	return testMessage{
		id:    id,
		body:  body,
		flags: flags,
		date:  time.Date(2025, 3, 10+n, 12, 0, 0, 0, time.UTC),
	}
}

func seed(t *testing.T, ep Endpoint, mailbox string, msgs ...testMessage) {
	t.Helper()
	client, err := connect(context.Background(), ep)
	if err != nil {
		t.Fatalf("seed connect: %v", err)
	}
	defer logout(client)
	for _, m := range msgs {
		if err := appendMessage(client, mailbox, m.body, m.flags, m.date); err != nil {
			t.Fatalf("seed append %s: %v", m.id, err)
		}
	}
}

func readAll(t *testing.T, ep Endpoint, mailbox string) []*imapclient.FetchMessageBuffer {
	t.Helper()
	client, err := connect(context.Background(), ep)
	if err != nil {
		t.Fatalf("read connect: %v", err)
	}
	defer logout(client)

	sel, err := client.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		t.Fatalf("read select %s: %v", mailbox, err)
	}
	if sel.NumMessages == 0 {
		return nil
	}
	var set imap.SeqSet
	set.AddRange(1, 0)
	msgs, err := client.Fetch(set, &imap.FetchOptions{
		UID: true, Flags: true, InternalDate: true, Envelope: true,
		BodySection: []*imap.FetchItemBodySection{wholeMessage},
	}).Collect()
	if err != nil {
		t.Fatalf("read fetch: %v", err)
	}
	return msgs
}

// newSyncer wires a syncer against a fresh store in the test's temp dir.
func newSyncer(t *testing.T, src, dst Endpoint, folders ...FolderMap) (*Syncer, *Store) {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	if len(folders) == 0 {
		folders = []FolderMap{{From: "INBOX", To: "INBOX"}}
	}
	return &Syncer{
		account: &Account{Name: "prueba", Source: src, Dest: dst, Folders: folders},
		store:   store,
		log:     log.New(io.Discard, "", 0),
	}, store
}

func setupPair(t *testing.T) (src, dst Endpoint) {
	t.Helper()
	cert, pool := testCert(t)
	prev := rootCAs
	rootCAs = pool
	t.Cleanup(func() { rootCAs = prev })

	source := startServer(t, cert, "usuario@midominio.com", "clave-origen", "INBOX", "Sent")
	dest := startServer(t, cert, "cuenta@gmail.com", "clave-destino", "INBOX")
	return source.endpoint, dest.endpoint
}

func TestSyncCopiesMessagesPreservingBytesFlagsAndDate(t *testing.T) {
	src, dst := setupPair(t)
	want := []testMessage{
		makeMessage(1),
		makeMessage(2, imap.FlagSeen),
		makeMessage(3, imap.FlagFlagged, imap.FlagAnswered),
	}
	seed(t, src, "INBOX", want...)

	syncer, _ := newSyncer(t, src, dst)
	if err := syncer.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := readAll(t, dst, "INBOX")
	if len(got) != len(want) {
		t.Fatalf("el destino tiene %d mensajes, se esperaban %d", len(got), len(want))
	}
	for i, m := range got {
		if string(m.FindBodySection(wholeMessage)) != string(want[i].body) {
			t.Errorf("mensaje %d: el cuerpo no coincide byte a byte", i+1)
		}
		if !m.InternalDate.Equal(want[i].date) {
			t.Errorf("mensaje %d: fecha %v, se esperaba %v", i+1, m.InternalDate, want[i].date)
		}
		if len(want[i].flags) != len(keepFlags(m.Flags)) {
			t.Errorf("mensaje %d: flags %v, se esperaban %v", i+1, m.Flags, want[i].flags)
		}
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	src, dst := setupPair(t)
	seed(t, src, "INBOX", makeMessage(1), makeMessage(2))

	syncer, _ := newSyncer(t, src, dst)
	for pass := 1; pass <= 3; pass++ {
		if err := syncer.Run(context.Background()); err != nil {
			t.Fatalf("pasada %d: %v", pass, err)
		}
		if got := len(readAll(t, dst, "INBOX")); got != 2 {
			t.Fatalf("tras la pasada %d el destino tiene %d mensajes, se esperaban 2", pass, got)
		}
	}
}

func TestSyncPicksUpNewMessages(t *testing.T) {
	src, dst := setupPair(t)
	seed(t, src, "INBOX", makeMessage(1))

	syncer, _ := newSyncer(t, src, dst)
	if err := syncer.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	seed(t, src, "INBOX", makeMessage(2), makeMessage(3))
	if err := syncer.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(readAll(t, dst, "INBOX")); got != 3 {
		t.Fatalf("el destino tiene %d mensajes, se esperaban 3", got)
	}
}

// When the source renumbers its mailbox the UID high-water mark is useless and
// mailsync walks the folder again. The Message-ID table is what stops that
// second walk from duplicating every message.
func TestMessageIDPreventsDuplicatesAfterUIDReset(t *testing.T) {
	src, dst := setupPair(t)
	seed(t, src, "INBOX", makeMessage(1), makeMessage(2))

	syncer, store := newSyncer(t, src, dst)
	if err := syncer.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Simulate the reset: forget where we were, keep what we have seen.
	if _, err := store.db.Exec(`UPDATE folder_state SET last_uid = 0`); err != nil {
		t.Fatal(err)
	}
	if err := syncer.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := len(readAll(t, dst, "INBOX")); got != 2 {
		t.Fatalf("el destino tiene %d mensajes tras el reinicio de UIDs, se esperaban 2", got)
	}
}

func TestSyncCreatesAndRenamesDestinationFolder(t *testing.T) {
	src, dst := setupPair(t)
	seed(t, src, "Sent", makeMessage(1))

	syncer, _ := newSyncer(t, src, dst, FolderMap{From: "Sent", To: "midominio/Enviados"})
	if err := syncer.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(readAll(t, dst, "midominio/Enviados")); got != 1 {
		t.Fatalf("la carpeta de destino tiene %d mensajes, se esperaba 1", got)
	}
}

func TestSourceIsNotModified(t *testing.T) {
	src, dst := setupPair(t)
	seed(t, src, "INBOX", makeMessage(1))

	syncer, _ := newSyncer(t, src, dst)
	if err := syncer.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := readAll(t, src, "INBOX")
	if len(got) != 1 {
		t.Fatalf("el origen tiene %d mensajes, se esperaba 1", len(got))
	}
	for _, f := range got[0].Flags {
		if f == imap.FlagSeen {
			t.Error("copiar marcó el mensaje como leído en el origen")
		}
	}
}

func TestLoginFailureIsExplained(t *testing.T) {
	src, _ := setupPair(t)
	src.Password = "incorrecta"
	if _, err := connect(context.Background(), src); err == nil {
		t.Fatal("se esperaba un fallo de autenticación")
	} else if !strings.Contains(err.Error(), "autenticación rechazada") {
		t.Fatalf("mensaje poco claro: %v", err)
	}
}

// Shared hosting often serves a self-signed certificate under the node's own
// name. Pinning its digest lets mailsync connect while still refusing an
// impostor -- unlike disabling verification, which would accept anything.
func TestPinnedFingerprintAcceptsUntrustedCertificate(t *testing.T) {
	cert, _ := testCert(t)
	prev := rootCAs
	rootCAs = x509.NewCertPool() // trust nothing: the pin is the only check
	t.Cleanup(func() { rootCAs = prev })

	server := startServer(t, cert, "info@ejemplo.test", "clave", "INBOX")

	if _, err := connect(context.Background(), server.endpoint); err == nil {
		t.Fatal("sin pin, un certificado no confiable debería rechazarse")
	}

	sum := sha256.Sum256(cert.Certificate[0])
	pinned := server.endpoint
	pinned.Fingerprint = hex.EncodeToString(sum[:])

	client, err := connect(context.Background(), pinned)
	if err != nil {
		t.Fatalf("con el fingerprint correcto debería conectar: %v", err)
	}
	logout(client)
}

func TestWrongFingerprintIsRejected(t *testing.T) {
	cert, pool := testCert(t)
	prev := rootCAs
	rootCAs = pool
	t.Cleanup(func() { rootCAs = prev })

	server := startServer(t, cert, "info@ejemplo.test", "clave", "INBOX")
	pinned := server.endpoint
	pinned.Fingerprint = strings.Repeat("ab", 32)

	_, err := connect(context.Background(), pinned)
	if err == nil {
		t.Fatal("un fingerprint que no coincide debe rechazarse aunque el certificado sea válido")
	}
	if !strings.Contains(err.Error(), "no coincide con el fingerprint") {
		t.Errorf("el error no explica el problema: %v", err)
	}
}

func TestPeerFingerprintMatchesCertificate(t *testing.T) {
	cert, _ := testCert(t)
	server := startServer(t, cert, "u", "p", "INBOX")

	got, peer, err := peerFingerprint(context.Background(), server.endpoint.Addr())
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(cert.Certificate[0])
	if got != hex.EncodeToString(want[:]) {
		t.Errorf("fingerprint = %s, se esperaba %s", got, hex.EncodeToString(want[:]))
	}
	if peer.Subject.CommonName != "mailsync-test" {
		t.Errorf("sujeto inesperado: %s", peer.Subject)
	}
}
