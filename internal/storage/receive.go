package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/social/contact"
)

// Receipt is how one incoming message is treated.
type Receipt struct {
	Disposition contact.Disposition
	// Stranger is true when the sender was not a friend when it arrived.
	Stranger bool
	// Seq is the sender's message count while a stranger (1, 2, ...); zero
	// for friends.
	Seq int
}

// ClassifyReceive decides how to treat one incoming event and counts it.
// The first decision for an event ID is stored and returned again on every
// later call, so relay redelivery, a second relay or an inbox re-fetch never
// counts it twice. Order is local arrival order; created_at plays no part.
func (s *RelationStore) ClassifyReceive(identity, peer, eventID string, isContact bool, now int64) (Receipt, error) {
	tx, err := s.begin()
	if err != nil {
		return Receipt{}, err
	}
	defer tx.rollback()
	ctx := context.Background()
	var r Receipt
	var disp int
	err = tx.conn.QueryRowContext(ctx, `SELECT disposition, seq FROM receive_disposition
		WHERE identity_npub = ? AND event_id = ?`, identity, eventID).Scan(&disp, &r.Seq)
	if err == nil {
		r.Disposition, r.Stranger = contact.Disposition(disp), r.Seq > 0
		return r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Receipt{}, fmt.Errorf("load receipt: %w", err)
	}
	p, err := load(tx.conn, identity, peer)
	if err != nil {
		return Receipt{}, err
	}
	rel := effective(p.Relation, isContact)
	r.Disposition = contact.CheckReceive(rel)
	if rel.State == contact.Friend {
		return r, nil // friends are always shown; nothing to remember
	}
	if rel.State == contact.Stranger {
		p.Relation = contact.ApplyReceive(rel)
		if err := tx.save(identity, p, now); err != nil {
			return Receipt{}, err
		}
		r.Stranger, r.Seq = true, p.Relation.Received
	}
	if _, err := tx.conn.ExecContext(ctx, `INSERT INTO receive_disposition
		(identity_npub, event_id, peer_npub, disposition, seq) VALUES (?, ?, ?, ?, ?)`,
		identity, eventID, peer, int(r.Disposition), r.Seq); err != nil {
		return Receipt{}, fmt.Errorf("store receipt: %w", err)
	}
	return r, tx.commit()
}
