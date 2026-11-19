package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/social/contact"
)

// Relation storage for the stranger rules (docs/search/discover-friends.md).
// Every check-and-record runs inside one BEGIN IMMEDIATE transaction, so the
// CLI and the daemon sending at the same time cannot both pass the same check.

const relationSchema = `
CREATE TABLE IF NOT EXISTS peer_relation (
	identity_npub    TEXT NOT NULL,
	peer_npub        TEXT NOT NULL,
	state            TEXT NOT NULL CHECK (state IN ('stranger', 'friend', 'blocked')),
	sent             INTEGER NOT NULL DEFAULT 0 CHECK (sent >= 0),
	received         INTEGER NOT NULL DEFAULT 0 CHECK (received >= 0),
	unblocked_at     INTEGER NOT NULL DEFAULT 0,
	ignored          INTEGER NOT NULL DEFAULT 0 CHECK (ignored IN (0, 1)),
	first_contact_at INTEGER NOT NULL,
	updated_at       INTEGER NOT NULL,
	PRIMARY KEY (identity_npub, peer_npub)
);
CREATE TABLE IF NOT EXISTS stranger_initiation (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	identity_npub TEXT NOT NULL,
	peer_npub     TEXT NOT NULL,
	at            INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stranger_initiation_identity ON stranger_initiation(identity_npub, at);
CREATE TABLE IF NOT EXISTS receive_disposition (
	identity_npub TEXT NOT NULL,
	event_id      TEXT NOT NULL,
	peer_npub     TEXT NOT NULL,
	disposition   INTEGER NOT NULL,
	seq           INTEGER NOT NULL,
	PRIMARY KEY (identity_npub, event_id)
);
`

func migrateRelations(db *sql.DB) error {
	if _, err := db.Exec(relationSchema); err != nil {
		return fmt.Errorf("failed to create relation tables: %w", err)
	}
	return nil
}

// PeerRelation is one stored row.
type PeerRelation struct {
	Peer           string
	Relation       contact.Relation
	Ignored        bool
	FirstContactAt int64
	UpdatedAt      int64
}

// RelationStore reads and writes peer relations for local identities.
type RelationStore struct {
	db *sql.DB
}

// NewRelationStore wraps a database opened by OpenDB.
func NewRelationStore(db *sql.DB) *RelationStore {
	return &RelationStore{db: db}
}

func parseState(s string) (contact.State, error) {
	for _, st := range []contact.State{contact.Stranger, contact.Friend, contact.Blocked} {
		if st.String() == s {
			return st, nil
		}
	}
	return 0, fmt.Errorf("unknown relation state %q", s)
}

type relTx struct {
	conn *sql.Conn
	done bool
}

func (s *RelationStore) begin() (*relTx, error) {
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &relTx{conn: conn}, nil
}

func (t *relTx) commit() error {
	t.done = true
	defer func() { _ = t.conn.Close() }()
	_, err := t.conn.ExecContext(context.Background(), "COMMIT")
	return err
}

func (t *relTx) rollback() {
	if t.done {
		return
	}
	t.done = true
	_, _ = t.conn.ExecContext(context.Background(), "ROLLBACK")
	_ = t.conn.Close()
}

const relationCols = `peer_npub, state, sent, received, unblocked_at, ignored, first_contact_at, updated_at`

func scanRelation(row interface{ Scan(...any) error }) (PeerRelation, error) {
	var (
		p       PeerRelation
		state   string
		ignored int
	)
	if err := row.Scan(&p.Peer, &state, &p.Relation.Sent, &p.Relation.Received, &p.Relation.UnblockedAt,
		&ignored, &p.FirstContactAt, &p.UpdatedAt); err != nil {
		return p, err
	}
	var err error
	p.Relation.State, err = parseState(state)
	p.Ignored = ignored == 1
	return p, err
}

// load returns the stored row, or a zero stranger row when none exists.
func load(q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, identity, peer string) (PeerRelation, error) {
	p, err := scanRelation(q.QueryRowContext(context.Background(),
		`SELECT `+relationCols+` FROM peer_relation WHERE identity_npub = ? AND peer_npub = ?`, identity, peer))
	if errors.Is(err, sql.ErrNoRows) {
		return PeerRelation{Peer: peer}, nil
	}
	if err != nil {
		return p, fmt.Errorf("load relation: %w", err)
	}
	return p, nil
}

func (t *relTx) save(identity string, p PeerRelation, now int64) error {
	if p.FirstContactAt == 0 {
		p.FirstContactAt = now
	}
	ignored := 0
	if p.Ignored {
		ignored = 1
	}
	_, err := t.conn.ExecContext(context.Background(), `INSERT INTO peer_relation
		(identity_npub, peer_npub, state, sent, received, unblocked_at, ignored, first_contact_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (identity_npub, peer_npub) DO UPDATE SET state = excluded.state, sent = excluded.sent,
		received = excluded.received, unblocked_at = excluded.unblocked_at, ignored = excluded.ignored,
		updated_at = excluded.updated_at`,
		identity, p.Peer, p.Relation.State.String(), p.Relation.Sent, p.Relation.Received,
		p.Relation.UnblockedAt, ignored, p.FirstContactAt, now)
	if err != nil {
		return fmt.Errorf("save relation: %w", err)
	}
	return nil
}

func (t *relTx) initiations(identity string, now int64) ([]int64, error) {
	ctx := context.Background()
	// Rows that left the window can never count again; drop them. Rows dated
	// in the future stay (see contact.QuotaAt).
	if _, err := t.conn.ExecContext(ctx, `DELETE FROM stranger_initiation WHERE identity_npub = ? AND at <= ?`,
		identity, now-contact.Window); err != nil {
		return nil, fmt.Errorf("prune initiations: %w", err)
	}
	rows, err := t.conn.QueryContext(ctx, `SELECT at FROM stranger_initiation WHERE identity_npub = ?`, identity)
	if err != nil {
		return nil, fmt.Errorf("load initiations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var at int64
		if err := rows.Scan(&at); err != nil {
			return nil, err
		}
		out = append(out, at)
	}
	return out, rows.Err()
}

// effective overlays keystore contacts: a contact is a friend unless the
// stored row says blocked.
func effective(r contact.Relation, isContact bool) contact.Relation {
	if isContact && r.State != contact.Blocked {
		return contact.Relation{State: contact.Friend}
	}
	return r
}

// ReserveSend checks one new message from identity to peer and, if allowed,
// records it (and the initiation) before returning. Call it once per logical
// message, before queueing; retries of a queued message must not call it.
func (s *RelationStore) ReserveSend(identity, peer string, isContact bool, now int64) (contact.SendDecision, error) {
	tx, err := s.begin()
	if err != nil {
		return contact.SendDecision{}, err
	}
	defer tx.rollback()
	p, err := load(tx.conn, identity, peer)
	if err != nil {
		return contact.SendDecision{}, err
	}
	rel := effective(p.Relation, isContact)
	if rel.State == contact.Friend {
		return contact.SendDecision{}, nil
	}
	inits, err := tx.initiations(identity, now)
	if err != nil {
		return contact.SendDecision{}, err
	}
	d, err := contact.CheckSend(rel, inits, now)
	if err != nil {
		return d, err
	}
	p.Relation = contact.ApplySend(rel, d)
	if err := tx.save(identity, p, now); err != nil {
		return d, err
	}
	if d.Initiation {
		if _, err := tx.conn.ExecContext(context.Background(),
			`INSERT INTO stranger_initiation (identity_npub, peer_npub, at) VALUES (?, ?, ?)`, identity, peer, now); err != nil {
			return d, fmt.Errorf("record initiation: %w", err)
		}
	}
	return d, tx.commit()
}

// Update applies fn to the stored relation (zero value if none) inside one
// transaction: accept, block, unblock, ignore and friend_accept use it.
func (s *RelationStore) Update(identity, peer string, now int64, fn func(PeerRelation) (PeerRelation, error)) (PeerRelation, error) {
	tx, err := s.begin()
	if err != nil {
		return PeerRelation{}, err
	}
	defer tx.rollback()
	p, err := load(tx.conn, identity, peer)
	if err != nil {
		return PeerRelation{}, err
	}
	next, err := fn(p)
	if err != nil {
		return p, err
	}
	next.Peer = peer
	if err := tx.save(identity, next, now); err != nil {
		return p, err
	}
	return next, tx.commit()
}

// Get returns the stored relation, or a zero stranger row when none exists.
func (s *RelationStore) Get(identity, peer string) (PeerRelation, error) {
	return load(s.db, identity, peer)
}

// Quota reports identity's daily stranger quota at now.
func (s *RelationStore) Quota(identity string, now int64) (contact.Quota, error) {
	tx, err := s.begin()
	if err != nil {
		return contact.Quota{}, err
	}
	defer tx.rollback()
	inits, err := tx.initiations(identity, now)
	if err != nil {
		return contact.Quota{}, err
	}
	return contact.QuotaAt(inits, now), tx.commit()
}

// List returns every stored relation of identity, ordered by peer.
func (s *RelationStore) List(identity string) ([]PeerRelation, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT `+relationCols+` FROM peer_relation WHERE identity_npub = ? ORDER BY peer_npub`, identity)
	if err != nil {
		return nil, fmt.Errorf("list relations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PeerRelation
	for rows.Next() {
		p, err := scanRelation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// FirstIncoming returns the plaintext and event ID of the first message
// identity received from peer, by local receive time, or "" when none.
func FirstIncoming(db *sql.DB, identity, peer string) (text, eventID string, err error) {
	var t, id sql.NullString
	err = db.QueryRowContext(context.Background(), `SELECT COALESCE(plaintext, content), event_id FROM messages
		WHERE recipient_npub = ? AND sender_npub = ? AND is_incoming = 1 ORDER BY received_at, id LIMIT 1`,
		identity, peer).Scan(&t, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("first message: %w", err)
	}
	return t.String, id.String, nil
}

// SentAt reports whether identity sent event eventID to peer, and the local
// time it was stored.
func SentAt(db *sql.DB, identity, peer, eventID string) (int64, bool, error) {
	var at int64
	err := db.QueryRowContext(context.Background(), `SELECT received_at FROM messages
		WHERE event_id = ? AND sender_npub = ? AND recipient_npub = ? AND is_incoming = 0`,
		eventID, identity, peer).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("sent message: %w", err)
	}
	return at, true, nil
}

// PreviewSends checks one new message from identity to each peer without
// recording anything: the same rules ReserveSend applies, with each would-be
// initiation counted against the ones after it. A caller about to reserve a
// batch (group invites) uses it so a refusal part-way spends no quota.
func (s *RelationStore) PreviewSends(identity string, peers []string, isContact func(string) bool, now int64) error {
	tx, err := s.begin()
	if err != nil {
		return err
	}
	defer tx.rollback()
	inits, err := tx.initiations(identity, now)
	if err != nil {
		return err
	}
	for _, peer := range peers {
		p, err := load(tx.conn, identity, peer)
		if err != nil {
			return err
		}
		rel := effective(p.Relation, isContact(peer))
		if rel.State == contact.Friend {
			continue
		}
		d, err := contact.CheckSend(rel, inits, now)
		if err != nil {
			return SendRefusal(err, peer)
		}
		if d.Initiation {
			inits = append(inits, now)
		}
	}
	return nil
}
