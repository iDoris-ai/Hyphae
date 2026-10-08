package groupchat

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
)

const fanoutSchema = `CREATE TABLE IF NOT EXISTS groupchat_fanout (
    local_npub      TEXT    NOT NULL,             -- 发送身份
    group_id        TEXT    NOT NULL,
    envelope_type   TEXT    NOT NULL,             -- 'message' | 'invite' | 'accept' | 'decline' | 'activate' | 'cancel'
    send_key        TEXT    NOT NULL,             -- message: logical_id；控制 envelope: invite_id
    recipient_npub  TEXT    NOT NULL,
    event_id        TEXT    NOT NULL,             -- 64 位小写 hex，签名后的 event ID，插入后不可变
    event_json      TEXT    NOT NULL,             -- 完整已签名 event（仅密文），插入后不可变
    queue_id        TEXT    NOT NULL DEFAULT '',  -- 已确认的 outbox QueueID；prepared 时为 ''
    state           TEXT    NOT NULL,             -- prepared | queued | relay_accepted | failed
    issue           TEXT    NOT NULL DEFAULT '',
    relay_acks      INTEGER NOT NULL DEFAULT 0,
    relay_count     INTEGER NOT NULL DEFAULT 0,
    attempts        INTEGER NOT NULL DEFAULT 0,
    max_retries     INTEGER NOT NULL,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    last_attempt_at INTEGER NOT NULL DEFAULT 0,
    accepted_at     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (local_npub, group_id, envelope_type, send_key, recipient_npub),
    UNIQUE (local_npub, event_id)
);
CREATE INDEX IF NOT EXISTS idx_groupchat_fanout_pending
    ON groupchat_fanout(local_npub, state) WHERE state IN ('prepared', 'queued');
`

type RecipientDeliveryState string

const (
	RecipientPrepared      RecipientDeliveryState = "prepared"
	RecipientQueued        RecipientDeliveryState = "queued"
	RecipientRelayAccepted RecipientDeliveryState = "relay_accepted"
	RecipientFailed        RecipientDeliveryState = "failed"
)

type RecipientDelivery struct {
	RecipientNpub string                              `json:"recipient_npub"`
	EventID       string                              `json:"event_id"`
	QueueID       string                              `json:"queue_id"`
	State         RecipientDeliveryState              `json:"state"`
	Issue         messaging.AgentMessageDeliveryIssue `json:"issue,omitempty"`
	RelayAcks     int                                 `json:"relay_acks"`  // 0 or 1: at least one relay accepted.
	RelayCount    int                                 `json:"relay_count"` // Number of configured targets.
	Attempts      int                                 `json:"attempts"`
	MaxRetries    int                                 `json:"max_retries"`
	LastAttemptAt int64                               `json:"last_attempt_at"`
	AcceptedAt    int64                               `json:"accepted_at"`
}

type FanoutReport struct {
	GroupID    string                              `json:"group_id"`
	LogicalID  string                              `json:"logical_id"`
	CreatedAt  int64                               `json:"created_at"`
	State      messaging.AgentMessageDeliveryState `json:"state"`
	Accepted   int                                 `json:"accepted"`
	Queued     int                                 `json:"queued"` // Includes prepared recipients.
	Failed     int                                 `json:"failed"`
	Recipients []RecipientDelivery                 `json:"recipients"`
}

// FanoutKey identifies one message or control envelope's durable send intent.
type FanoutKey struct {
	LocalNpub    string
	GroupID      string
	EnvelopeType EnvelopeType
	SendKey      string
}

func fanoutRank(state RecipientDeliveryState) int {
	switch state {
	case RecipientPrepared:
		return 0
	case RecipientQueued, RecipientFailed:
		return 1
	case RecipientRelayAccepted:
		return 2
	default:
		return -1
	}
}

// transitionFanoutTx applies a compare-and-swap inside the caller's write transaction.
// A false result means a stale caller or missing row; reload the current evidence.
// Only mutable bookkeeping fields are accepted, never the frozen event or intent key.
func transitionFanoutTx(tx queryExecer, key FanoutKey, recipient string, from, to RecipientDeliveryState, fields map[string]any) (bool, error) {
	fromRank, toRank := fanoutRank(from), fanoutRank(to)
	if fromRank < 0 || toRank < 0 || toRank < fromRank ||
		(from == RecipientPrepared && to == RecipientRelayAccepted) {
		return false, ErrInvalidTransition
	}
	sets := []string{"updated_at = MAX(updated_at, ?)"}
	args := []any{time.Now().Unix()}
	if from != to {
		sets = append(sets, "state = ?")
		args = append(args, to)
	}
	columns := make([]string, 0, len(fields))
	for column := range fields {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	for _, column := range columns {
		value := fields[column]
		expression := column + " = ?"
		switch column {
		case "queue_id":
			if _, ok := value.(string); !ok {
				return false, fmt.Errorf("invalid fanout queue_id")
			}
			expression = "queue_id = COALESCE(NULLIF(?, ''), queue_id)"
		case "relay_acks":
			acks, ok := value.(int)
			if !ok || acks < 0 || acks > 1 {
				return false, fmt.Errorf("fanout relay_acks must be 0 or 1")
			}
			expression = "relay_acks = MAX(relay_acks, ?)"
		case "attempts", "last_attempt_at", "updated_at", "accepted_at", "relay_count", "max_retries":
			if from == to || column == "accepted_at" {
				expression = column + " = MAX(" + column + ", ?)"
			}
		case "issue":
		default:
			return false, fmt.Errorf("immutable or unknown fanout field %q", column)
		}
		if column == "updated_at" {
			sets[0] = expression
			args[0] = value
			continue
		}
		sets = append(sets, expression)
		args = append(args, value)
	}
	args = append(args, key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient, from)
	result, err := tx.Exec(`UPDATE groupchat_fanout SET `+strings.Join(sets, ", ")+`
        WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ?
        AND recipient_npub = ? AND state = ?`, args...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

// LoadFanoutReport reads only SQLite evidence and returns recipients in npub order.
// A missing intent returns sql.ErrNoRows rather than a successful empty report.
func (s *Store) LoadFanoutReport(key FanoutKey) (FanoutReport, error) {
	local, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return FanoutReport{}, err
	}
	report := FanoutReport{GroupID: key.GroupID, LogicalID: key.SendKey, Recipients: []RecipientDelivery{}}
	rows, err := s.db.Query(`SELECT recipient_npub, event_id, queue_id, state, issue,
        relay_acks, relay_count, attempts, max_retries, last_attempt_at, accepted_at, created_at
        FROM groupchat_fanout WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ?
        ORDER BY recipient_npub`, local, key.GroupID, key.EnvelopeType, key.SendKey)
	if err != nil {
		return FanoutReport{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var recipient RecipientDelivery
		var createdAt int64
		if err := rows.Scan(&recipient.RecipientNpub, &recipient.EventID, &recipient.QueueID,
			&recipient.State, &recipient.Issue, &recipient.RelayAcks, &recipient.RelayCount,
			&recipient.Attempts, &recipient.MaxRetries, &recipient.LastAttemptAt, &recipient.AcceptedAt, &createdAt); err != nil {
			return FanoutReport{}, err
		}
		switch recipient.State {
		case RecipientPrepared, RecipientQueued:
			report.Queued++
		case RecipientRelayAccepted:
			report.Accepted++
		case RecipientFailed:
			report.Failed++
		default:
			return FanoutReport{}, ErrInvalidTransition
		}
		if len(report.Recipients) == 0 {
			report.CreatedAt = createdAt
		}
		report.Recipients = append(report.Recipients, recipient)
	}
	if err := rows.Err(); err != nil {
		return FanoutReport{}, err
	}
	if len(report.Recipients) == 0 {
		return FanoutReport{}, sql.ErrNoRows
	}
	report.State = deriveFanoutState(report)
	return report, nil
}

func deriveFanoutState(report FanoutReport) messaging.AgentMessageDeliveryState {
	switch {
	case report.Failed > 0:
		return messaging.AgentMessageFailed
	case report.Queued > 0:
		return messaging.AgentMessageQueued
	default:
		return messaging.AgentMessageRelayAccepted
	}
}
