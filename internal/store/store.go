package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"path/filepath"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Store is a thin wrapper around an SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies the schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite is single-writer; keep one connection to avoid "database is locked".
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func now() int64 { return time.Now().Unix() }

// ---------------------------------------------------------------------------
// Telegram users
// ---------------------------------------------------------------------------

const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusBlocked  = "blocked"
)

// TGUser is a Telegram subscriber row.
type TGUser struct {
	TGID      int64
	Username  string
	FirstName string
	Status    string
	VKUserID  sql.NullInt64
}

// Linked reports whether the user has a VK account attached.
func (u TGUser) Linked() bool { return u.VKUserID.Valid }

// UpsertTGUser inserts the user if new (status=pending) or refreshes profile fields.
// Returns true if a new row was created.
func (s *Store) UpsertTGUser(tgID int64, username, firstName string) (created bool, err error) {
	var exists int
	err = s.db.QueryRow(`SELECT 1 FROM tg_users WHERE tg_id=?`, tgID).Scan(&exists)
	switch {
	case err == sql.ErrNoRows:
		_, err = s.db.Exec(`
			INSERT INTO tg_users (tg_id, username, first_name, status, created_at)
			VALUES (?, ?, ?, 'pending', ?)`, tgID, username, firstName, now())
		return err == nil, err
	case err != nil:
		return false, err
	default:
		_, err = s.db.Exec(`UPDATE tg_users SET username=?, first_name=? WHERE tg_id=?`,
			username, firstName, tgID)
		return false, err
	}
}

// GetTGUser returns the user or (nil, nil) if absent.
func (s *Store) GetTGUser(tgID int64) (*TGUser, error) {
	u := &TGUser{}
	err := s.db.QueryRow(`
		SELECT tg_id, username, first_name, status, vk_user_id
		FROM tg_users WHERE tg_id=?`, tgID).
		Scan(&u.TGID, &u.Username, &u.FirstName, &u.Status, &u.VKUserID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// SetStatus updates a user's approval status (stamping approved_at on approval).
func (s *Store) SetStatus(tgID int64, status string) error {
	if status == StatusApproved {
		_, err := s.db.Exec(`UPDATE tg_users SET status=?, approved_at=? WHERE tg_id=?`, status, now(), tgID)
		return err
	}
	_, err := s.db.Exec(`UPDATE tg_users SET status=? WHERE tg_id=?`, status, tgID)
	return err
}

// ApprovedUsers returns all users with status=approved.
func (s *Store) ApprovedUsers() ([]TGUser, error) {
	rows, err := s.db.Query(`
		SELECT tg_id, username, first_name, status, vk_user_id
		FROM tg_users WHERE status='approved' ORDER BY tg_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsers(rows)
}

// PendingUsers returns all users awaiting approval.
func (s *Store) PendingUsers() ([]TGUser, error) {
	rows, err := s.db.Query(`
		SELECT tg_id, username, first_name, status, vk_user_id
		FROM tg_users WHERE status='pending' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsers(rows)
}

func scanUsers(rows *sql.Rows) ([]TGUser, error) {
	var out []TGUser
	for rows.Next() {
		var u TGUser
		if err := rows.Scan(&u.TGID, &u.Username, &u.FirstName, &u.Status, &u.VKUserID); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetVKLink attaches a VK user id to a TG user. Fails if the VK id is already taken.
func (s *Store) SetVKLink(tgID int64, vkUserID int64) error {
	_, err := s.db.Exec(`UPDATE tg_users SET vk_user_id=?, linked_at=? WHERE tg_id=?`, vkUserID, now(), tgID)
	return err
}

// GetUserByVK returns the TG user linked to a VK id, or (nil, nil).
func (s *Store) GetUserByVK(vkUserID int64) (*TGUser, error) {
	u := &TGUser{}
	err := s.db.QueryRow(`
		SELECT tg_id, username, first_name, status, vk_user_id
		FROM tg_users WHERE vk_user_id=?`, vkUserID).
		Scan(&u.TGID, &u.Username, &u.FirstName, &u.Status, &u.VKUserID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// ---------------------------------------------------------------------------
// Link codes
// ---------------------------------------------------------------------------

// CreateLinkCode stores a one-time code for a TG user, replacing any prior unused ones.
func (s *Store) CreateLinkCode(code string, tgID int64, ttl time.Duration) error {
	_, _ = s.db.Exec(`DELETE FROM link_codes WHERE tg_id=? AND used_at IS NULL`, tgID)
	_, err := s.db.Exec(`
		INSERT INTO link_codes (code, tg_id, created_at, expires_at)
		VALUES (?, ?, ?, ?)`, code, tgID, now(), time.Now().Add(ttl).Unix())
	return err
}

// ConsumeLinkCode validates and marks a code used, returning the owning tg_id.
// ok=false if the code is unknown, expired, or already used.
func (s *Store) ConsumeLinkCode(code string) (tgID int64, ok bool, err error) {
	var expires int64
	var used sql.NullInt64
	err = s.db.QueryRow(`SELECT tg_id, expires_at, used_at FROM link_codes WHERE code=?`, code).
		Scan(&tgID, &expires, &used)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if used.Valid || now() > expires {
		return 0, false, nil
	}
	if _, err := s.db.Exec(`UPDATE link_codes SET used_at=? WHERE code=?`, now(), code); err != nil {
		return 0, false, err
	}
	return tgID, true, nil
}

// ---------------------------------------------------------------------------
// Broadcasts
// ---------------------------------------------------------------------------

// RecordBroadcast logs an @all message; returns false if this (peer,cmid) was already seen.
func (s *Store) RecordBroadcast(peerID, fromID, cmid int64, text string) (fresh bool, err error) {
	res, err := s.db.Exec(`
		INSERT OR IGNORE INTO broadcasts (vk_peer_id, vk_from_id, vk_cmid, text, created_at)
		VALUES (?, ?, ?, ?, ?)`, peerID, fromID, cmid, text, now())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
