// Package audit maintains a local, append-only, tamper-evident log of
// security/state-relevant actions (identity creation, message send/receive,
// group membership changes, daemon auto-replies). Each row's hash covers the
// previous row's hash, so any edit or deletion breaks the chain from that
// point forward — verifiable with VerifyChain.
//
// This is a single-writer, single-machine design (SQLite itself serializes
// writes; LogAction does a read-then-insert inside one transaction), not the
// multi-tenant model Buzz's equivalent (buzz-audit) solves for — see
// specs/m1.5/tasks/04-audit-log-hash-chain.md for why the simpler design is
// the right call here.
package audit

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// keyStoreDirName mirrors internal/identity.KeyStoreDirName. Duplicated
// (rather than imported) deliberately: internal/identity's CLI commands are
// the primary caller of LogAction, and internal/identity importing this
// package while this package imported internal/identity back would be a
// cycle. This is 3 lines of path-joining logic, not worth restructuring the
// package graph over.
const keyStoreDirName = ".agent-speaker"

func keyStoreDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, keyStoreDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create keystore directory: %w", err)
	}
	return dir, nil
}

// Action identifies the kind of event being recorded.
type Action string

const (
	ActionIdentityCreated    Action = "identity_created"
	ActionContactAdded       Action = "contact_added"
	ActionMessageSent        Action = "message_sent"
	ActionMessageReceived    Action = "message_received"
	ActionGroupCreated       Action = "group_created"
	ActionGroupMemberAdded   Action = "group_member_added"
	ActionGroupMemberRemoved Action = "group_member_removed"
	ActionAutoReplySent      Action = "auto_reply_sent"
)

// genesisHash is the prev_hash value for the first row in the chain: 64 "0"
// characters, matching the length of a real sha256 hex digest.
const genesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Entry is one row of the audit log, as returned by List.
type Entry struct {
	Seq       int64          `json:"seq"`
	Timestamp int64          `json:"ts"`
	Actor     string         `json:"actor"`
	Action    string         `json:"action"`
	Details   map[string]any `json:"details"`
	PrevHash  string         `json:"prev_hash"`
	Hash      string         `json:"hash"`
}

var (
	dbMu sync.Mutex
	db   *sql.DB
)

// getDB lazily opens (and schema-initializes) a dedicated connection to the
// same messages.db file internal/storage uses. This package deliberately
// does not import internal/storage: storage already imports internal/identity
// (for the keystore directory path), and internal/identity is one of this
// package's own callers, so importing storage here would create a cycle.
// Opening a second *sql.DB handle to the same WAL-mode SQLite file is safe.
//
// Deliberately does NOT use sync.Once: a one-shot CLI command failing to
// initialize once and giving up for the rest of that process is fine, but
// the daemon is long-running, and a transient failure (e.g. a momentary disk
// hiccup) on the first audit write of a multi-day process would otherwise
// silently disable audit logging for its entire remaining lifetime. Caching
// only the success (db stays nil until one fully succeeds) means every call
// retries init from scratch until it works.
func getDB() (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db != nil {
		return db, nil
	}

	dir, err := keyStoreDir()
	if err != nil {
		return nil, fmt.Errorf("audit: failed to resolve keystore dir: %w", err)
	}
	path := filepath.Join(dir, "messages.db")

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("audit: failed to open db: %w", err)
	}
	if _, err := conn.Exec("PRAGMA journal_mode = WAL"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("audit: failed to enable WAL mode: %w", err)
	}
	if _, err := conn.Exec(schemaSQL); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("audit: failed to create schema: %w", err)
	}

	db = conn
	return db, nil
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS audit_log (
	seq       INTEGER PRIMARY KEY,
	ts        INTEGER NOT NULL,
	actor     TEXT NOT NULL,
	action    TEXT NOT NULL,
	details   TEXT NOT NULL,
	prev_hash TEXT NOT NULL,
	hash      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_log_actor ON audit_log(actor);
CREATE INDEX IF NOT EXISTS idx_audit_log_action ON audit_log(action);
`

// hashInput is the exact, ordered set of fields covered by each entry's hash.
// Deliberately hashed via json.Marshal rather than delimiter-joined
// (e.g. "%d|%d|%s|%s|%s|%s") — a plain-delimiter preimage is not
// self-delimiting: a value containing "|" could shift field boundaries and
// produce the same preimage for two different logical tuples. JSON escapes
// quotes/backslashes within string fields, so encoding a fixed struct with a
// fixed field order can't be confused across field boundaries regardless of
// what actor/action/details contain.
type hashInput struct {
	Seq      int64  `json:"seq"`
	Ts       int64  `json:"ts"`
	Actor    string `json:"actor"`
	Action   string `json:"action"`
	Details  string `json:"details"`
	PrevHash string `json:"prev_hash"`
}

func computeHash(seq, ts int64, actor, action, detailsJSON, prevHash string) string {
	// json.Marshal of this fixed, all-marshalable-typed struct never errors.
	b, _ := json.Marshal(hashInput{
		Seq: seq, Ts: ts, Actor: actor, Action: action, Details: detailsJSON, PrevHash: prevHash,
	})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// LogAction appends one entry to the audit chain. Callers should treat a
// non-nil error as non-fatal to whatever operation triggered the log entry
// (identity creation, message send, etc.) — audit logging is fire-and-forget
// by design; see each call site.
func LogAction(actor string, action Action, details map[string]any) error {
	conn, err := getDB()
	if err != nil {
		return err
	}
	if details == nil {
		details = map[string]any{}
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("audit: failed to marshal details: %w", err)
	}

	tx, err := conn.Begin()
	if err != nil {
		return fmt.Errorf("audit: failed to begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var lastSeq sql.NullInt64
	var lastHash sql.NullString
	row := tx.QueryRow(`SELECT seq, hash FROM audit_log ORDER BY seq DESC LIMIT 1`)
	if err := row.Scan(&lastSeq, &lastHash); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("audit: failed to read chain tail: %w", err)
	}

	nextSeq := int64(1)
	prevHash := genesisHash
	if lastSeq.Valid {
		nextSeq = lastSeq.Int64 + 1
		prevHash = lastHash.String
	}

	ts := time.Now().Unix()
	hash := computeHash(nextSeq, ts, actor, string(action), string(detailsJSON), prevHash)

	if _, err := tx.Exec(
		`INSERT INTO audit_log (seq, ts, actor, action, details, prev_hash, hash) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		nextSeq, ts, actor, string(action), string(detailsJSON), prevHash, hash,
	); err != nil {
		return fmt.Errorf("audit: failed to insert entry: %w", err)
	}

	return tx.Commit()
}

// VerifyChain walks the whole chain in order and recomputes every hash. ok is
// true iff every row's prev_hash matches the previous row's hash and every
// row's own hash matches its recomputed value. brokenAtSeq identifies the
// first row where that's not the case (0 if ok or the table is empty).
func VerifyChain() (ok bool, brokenAtSeq int64, err error) {
	conn, err := getDB()
	if err != nil {
		return false, 0, err
	}

	rows, err := conn.Query(`SELECT seq, ts, actor, action, details, prev_hash, hash FROM audit_log ORDER BY seq ASC`)
	if err != nil {
		return false, 0, fmt.Errorf("audit: failed to query chain: %w", err)
	}
	defer rows.Close()

	expectedPrev := genesisHash
	for rows.Next() {
		var seq, ts int64
		var actor, action, details, prevHash, hash string
		if err := rows.Scan(&seq, &ts, &actor, &action, &details, &prevHash, &hash); err != nil {
			return false, 0, fmt.Errorf("audit: failed to scan row: %w", err)
		}
		if prevHash != expectedPrev {
			return false, seq, nil
		}
		if computeHash(seq, ts, actor, action, details, prevHash) != hash {
			return false, seq, nil
		}
		expectedPrev = hash
	}
	if err := rows.Err(); err != nil {
		return false, 0, err
	}
	return true, 0, nil
}

// List returns up to limit entries, most recent first. limit <= 0 means no limit.
func List(limit int) ([]Entry, error) {
	conn, err := getDB()
	if err != nil {
		return nil, err
	}

	query := `SELECT seq, ts, actor, action, details, prev_hash, hash FROM audit_log ORDER BY seq DESC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: failed to query entries: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		var detailsJSON string
		if err := rows.Scan(&e.Seq, &e.Timestamp, &e.Actor, &e.Action, &detailsJSON, &e.PrevHash, &e.Hash); err != nil {
			return nil, fmt.Errorf("audit: failed to scan entry: %w", err)
		}
		if err := json.Unmarshal([]byte(detailsJSON), &e.Details); err != nil {
			e.Details = map[string]any{"_unparsed": detailsJSON}
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// resetForTest closes and forgets the cached connection so the next getDB
// call re-resolves the path and re-initializes. Test-only.
func resetForTest() {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db != nil {
		_ = db.Close()
	}
	db = nil
}
