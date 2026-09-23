package main

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store holds the sync state: how far each folder has been copied, and which
// Message-IDs have already been seen. This is what makes a re-run pick up where
// it left off instead of duplicating mail.
type Store struct {
	db *sql.DB
}

type FolderState struct {
	Account     string
	Folder      string
	UIDValidity uint32
	LastUID     uint32
	Copied      int64
	Skipped     int64
	LastRun     time.Time
	LastError   string
}

const schema = `
CREATE TABLE IF NOT EXISTS folder_state (
	account      TEXT    NOT NULL,
	folder       TEXT    NOT NULL,
	uid_validity INTEGER NOT NULL DEFAULT 0,
	last_uid     INTEGER NOT NULL DEFAULT 0,
	copied       INTEGER NOT NULL DEFAULT 0,
	skipped      INTEGER NOT NULL DEFAULT 0,
	last_run     INTEGER NOT NULL DEFAULT 0,
	last_error   TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (account, folder)
);
CREATE TABLE IF NOT EXISTS seen_message (
	account    TEXT NOT NULL,
	message_id TEXT NOT NULL,
	copied_at  INTEGER NOT NULL,
	PRIMARY KEY (account, message_id)
);
`

func OpenStore(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer keeps the accounts' goroutines from tripping over each other.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("no se pudo inicializar %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// FolderState returns the stored position for a folder, or a zero value when
// the folder has never been synced.
func (s *Store) FolderState(account, folder string) (FolderState, error) {
	st := FolderState{Account: account, Folder: folder}
	var lastRun int64
	err := s.db.QueryRow(
		`SELECT uid_validity, last_uid, copied, skipped, last_run, last_error
		 FROM folder_state WHERE account = ? AND folder = ?`, account, folder,
	).Scan(&st.UIDValidity, &st.LastUID, &st.Copied, &st.Skipped, &lastRun, &st.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if lastRun > 0 {
		st.LastRun = time.Unix(lastRun, 0)
	}
	return st, nil
}

// SetUIDValidity records the folder's current UIDVALIDITY and resets the
// position when the server has invalidated the previous numbering.
func (s *Store) SetUIDValidity(account, folder string, uidValidity uint32, resetUID bool) error {
	_, err := s.db.Exec(
		`INSERT INTO folder_state (account, folder, uid_validity)
		 VALUES (?, ?, ?)
		 ON CONFLICT (account, folder) DO UPDATE SET
		   uid_validity = excluded.uid_validity,
		   last_uid = CASE WHEN ? THEN 0 ELSE folder_state.last_uid END`,
		account, folder, uidValidity, resetUID)
	return err
}

// Advance moves the folder's high-water mark and bumps the counters. The
// Message-ID is recorded in the same transaction so a crash can never leave a
// message counted as copied but not deduplicated, or the reverse.
func (s *Store) Advance(account, folder string, uid uint32, messageID string, copied bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	column := "skipped"
	if copied {
		column = "copied"
	}
	if _, err := tx.Exec(fmt.Sprintf(
		`UPDATE folder_state SET last_uid = ?, %s = %s + 1 WHERE account = ? AND folder = ?`,
		column, column), uid, account, folder); err != nil {
		return err
	}
	if copied && messageID != "" {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO seen_message (account, message_id, copied_at) VALUES (?, ?, ?)`,
			account, messageID, time.Now().Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// HasSeen reports whether this account already copied a message with that
// Message-ID. It is the safety net for servers that reset UIDVALIDITY.
func (s *Store) HasSeen(account, messageID string) (bool, error) {
	if messageID == "" {
		return false, nil
	}
	var one int
	err := s.db.QueryRow(
		`SELECT 1 FROM seen_message WHERE account = ? AND message_id = ?`,
		account, messageID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// RecordRun stamps the end of a folder pass, storing the error if it failed.
func (s *Store) RecordRun(account, folder string, runErr error) error {
	msg := ""
	if runErr != nil {
		msg = runErr.Error()
	}
	_, err := s.db.Exec(
		`INSERT INTO folder_state (account, folder, last_run, last_error)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (account, folder) DO UPDATE SET
		   last_run = excluded.last_run, last_error = excluded.last_error`,
		account, folder, time.Now().Unix(), msg)
	return err
}

func (s *Store) All() ([]FolderState, error) {
	rows, err := s.db.Query(
		`SELECT account, folder, uid_validity, last_uid, copied, skipped, last_run, last_error
		 FROM folder_state ORDER BY account, folder`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FolderState
	for rows.Next() {
		var st FolderState
		var lastRun int64
		if err := rows.Scan(&st.Account, &st.Folder, &st.UIDValidity, &st.LastUID,
			&st.Copied, &st.Skipped, &lastRun, &st.LastError); err != nil {
			return nil, err
		}
		if lastRun > 0 {
			st.LastRun = time.Unix(lastRun, 0)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
