package groupchat

import (
	"database/sql"
	"fmt"
	"math"
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

// fanoutInt accepts int/int64 (in-process callers) or a whole-number float64
// (JSON-decoded). A string would be silently accepted by SQLite's dynamic
// typing and later break LoadFanoutReport's Scan, so it is rejected here.
func fanoutInt(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		if v != math.Trunc(v) {
			return 0, false
		}
		return int64(v), true
	default:
		return 0, false
	}
}

// transitionFanoutTx applies a compare-and-swap inside the caller's write transaction.
// A false result means a stale caller or missing row; reload the current evidence.
// Only mutable bookkeeping fields are accepted, never the frozen event or intent key.
//
// D2: entering relay_accepted requires relay_acks = 1, a nonzero accepted_at,
// and (via the SQL CAS, not just the precheck) an already-persisted nonempty
// queue_id -- a row that skipped queued (prepared -> failed -> here is rank-
// legal) must not become markable accepted. D14: queue_id may only be
// supplied when the target state is queued (never same-state prepared, which
// would otherwise forge one), is required to be nonempty when entering
// queued, and a same-state queued call may never replace an already-
// confirmed value. max_retries is frozen at intent creation.
// attempts/last_attempt_at/relay_count/accepted_at/updated_at are
// monotonic (MAX) on every transition.
func transitionFanoutTx(tx queryExecer, key FanoutKey, recipient string, from, to RecipientDeliveryState, fields map[string]any) (bool, error) {
	fromRank, toRank := fanoutRank(from), fanoutRank(to)
	if fromRank < 0 || toRank < 0 || toRank < fromRank ||
		(from == RecipientPrepared && to == RecipientRelayAccepted) {
		return false, ErrInvalidTransition
	}
	localNpub, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return false, err
	}
	entering := from != to
	if entering && to == RecipientQueued {
		if queueID, ok := fields["queue_id"].(string); !ok || queueID == "" {
			return false, fmt.Errorf("fanout queue_id is required when transitioning into queued")
		}
	}
	if entering && to == RecipientRelayAccepted {
		if acks, ok := fanoutInt(fields["relay_acks"]); !ok || acks != 1 {
			return false, fmt.Errorf("fanout relay acceptance requires relay_acks = 1")
		}
		if acceptedAt, ok := fanoutInt(fields["accepted_at"]); !ok || acceptedAt <= 0 {
			return false, fmt.Errorf("fanout relay acceptance requires a nonzero accepted_at")
		}
	}
	sets := []string{"updated_at = MAX(updated_at, ?)"}
	args := []any{time.Now().Unix()}
	if entering {
		sets = append(sets, "state = ?")
		args = append(args, to)
	}
	// Entering relay_accepted, or retrying out of failed, must never leave a
	// stale failure issue behind; the loop below ignores any caller value.
	clearIssue := entering && (to == RecipientRelayAccepted || (from == RecipientFailed && to == RecipientQueued))
	if clearIssue {
		sets = append(sets, "issue = ?")
		args = append(args, string(messaging.AgentMessageIssueNone))
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
			v, ok := value.(string)
			if !ok {
				return false, fmt.Errorf("invalid fanout queue_id")
			}
			if to != RecipientQueued {
				return false, fmt.Errorf("fanout queue_id may only be set when the target state is queued")
			}
			if entering {
				expression = "queue_id = COALESCE(NULLIF(?, ''), queue_id)"
			} else {
				expression = "queue_id = CASE WHEN queue_id = '' THEN ? ELSE queue_id END"
			}
			value = v
		case "relay_acks":
			acks, ok := fanoutInt(value)
			if !ok || acks < 0 || acks > 1 {
				return false, fmt.Errorf("fanout relay_acks must be 0 or 1")
			}
			value = acks
			expression = "relay_acks = MAX(relay_acks, ?)"
		case "attempts", "last_attempt_at", "accepted_at", "relay_count":
			n, ok := fanoutInt(value)
			if !ok || n < 0 {
				return false, fmt.Errorf("fanout %s must be a non-negative integer", column)
			}
			value = n
			expression = column + " = MAX(" + column + ", ?)"
		case "issue":
			var issue messaging.AgentMessageDeliveryIssue
			switch v := value.(type) {
			case messaging.AgentMessageDeliveryIssue:
				issue = v
			case string:
				issue = messaging.AgentMessageDeliveryIssue(v)
			default:
				return false, fmt.Errorf("invalid fanout issue")
			}
			if to == RecipientRelayAccepted || clearIssue {
				continue // already force-cleared above (or frozen); never let it be re-set.
			}
			value = string(issue)
		default:
			return false, fmt.Errorf("immutable or unknown fanout field %q", column)
		}
		sets = append(sets, expression)
		args = append(args, value)
	}
	args = append(args, localNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient, from)
	guard := "" // see D2 above: the authoritative, not just in-memory, queue_id check.
	if entering && to == RecipientRelayAccepted {
		guard = " AND queue_id != ''"
	}
	result, err := tx.Exec(`UPDATE groupchat_fanout SET `+strings.Join(sets, ", ")+`
        WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ?
        AND recipient_npub = ? AND state = ?`+guard, args...)
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
		} else if createdAt != report.CreatedAt {
			return FanoutReport{}, fmt.Errorf("fanout intent has inconsistent created_at: recipient %q has %d, expected %d", recipient.RecipientNpub, createdAt, report.CreatedAt)
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
