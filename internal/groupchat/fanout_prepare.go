package groupchat

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
)

// PrepareMessageFanout commits sender history and encrypted, signed recipient
// events in one SQLite transaction. It neither enqueues nor publishes anything.
// A repeated intent returns its frozen events and time, without signing again.
func (s *Store) PrepareMessageFanout(senderSK nostr.SecretKey, groupID, logicalID, body string, createdAt nostr.Timestamp, maxRetries int) (FanoutReport, error) {
	tx, err := s.beginImmediate()
	if err != nil {
		return FanoutReport{}, err
	}
	defer tx.Rollback()
	e := Envelope{Version: Version, Type: EnvelopeMessage, GroupID: groupID, LogicalID: logicalID, Body: body}
	if err = s.prepareFanoutTx(tx, senderSK, e, createdAt, maxRetries, messaging.BuildAgentMessageEvent); err != nil {
		return FanoutReport{}, err
	}
	if err = tx.Commit(); err != nil {
		return FanoutReport{}, err
	}
	return s.LoadFanoutReport(FanoutKey{LocalNpub: common.EncodeNpub(senderSK.Public()), GroupID: groupID, EnvelopeType: EnvelopeMessage, SendKey: logicalID})
}

type fanoutEventBuilder func(nostr.SecretKey, nostr.PubKey, string, nostr.Timestamp) (*nostr.Event, error)

// prepareFanoutTx is T1's message-only boundary. The caller must hold a write
// transaction and roll it back on any error, including a failed commit. build
// is scoped to this call so fault injection cannot affect concurrent callers.
func (s *Store) prepareFanoutTx(tx queryExecer, senderSK nostr.SecretKey, e Envelope, createdAt nostr.Timestamp, maxRetries int, build fanoutEventBuilder) error {
	if senderSK == (nostr.SecretKey{}) || maxRetries <= 0 || createdAt < 0 || build == nil {
		return errors.New("invalid fanout preparation parameters")
	}
	if e.Type != EnvelopeMessage {
		return ErrProtocolMismatch
	}
	plaintext, err := Encode(e)
	if err != nil {
		return err
	}
	if err = validateBody(e.Body); err != nil {
		return err
	}
	local := common.EncodeNpub(senderSK.Public())
	group, err := loadGroup(tx, local, e.GroupID)
	if err != nil {
		return err
	}
	if group.State == StateLeft {
		return ErrGroupLeft
	}
	if group.State != StateActive {
		return ErrGroupNotActive
	}
	hash, roster, err := CanonicalRosterHash(group.ID, group.Creator, group.Name, group.Roster)
	if err != nil || hash != group.RosterHash || !sameStrings(roster, group.Roster) || !contains(roster, local) {
		return ErrProtocolMismatch
	}
	recipients := make([]string, 0, len(roster)-1)
	for _, member := range roster {
		if _, err = activeRosterTx(tx, local, e.GroupID, member); err != nil {
			return err
		}
		if member != local {
			recipients = append(recipients, member)
		}
	}
	var memberCount int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM groupchat_members WHERE local_npub=? AND group_id=?`, local, e.GroupID).Scan(&memberCount); err != nil {
		return err
	}
	if memberCount != len(roster) {
		return ErrProtocolMismatch
	}
	// Existing history is authoritative for both body and time, including history
	// written by S2 before the first fanout preparation.
	var sender, body, eventID string
	var historyAt int64
	err = tx.QueryRow(`SELECT sender_npub,body,created_at,COALESCE(event_id,'') FROM groupchat_messages WHERE local_npub=? AND group_id=? AND logical_id=?`, local, e.GroupID, e.LogicalID).Scan(&sender, &body, &historyAt, &eventID)
	historyExists := err == nil
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if historyExists {
		if sender != local || body != e.Body {
			return ErrLogicalIDConflict
		}
		if historyAt <= 0 || eventID != "" {
			return ErrProtocolMismatch
		}
		createdAt = nostr.Timestamp(historyAt)
	}
	rows, err := tx.Query(`SELECT recipient_npub,event_id,event_json,created_at,max_retries FROM groupchat_fanout WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? ORDER BY recipient_npub`, local, e.GroupID, e.Type, e.LogicalID)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var recipient, id, raw string
		var at int64
		var budget int
		if err = rows.Scan(&recipient, &id, &raw, &at, &budget); err != nil {
			break
		}
		if !historyExists || count >= len(recipients) || recipient != recipients[count] || at != historyAt || budget != maxRetries {
			err = ErrProtocolMismatch
			break
		}
		var event nostr.Event
		if err = json.Unmarshal([]byte(raw), &event); err != nil {
			break
		}
		if event.ID.Hex() != id {
			err = ErrProtocolMismatch
			break
		}
		pk, parseErr := common.ParsePublicKey(recipient)
		if parseErr != nil {
			err = parseErr
			break
		}
		if err = validatePreparedEvent(&event, senderSK, pk, plaintext, createdAt); err != nil {
			break
		}
		count++
	}
	scanErr := rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if scanErr != nil {
		return scanErr
	}
	if closeErr != nil {
		return closeErr
	}
	if count > 0 {
		if count != len(recipients) {
			return ErrProtocolMismatch
		}
		return nil
	}
	if createdAt == 0 {
		createdAt = nostr.Timestamp(time.Now().Unix())
	}
	if _, err = s.storeLocalMessageTx(tx, local, e.GroupID, e.LogicalID, e.Body, int64(createdAt)); err != nil {
		return err
	}
	for _, recipient := range recipients {
		pk, err := common.ParsePublicKey(recipient)
		if err != nil {
			return err
		}
		event, err := build(senderSK, pk, plaintext, createdAt)
		if err != nil {
			return fmt.Errorf("build fanout event: %w", err)
		}
		if err = validatePreparedEvent(event, senderSK, pk, plaintext, createdAt); err != nil {
			return err
		}
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		// F1 native defaults initialize revision/generation/watermarks to zero,
		// phase=idle, no queue and unknown retry budget. No secrets are persisted.
		_, err = tx.Exec(`INSERT INTO groupchat_fanout (local_npub,group_id,envelope_type,send_key,recipient_npub,event_id,event_json,state,max_retries,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, local, e.GroupID, e.Type, e.LogicalID, recipient, event.ID.Hex(), string(raw), RecipientPrepared, maxRetries, int64(createdAt), int64(createdAt))
		if err != nil {
			return fmt.Errorf("store prepared fanout: %w", err)
		}
	}
	return nil
}

// Validate the builder's output (and frozen replay) against the exact T1 input.
// NIP-44's shared conversation key also permits sender-side verification.
func validatePreparedEvent(event *nostr.Event, sender nostr.SecretKey, recipient nostr.PubKey, plaintext string, at nostr.Timestamp) error {
	if event == nil || event.Kind != messaging.AgentKind || event.PubKey != sender.Public() || event.CreatedAt != at || !event.CheckID() || !event.VerifySignature() {
		return ErrProtocolMismatch
	}
	if err := messaging.ValidateAgentMessageEvent(event); err != nil {
		return err
	}
	pCount, encCount := 0, 0
	for _, tag := range event.Tags {
		if len(tag) == 0 {
			continue
		}
		switch tag[0] {
		case "p":
			pCount++
			if len(tag) != 2 || tag[1] != common.PubKeyToHex(recipient) {
				return ErrProtocolMismatch
			}
		case "enc":
			encCount++
			if len(tag) != 2 || tag[1] != "nip44" {
				return ErrProtocolMismatch
			}
		}
	}
	if pCount != 1 || encCount != 1 {
		return ErrProtocolMismatch
	}
	ciphertext, err := messaging.DecompressText(event.Content)
	if err != nil {
		return errors.New("invalid prepared ciphertext")
	}
	decoded, err := crypto.DecryptMessage(ciphertext, sender, recipient)
	if err != nil || decoded != plaintext {
		return ErrProtocolMismatch
	}
	return nil
}
