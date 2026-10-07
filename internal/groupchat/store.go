package groupchat

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/wireevent"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

type GroupState string

const (
	StatePending    GroupState = "pending"
	StateActivating GroupState = "activating"
	StateActive     GroupState = "active"
	StateLeft       GroupState = "left"
	StateCancelled  GroupState = "cancelled"
)

type InviteState string

const (
	InvitePending   InviteState = "pending"
	InviteAccepted  InviteState = "accepted"
	InviteDeclined  InviteState = "declined"
	InviteActive    InviteState = "active"
	InviteCancelled InviteState = "cancelled"
)

var (
	ErrGroupNotFound     = errors.New("group not found")
	ErrInviteNotFound    = errors.New("group invitation not found")
	ErrGroupNotActive    = errors.New("group is not active")
	ErrGroupLeft         = errors.New("group was left locally")
	ErrIdentityMismatch  = errors.New("current identity does not match group invitation")
	ErrProtocolMismatch  = errors.New("group protocol identity or roster mismatch")
	ErrInvalidTransition = errors.New("invalid group state transition")
	ErrLogicalIDConflict = errors.New("group logical message ID conflict")
)

// Store owns only the groupchat_* tables. It may share SQLite with existing
// Hyphae storage without changing the legacy local group tables.
type Store struct{ db *sql.DB }

type Group struct {
	LocalNpub  string
	ID         string
	Name       string
	Creator    string
	Roster     []string
	RosterHash string
	State      GroupState
	CreatedAt  int64
	UpdatedAt  int64
}

type GroupDraft struct {
	Group       Group
	Invitations []Envelope
}

// NewStore creates the isolated group-chat tables on the provided database.
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("groupchat database is required")
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS groupchat_groups (
			local_npub TEXT NOT NULL,
			group_id TEXT NOT NULL,
			name TEXT NOT NULL,
			creator_npub TEXT NOT NULL,
			roster_json TEXT NOT NULL,
			roster_hash TEXT NOT NULL,
			state TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (local_npub, group_id)
		)`,
		`CREATE TABLE IF NOT EXISTS groupchat_members (
			local_npub TEXT NOT NULL,
			group_id TEXT NOT NULL,
			npub TEXT NOT NULL,
			invite_id TEXT,
			state TEXT NOT NULL,
			is_creator INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (local_npub, group_id, npub),
			UNIQUE (local_npub, group_id, invite_id)
		)`,
		`CREATE TABLE IF NOT EXISTS groupchat_invites (
			local_npub TEXT NOT NULL,
			invite_id TEXT NOT NULL,
			group_id TEXT NOT NULL,
			creator_npub TEXT NOT NULL,
			invitee_npub TEXT NOT NULL,
			name TEXT NOT NULL,
			roster_json TEXT NOT NULL,
			roster_hash TEXT NOT NULL,
			state TEXT NOT NULL,
			invite_event_id TEXT NOT NULL DEFAULT '',
			accept_event_id TEXT NOT NULL DEFAULT '',
			activation_event_id TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (local_npub, invite_id),
			UNIQUE (local_npub, group_id, invitee_npub)
		)`,
		`CREATE TABLE IF NOT EXISTS groupchat_messages (
			local_npub TEXT NOT NULL,
			group_id TEXT NOT NULL,
			logical_id TEXT NOT NULL,
			sender_npub TEXT NOT NULL,
			body TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			event_id TEXT,
			PRIMARY KEY (local_npub, group_id, logical_id),
			UNIQUE (event_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_groupchat_invites_state ON groupchat_invites(local_npub, state)`,
		`CREATE INDEX IF NOT EXISTS idx_groupchat_messages_order ON groupchat_messages(local_npub, group_id, created_at, logical_id)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("create groupchat schema: %w", err)
		}
	}
	return nil
}

// CreateGroup stores one immutable initial roster and creates one distinct
// pending invitation per non-creator member. The creator's create action is
// its own explicit consent; the group is not active until all invitees accept
// and every activation event is durably queued.
func (s *Store) CreateGroup(name, creator string, invitees []string) (GroupDraft, error) {
	creator, err := canonicalNpub(creator)
	if err != nil {
		return GroupDraft{}, fmt.Errorf("invalid creator: %w", err)
	}
	members := append([]string{creator}, invitees...)
	groupID, err := NewOpaqueID()
	if err != nil {
		return GroupDraft{}, err
	}
	rosterHash, roster, err := CanonicalRosterHash(groupID, creator, name, members)
	if err != nil {
		return GroupDraft{}, err
	}
	now := time.Now().Unix()
	draft := GroupDraft{
		Group: Group{LocalNpub: creator, ID: groupID, Name: name, Creator: creator,
			Roster: roster, RosterHash: rosterHash, State: StatePending, CreatedAt: now, UpdatedAt: now},
	}
	for _, member := range roster {
		if member == creator {
			continue
		}
		inviteID, err := NewOpaqueID()
		if err != nil {
			return GroupDraft{}, err
		}
		draft.Invitations = append(draft.Invitations, Envelope{
			Type: EnvelopeInvite, Version: Version, GroupID: groupID, CreatorNpub: creator,
			InviteID: inviteID, RosterHash: rosterHash, InviteeNpub: member,
			Name: name, Members: append([]string(nil), roster...),
		})
	}

	rosterJSON, err := json.Marshal(roster)
	if err != nil {
		return GroupDraft{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return GroupDraft{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO groupchat_groups
		(local_npub, group_id, name, creator_npub, roster_json, roster_hash, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, creator, groupID, name, creator, string(rosterJSON), rosterHash, StatePending, now, now); err != nil {
		return GroupDraft{}, fmt.Errorf("store group draft: %w", err)
	}
	for _, member := range roster {
		state := "pending"
		isCreator := 0
		var inviteID any
		if member == creator {
			state = "accepted"
			isCreator = 1
		} else {
			for _, invitation := range draft.Invitations {
				if invitation.InviteeNpub == member {
					inviteID = invitation.InviteID
					break
				}
			}
		}
		if _, err := tx.Exec(`INSERT INTO groupchat_members
			(local_npub, group_id, npub, invite_id, state, is_creator) VALUES (?, ?, ?, ?, ?, ?)`,
			creator, groupID, member, inviteID, state, isCreator); err != nil {
			return GroupDraft{}, fmt.Errorf("store group member: %w", err)
		}
	}
	for _, invitation := range draft.Invitations {
		if err := insertInvite(tx, creator, invitation, InvitePending, "", now); err != nil {
			return GroupDraft{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return GroupDraft{}, fmt.Errorf("commit group draft: %w", err)
	}
	return draft, nil
}

// ReceiveInvite persists only a pending invitation. Replayed invitations do
// not reactivate a group that was accepted, cancelled, or locally left.
func (s *Store) ReceiveInvite(v VerifiedIncoming) (bool, error) {
	e, err := envelopeFrom(v, EnvelopeInvite)
	if err != nil {
		return false, err
	}
	if v.senderNpub != e.CreatorNpub || v.recipientNpub != e.InviteeNpub {
		return false, ErrProtocolMismatch
	}
	rosterJSON, _ := json.Marshal(e.Members)
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var existingName, existingCreator, existingRoster, existingHash, existingState string
	err = tx.QueryRow(`SELECT name, creator_npub, roster_json, roster_hash, state FROM groupchat_groups
		WHERE local_npub = ? AND group_id = ?`, v.recipientNpub, e.GroupID).
		Scan(&existingName, &existingCreator, &existingRoster, &existingHash, &existingState)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == sql.ErrNoRows {
		if _, err := tx.Exec(`INSERT INTO groupchat_groups
			(local_npub, group_id, name, creator_npub, roster_json, roster_hash, state, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, v.recipientNpub, e.GroupID, e.Name, e.CreatorNpub,
			string(rosterJSON), e.RosterHash, StatePending, now, now); err != nil {
			return false, fmt.Errorf("store invited group: %w", err)
		}
		for _, member := range e.Members {
			isCreator := 0
			if member == e.CreatorNpub {
				isCreator = 1
			}
			if _, err := tx.Exec(`INSERT INTO groupchat_members
				(local_npub, group_id, npub, state, is_creator) VALUES (?, ?, ?, ?, ?)`,
				v.recipientNpub, e.GroupID, member, "pending", isCreator); err != nil {
				return false, fmt.Errorf("store invited roster: %w", err)
			}
		}
	} else {
		if existingName != e.Name || existingCreator != e.CreatorNpub || existingRoster != string(rosterJSON) || existingHash != e.RosterHash {
			return false, ErrProtocolMismatch
		}
		if existingState == string(StateLeft) || existingState == string(StateCancelled) {
			return false, ErrInvalidTransition
		}
	}

	var oldGroup, oldCreator, oldInvitee, oldHash, oldState string
	err = tx.QueryRow(`SELECT group_id, creator_npub, invitee_npub, roster_hash, state FROM groupchat_invites
		WHERE local_npub = ? AND invite_id = ?`, v.recipientNpub, e.InviteID).
		Scan(&oldGroup, &oldCreator, &oldInvitee, &oldHash, &oldState)
	if err == nil {
		if oldGroup != e.GroupID || oldCreator != e.CreatorNpub || oldInvitee != e.InviteeNpub || oldHash != e.RosterHash {
			return false, ErrProtocolMismatch
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	if err := insertInvite(tx, v.recipientNpub, e, InvitePending, v.eventID, now); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE groupchat_members SET invite_id = ? WHERE local_npub = ? AND group_id = ? AND npub = ?`,
		e.InviteID, v.recipientNpub, e.GroupID, e.InviteeNpub); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// AcceptInvite explicitly records acceptance for the selected identity and
// returns the signed-response payload for the caller to encrypt and send.
func (s *Store) AcceptInvite(inviteID, currentIdentity string) (Envelope, error) {
	currentIdentity, err := canonicalNpub(currentIdentity)
	if err != nil {
		return Envelope{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Envelope{}, err
	}
	defer tx.Rollback()
	invite, group, err := loadInviteGroup(tx, currentIdentity, inviteID)
	if err != nil {
		if errors.Is(err, ErrInviteNotFound) {
			var exists int
			if queryErr := tx.QueryRow(`SELECT COUNT(*) FROM groupchat_invites WHERE invite_id = ?`, inviteID).Scan(&exists); queryErr != nil {
				return Envelope{}, queryErr
			}
			if exists > 0 {
				return Envelope{}, ErrIdentityMismatch
			}
		}
		return Envelope{}, err
	}
	if invite.invitee != currentIdentity {
		return Envelope{}, ErrIdentityMismatch
	}
	if group.State == StateLeft || group.State == StateCancelled || group.State == StateActive {
		return Envelope{}, ErrInvalidTransition
	}
	switch invite.state {
	case InvitePending:
		if _, err := tx.Exec(`UPDATE groupchat_invites SET state = ?, updated_at = ? WHERE local_npub = ? AND invite_id = ?`,
			InviteAccepted, time.Now().Unix(), currentIdentity, inviteID); err != nil {
			return Envelope{}, err
		}
		if _, err := tx.Exec(`UPDATE groupchat_members SET state = ? WHERE local_npub = ? AND group_id = ? AND npub = ?`,
			InviteAccepted, currentIdentity, group.ID, currentIdentity); err != nil {
			return Envelope{}, err
		}
	case InviteAccepted:
	default:
		return Envelope{}, ErrInvalidTransition
	}
	if err := tx.Commit(); err != nil {
		return Envelope{}, err
	}
	return Envelope{Type: EnvelopeAccept, Version: Version, GroupID: group.ID,
		CreatorNpub: group.Creator, InviteID: inviteID, RosterHash: group.RosterHash,
		InviteeNpub: currentIdentity}, nil
}

// DeclineInvite explicitly rejects an invitation. The local group becomes
// cancelled and the returned signed-response payload is addressed to its
// creator; fixed-roster activation can never silently omit this member.
func (s *Store) DeclineInvite(inviteID, currentIdentity string) (Envelope, error) {
	currentIdentity, err := canonicalNpub(currentIdentity)
	if err != nil {
		return Envelope{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Envelope{}, err
	}
	defer tx.Rollback()
	invite, group, err := loadInviteGroup(tx, currentIdentity, inviteID)
	if err != nil {
		return Envelope{}, err
	}
	if invite.invitee != currentIdentity {
		return Envelope{}, ErrIdentityMismatch
	}
	if invite.state == InviteDeclined && group.State == StateCancelled {
		if err := tx.Commit(); err != nil {
			return Envelope{}, err
		}
		return Envelope{Type: EnvelopeDecline, Version: Version, GroupID: group.ID,
			CreatorNpub: group.Creator, InviteID: inviteID, RosterHash: group.RosterHash,
			InviteeNpub: currentIdentity}, nil
	}
	if invite.state != InvitePending || group.State != StatePending {
		return Envelope{}, ErrInvalidTransition
	}
	now := time.Now().Unix()
	if _, err := tx.Exec(`UPDATE groupchat_invites SET state = ?, updated_at = ? WHERE local_npub = ? AND invite_id = ?`,
		InviteDeclined, now, currentIdentity, inviteID); err != nil {
		return Envelope{}, err
	}
	if _, err := tx.Exec(`UPDATE groupchat_groups SET state = ?, updated_at = ? WHERE local_npub = ? AND group_id = ?`,
		StateCancelled, now, currentIdentity, group.ID); err != nil {
		return Envelope{}, err
	}
	if err := tx.Commit(); err != nil {
		return Envelope{}, err
	}
	return Envelope{Type: EnvelopeDecline, Version: Version, GroupID: group.ID,
		CreatorNpub: group.Creator, InviteID: inviteID, RosterHash: group.RosterHash,
		InviteeNpub: currentIdentity}, nil
}

// ReceiveAcceptance records a creator-verified acceptance. The final
// acceptance moves the creator's group to activating and returns one creator
// activation payload per invitee; callers must durably queue each signed event
// before calling MarkActivationQueued.
func (s *Store) ReceiveAcceptance(v VerifiedIncoming) ([]Envelope, error) {
	e, err := envelopeFrom(v, EnvelopeAccept)
	if err != nil {
		return nil, err
	}
	if v.senderNpub != e.InviteeNpub || v.recipientNpub != e.CreatorNpub {
		return nil, ErrProtocolMismatch
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	invite, group, err := loadInviteGroup(tx, v.recipientNpub, e.InviteID)
	if err != nil {
		return nil, err
	}
	if group.ID != e.GroupID || group.Creator != e.CreatorNpub || invite.invitee != e.InviteeNpub || invite.hash != e.RosterHash {
		return nil, ErrProtocolMismatch
	}
	if group.State == StateCancelled || group.State == StateLeft {
		return nil, ErrInvalidTransition
	}
	if invite.state == InvitePending {
		if _, err := tx.Exec(`UPDATE groupchat_invites SET state = ?, accept_event_id = ?, updated_at = ?
			WHERE local_npub = ? AND invite_id = ?`, InviteAccepted, v.eventID, time.Now().Unix(), v.recipientNpub, e.InviteID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE groupchat_members SET state = ? WHERE local_npub = ? AND group_id = ? AND npub = ?`,
			InviteAccepted, v.recipientNpub, e.GroupID, e.InviteeNpub); err != nil {
			return nil, err
		}
	} else if invite.state != InviteAccepted && invite.state != InviteActive {
		return nil, ErrInvalidTransition
	}

	if group.State == StatePending {
		var pending int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM groupchat_invites WHERE local_npub = ? AND group_id = ? AND state != ?`,
			v.recipientNpub, e.GroupID, InviteAccepted).Scan(&pending); err != nil {
			return nil, err
		}
		if pending == 0 {
			if _, err := tx.Exec(`UPDATE groupchat_groups SET state = ?, updated_at = ? WHERE local_npub = ? AND group_id = ?`,
				StateActivating, time.Now().Unix(), v.recipientNpub, e.GroupID); err != nil {
				return nil, err
			}
			group.State = StateActivating
		}
	}
	activations := []Envelope(nil)
	if group.State == StateActivating || group.State == StateActive {
		activations, err = activationEnvelopes(tx, v.recipientNpub, group)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return activations, nil
}

// MarkActivationQueued records that the original signed activation event is
// durably published or queued for retry. Relay ACK is not recipient receipt.
func (s *Store) MarkActivationQueued(localNpub, groupID, inviteID, eventID string, queued bool) error {
	if !queued || len(eventID) != 64 || !isLowerHex(eventID) {
		return errors.New("activation must be durably queued before marking it")
	}
	localNpub, err := canonicalNpub(localNpub)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var groupState string
	if err := tx.QueryRow(`SELECT state FROM groupchat_groups WHERE local_npub = ? AND group_id = ?`, localNpub, groupID).Scan(&groupState); err != nil {
		if err == sql.ErrNoRows {
			return ErrGroupNotFound
		}
		return err
	}
	if groupState != string(StateActivating) && groupState != string(StateActive) {
		return ErrInvalidTransition
	}
	var inviteState, recipient, oldActivationEventID string
	err = tx.QueryRow(`SELECT state, invitee_npub FROM groupchat_invites WHERE local_npub = ? AND group_id = ? AND invite_id = ?`,
		localNpub, groupID, inviteID).Scan(&inviteState, &recipient)
	if err != nil {
		if err == sql.ErrNoRows {
			return ErrInviteNotFound
		}
		return err
	}
	if inviteState != string(InviteAccepted) && inviteState != string(InviteActive) {
		return ErrInvalidTransition
	}
	if inviteState == string(InviteActive) {
		if err := tx.QueryRow(`SELECT activation_event_id FROM groupchat_invites WHERE local_npub = ? AND invite_id = ?`,
			localNpub, inviteID).Scan(&oldActivationEventID); err != nil {
			return err
		}
		if oldActivationEventID != eventID {
			return ErrProtocolMismatch
		}
	}
	if _, err := tx.Exec(`UPDATE groupchat_invites SET state = ?, activation_event_id = ?, updated_at = ?
		WHERE local_npub = ? AND invite_id = ?`, InviteActive, eventID, time.Now().Unix(), localNpub, inviteID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE groupchat_members SET state = ? WHERE local_npub = ? AND group_id = ? AND npub = ?`,
		InviteActive, localNpub, groupID, recipient); err != nil {
		return err
	}
	var waiting int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM groupchat_invites WHERE local_npub = ? AND group_id = ? AND state != ?`,
		localNpub, groupID, InviteActive).Scan(&waiting); err != nil {
		return err
	}
	if waiting == 0 {
		if _, err := tx.Exec(`UPDATE groupchat_groups SET state = ?, updated_at = ? WHERE local_npub = ? AND group_id = ?`,
			StateActive, time.Now().Unix(), localNpub, groupID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE groupchat_members SET state = ? WHERE local_npub = ? AND group_id = ?`,
			InviteActive, localNpub, groupID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReceiveActivation activates only the invitation and immutable roster the
// local identity previously accepted. Replays are idempotent and cannot revive
// a locally left or cancelled group.
func (s *Store) ReceiveActivation(v VerifiedIncoming) (bool, error) {
	e, err := envelopeFrom(v, EnvelopeActivate)
	if err != nil {
		return false, err
	}
	if v.senderNpub != e.CreatorNpub || v.recipientNpub != e.InviteeNpub {
		return false, ErrProtocolMismatch
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	invite, group, err := loadInviteGroup(tx, v.recipientNpub, e.InviteID)
	if err != nil {
		return false, err
	}
	if group.ID != e.GroupID || group.Name != e.Name || group.Creator != e.CreatorNpub ||
		invite.invitee != e.InviteeNpub || invite.hash != e.RosterHash || !sameStrings(group.Roster, e.Members) {
		return false, ErrProtocolMismatch
	}
	if group.State == StateLeft || group.State == StateCancelled {
		return false, ErrInvalidTransition
	}
	if invite.state != InviteAccepted && invite.state != InviteActive {
		return false, ErrInvalidTransition
	}
	if invite.state == InviteActive {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if _, err := tx.Exec(`UPDATE groupchat_invites SET state = ?, activation_event_id = ?, updated_at = ?
		WHERE local_npub = ? AND invite_id = ?`, InviteActive, v.eventID, time.Now().Unix(), v.recipientNpub, e.InviteID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE groupchat_groups SET state = ?, updated_at = ? WHERE local_npub = ? AND group_id = ?`,
		StateActive, time.Now().Unix(), v.recipientNpub, e.GroupID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE groupchat_members SET state = ? WHERE local_npub = ? AND group_id = ?`,
		InviteActive, v.recipientNpub, e.GroupID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ReceiveDecline cancels the pending fixed-roster activation. It never
// silently activates a subset.
func (s *Store) ReceiveDecline(v VerifiedIncoming) ([]Envelope, error) {
	e, err := envelopeFrom(v, EnvelopeDecline)
	if err != nil {
		return nil, err
	}
	if v.senderNpub != e.InviteeNpub || v.recipientNpub != e.CreatorNpub {
		return nil, ErrProtocolMismatch
	}
	return s.cancelFromInvite(v, e, true)
}

// CancelGroup cancels only a not-yet-active group and returns one cancellation
// envelope per invitee for the caller to send through the durable event sender.
func (s *Store) CancelGroup(localNpub, groupID string) ([]Envelope, error) {
	localNpub, err := canonicalNpub(localNpub)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	group, err := loadGroup(tx, localNpub, groupID)
	if err != nil {
		return nil, err
	}
	if group.Creator != localNpub || (group.State != StatePending && group.State != StateActivating) {
		return nil, ErrInvalidTransition
	}
	rows, err := tx.Query(`SELECT invite_id, invitee_npub FROM groupchat_invites WHERE local_npub = ? AND group_id = ? ORDER BY invitee_npub`, localNpub, groupID)
	if err != nil {
		return nil, err
	}
	var result []Envelope
	for rows.Next() {
		var inviteID, invitee string
		if err := rows.Scan(&inviteID, &invitee); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, Envelope{Type: EnvelopeCancel, Version: Version, GroupID: groupID,
			CreatorNpub: group.Creator, InviteID: inviteID, RosterHash: group.RosterHash, InviteeNpub: invitee})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if _, err := tx.Exec(`UPDATE groupchat_groups SET state = ?, updated_at = ? WHERE local_npub = ? AND group_id = ?`,
		StateCancelled, time.Now().Unix(), localNpub, groupID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE groupchat_invites SET state = ?, updated_at = ? WHERE local_npub = ? AND group_id = ?`,
		InviteCancelled, time.Now().Unix(), localNpub, groupID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// ReceiveCancel applies a creator-signed cancellation to a pending invitation.
func (s *Store) ReceiveCancel(v VerifiedIncoming) error {
	e, err := envelopeFrom(v, EnvelopeCancel)
	if err != nil {
		return err
	}
	if v.senderNpub != e.CreatorNpub || v.recipientNpub != e.InviteeNpub {
		return ErrProtocolMismatch
	}
	_, err = s.cancelFromInvite(v, e, false)
	return err
}

func (s *Store) cancelFromInvite(v VerifiedIncoming, e Envelope, declined bool) ([]Envelope, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	invite, group, err := loadInviteGroup(tx, v.recipientNpub, e.InviteID)
	if err != nil {
		return nil, err
	}
	if group.ID != e.GroupID || group.Creator != e.CreatorNpub || invite.invitee != e.InviteeNpub || invite.hash != e.RosterHash {
		return nil, ErrProtocolMismatch
	}
	if group.State == StateActive || group.State == StateLeft {
		return nil, ErrInvalidTransition
	}
	if group.State == StateCancelled {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		if declined && group.Creator == v.recipientNpub {
			return cancellationEnvelopes(s.db, v.recipientNpub, group, e.InviteeNpub)
		}
		return nil, nil
	}
	notices, err := cancellationEnvelopes(tx, v.recipientNpub, group, e.InviteeNpub)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE groupchat_groups SET state = ?, updated_at = ? WHERE local_npub = ? AND group_id = ?`,
		StateCancelled, time.Now().Unix(), v.recipientNpub, e.GroupID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE groupchat_invites SET state = ?, updated_at = ? WHERE local_npub = ? AND group_id = ?`,
		InviteCancelled, time.Now().Unix(), v.recipientNpub, e.GroupID); err != nil {
		return nil, err
	}
	if declined {
		if _, err := tx.Exec(`UPDATE groupchat_invites SET state = ? WHERE local_npub = ? AND invite_id = ?`,
			InviteDeclined, v.recipientNpub, e.InviteID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return notices, nil
}

func cancellationEnvelopes(queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}, localNpub string, group Group, excludeNpub string) ([]Envelope, error) {
	rows, err := queryer.Query(`SELECT invite_id, invitee_npub FROM groupchat_invites
		WHERE local_npub = ? AND group_id = ? ORDER BY invitee_npub`, localNpub, group.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Envelope
	for rows.Next() {
		var inviteID, invitee string
		if err := rows.Scan(&inviteID, &invitee); err != nil {
			return nil, err
		}
		if invitee == excludeNpub {
			continue
		}
		result = append(result, Envelope{Type: EnvelopeCancel, Version: Version, GroupID: group.ID,
			CreatorNpub: group.Creator, InviteID: inviteID, RosterHash: group.RosterHash, InviteeNpub: invitee})
	}
	return result, rows.Err()
}

// LeaveGroup is a local-only state transition. It retains local history but
// prevents future send, receive, and display through this store.
func (s *Store) LeaveGroup(localNpub, groupID string) error {
	localNpub, err := canonicalNpub(localNpub)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`UPDATE groupchat_groups SET state = ?, updated_at = ?
		WHERE local_npub = ? AND group_id = ? AND state = ?`, StateLeft, time.Now().Unix(), localNpub, groupID, StateActive)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		var state string
		if err := s.db.QueryRow(`SELECT state FROM groupchat_groups WHERE local_npub = ? AND group_id = ?`, localNpub, groupID).Scan(&state); err != nil {
			if err == sql.ErrNoRows {
				return ErrGroupNotFound
			}
			return err
		}
		if state == string(StateLeft) {
			return nil
		}
		return ErrInvalidTransition
	}
	return nil
}

func (s *Store) GetGroup(localNpub, groupID string) (Group, error) {
	localNpub, err := canonicalNpub(localNpub)
	if err != nil {
		return Group{}, err
	}
	return loadGroup(s.db, localNpub, groupID)
}

func (s *Store) ListGroups(localNpub string) ([]Group, error) {
	localNpub, err := canonicalNpub(localNpub)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT group_id, name, creator_npub, roster_json, roster_hash, state, created_at, updated_at
		FROM groupchat_groups WHERE local_npub = ? ORDER BY updated_at DESC, group_id`, localNpub)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []Group
	for rows.Next() {
		var group Group
		var membersJSON, state string
		group.LocalNpub = localNpub
		if err := rows.Scan(&group.ID, &group.Name, &group.Creator, &membersJSON, &group.RosterHash, &state, &group.CreatedAt, &group.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(membersJSON), &group.Roster); err != nil {
			return nil, fmt.Errorf("decode stored group roster: %w", err)
		}
		group.State = GroupState(state)
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func insertInvite(tx *sql.Tx, localNpub string, e Envelope, state InviteState, eventID string, now int64) error {
	rosterJSON, err := json.Marshal(e.Members)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO groupchat_invites
		(local_npub, invite_id, group_id, creator_npub, invitee_npub, name, roster_json, roster_hash, state, invite_event_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, localNpub, e.InviteID, e.GroupID, e.CreatorNpub,
		e.InviteeNpub, e.Name, string(rosterJSON), e.RosterHash, state, eventID, now, now); err != nil {
		return fmt.Errorf("store group invitation: %w", err)
	}
	return nil
}

type inviteRow struct {
	id, groupID, creator, invitee, name, rosterJSON, hash, inviteEventID, acceptEventID, activationEventID string
	state                                                                                                  InviteState
}

func loadInviteGroup(tx *sql.Tx, localNpub, inviteID string) (inviteRow, Group, error) {
	var invite inviteRow
	err := tx.QueryRow(`SELECT invite_id, group_id, creator_npub, invitee_npub, name, roster_json, roster_hash,
		state, invite_event_id, accept_event_id, activation_event_id FROM groupchat_invites
		WHERE local_npub = ? AND invite_id = ?`, localNpub, inviteID).Scan(
		&invite.id, &invite.groupID, &invite.creator, &invite.invitee, &invite.name, &invite.rosterJSON, &invite.hash,
		&invite.state, &invite.inviteEventID, &invite.acceptEventID, &invite.activationEventID)
	if err != nil {
		if err == sql.ErrNoRows {
			return inviteRow{}, Group{}, ErrInviteNotFound
		}
		return inviteRow{}, Group{}, err
	}
	group, err := loadGroup(tx, localNpub, invite.groupID)
	return invite, group, err
}

func loadGroup(queryer interface {
	QueryRow(string, ...any) *sql.Row
}, localNpub, groupID string) (Group, error) {
	var group Group
	var rosterJSON, state string
	group.LocalNpub = localNpub
	err := queryer.QueryRow(`SELECT group_id, name, creator_npub, roster_json, roster_hash, state, created_at, updated_at
		FROM groupchat_groups WHERE local_npub = ? AND group_id = ?`, localNpub, groupID).
		Scan(&group.ID, &group.Name, &group.Creator, &rosterJSON, &group.RosterHash, &state, &group.CreatedAt, &group.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return Group{}, ErrGroupNotFound
		}
		return Group{}, err
	}
	if err := json.Unmarshal([]byte(rosterJSON), &group.Roster); err != nil {
		return Group{}, fmt.Errorf("decode stored group roster: %w", err)
	}
	group.State = GroupState(state)
	return group, nil
}

func activationEnvelopes(tx *sql.Tx, localNpub string, group Group) ([]Envelope, error) {
	rows, err := tx.Query(`SELECT invite_id, invitee_npub FROM groupchat_invites
		WHERE local_npub = ? AND group_id = ? ORDER BY invitee_npub`, localNpub, group.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var envelopes []Envelope
	for rows.Next() {
		var inviteID, invitee string
		if err := rows.Scan(&inviteID, &invitee); err != nil {
			return nil, err
		}
		envelopes = append(envelopes, Envelope{Type: EnvelopeActivate, Version: Version,
			GroupID: group.ID, CreatorNpub: group.Creator, InviteID: inviteID, RosterHash: group.RosterHash,
			InviteeNpub: invitee, Name: group.Name, Members: append([]string(nil), group.Roster...)})
	}
	return envelopes, rows.Err()
}

func envelopeFrom(v VerifiedIncoming, expected EnvelopeType) (Envelope, error) {
	if !v.valid() || v.kind != wireevent.Kind30078 || !v.isEncrypted {
		return Envelope{}, errors.New("verified encrypted Agent event is required")
	}
	e, err := Decode(v.plaintext)
	if err != nil {
		return Envelope{}, err
	}
	if e.Type != expected {
		return Envelope{}, fmt.Errorf("unexpected group envelope type %q", e.Type)
	}
	return e, nil
}

// valid is the state layer's fail-closed gate for opaque incoming values. The
// protocol decoder validates the envelope, while the constructor establishes
// authenticity; state transitions additionally require a non-zero value with
// canonical signed identities and a well-formed event ID before consulting DB.
func (v VerifiedIncoming) valid() bool {
	if !v.verified || v.kind != wireevent.Kind30078 || !v.isEncrypted ||
		len(v.eventID) != 64 || !isLowerHex(v.eventID) || !validProtocolText(v.plaintext) {
		return false
	}
	for _, identity := range []string{v.senderNpub, v.recipientNpub} {
		canonical, err := canonicalNpub(identity)
		if err != nil || canonical != identity {
			return false
		}
	}
	return true
}

func normalizeMemberList(values []string) []string {
	copyValues := append([]string(nil), values...)
	sort.Strings(copyValues)
	return copyValues
}

func canonicalLocalNpub(value string) (string, error) {
	pk, err := common.ParsePublicKey(value)
	if err != nil {
		return "", err
	}
	return common.EncodeNpub(pk), nil
}

func rosterJSON(values []string) (string, error) {
	canonical := normalizeMemberList(values)
	data, err := json.Marshal(canonical)
	return string(data), err
}

func membersFromJSON(encoded string) ([]string, error) {
	var members []string
	if err := json.Unmarshal([]byte(encoded), &members); err != nil {
		return nil, err
	}
	return members, nil
}

func sortGroupMessages(messages []types.GroupMessage) {
	sort.SliceStable(messages, func(i, j int) bool {
		if messages[i].CreatedAt != messages[j].CreatedAt {
			return messages[i].CreatedAt < messages[j].CreatedAt
		}
		return messages[i].ID < messages[j].ID
	})
}
