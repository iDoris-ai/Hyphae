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
    max_retries     INTEGER NOT NULL CHECK (max_retries > 0),
    row_revision    INTEGER NOT NULL DEFAULT 0 CHECK (row_revision >= 0),
    attempt_generation INTEGER NOT NULL DEFAULT 0 CHECK (attempt_generation >= 0),
    attempt_phase   TEXT NOT NULL DEFAULT 'idle' CHECK (attempt_phase IN ('idle','reserved','started','failed','accepted')),
    last_result_generation INTEGER NOT NULL DEFAULT 0 CHECK (last_result_generation >= 0),
    last_failure_receipt TEXT NOT NULL DEFAULT '',
    retry_count     INTEGER CHECK (retry_count IS NULL OR retry_count >= 0),
    retry_queue_id  TEXT NOT NULL DEFAULT '',
    last_recovery_id TEXT NOT NULL DEFAULT '',
    last_recovery_digest TEXT NOT NULL DEFAULT '',
    accounting_origin TEXT NOT NULL DEFAULT 'native' CHECK (accounting_origin IN ('native','legacy')),
    legacy_attempts_base INTEGER NOT NULL DEFAULT 0 CHECK (legacy_attempts_base >= 0),
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
	RecipientNpub      string                              `json:"recipient_npub"`
	EventID            string                              `json:"event_id"`
	QueueID            string                              `json:"queue_id"`
	State              RecipientDeliveryState              `json:"state"`
	Issue              messaging.AgentMessageDeliveryIssue `json:"issue,omitempty"`
	RelayAcks          int                                 `json:"relay_acks"`  // 0 or 1: at least one relay accepted.
	RelayCount         int                                 `json:"relay_count"` // Number of configured targets.
	Attempts           int                                 `json:"attempts"`
	RetryCount         *int                                `json:"retry_count"`
	RetryQueueID       string                              `json:"retry_queue_id"`
	AttemptPhase       string                              `json:"attempt_phase"`
	AccountingOrigin   string                              `json:"accounting_origin"`
	LegacyAttemptsBase int                                 `json:"legacy_attempts_base"`
	MaxRetries         int                                 `json:"max_retries"`
	LastAttemptAt      int64                               `json:"last_attempt_at"`
	AcceptedAt         int64                               `json:"accepted_at"`
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

type fanoutAttemptToken struct {
	key                         FanoutKey
	recipient, eventID, queueID string
	generation                  int64
}

// reserveFanoutAttemptTx consumes a generation without counting a publish start.
func reserveFanoutAttemptTx(tx queryExecer, key FanoutKey, recipient string) (fanoutAttemptToken, error) {
	local, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return fanoutAttemptToken{}, err
	}
	var token fanoutAttemptToken
	token.key, token.recipient = key, recipient
	var revision int64
	var phase string
	err = tx.QueryRow(`SELECT event_id,queue_id,attempt_generation,row_revision,attempt_phase FROM groupchat_fanout
        WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=? AND state=?`,
		local, key.GroupID, key.EnvelopeType, key.SendKey, recipient, RecipientQueued).Scan(&token.eventID, &token.queueID, &token.generation, &revision, &phase)
	if err != nil {
		return fanoutAttemptToken{}, err
	}
	if token.queueID == "" || token.generation < 0 || revision < 0 || token.generation == math.MaxInt64 || revision == math.MaxInt64 {
		return fanoutAttemptToken{}, fmt.Errorf("fanout attempt reservation is invalid or exhausted")
	}
	if phase != "idle" && phase != "failed" && phase != "accepted" {
		return fanoutAttemptToken{}, fmt.Errorf("fanout attempt phase %q cannot be reserved", phase)
	}
	if phase == "accepted" {
		return fanoutAttemptToken{}, fmt.Errorf("accepted fanout row is frozen")
	}
	token.generation++
	result, err := tx.Exec(`UPDATE groupchat_fanout SET attempt_generation=?,attempt_phase='reserved',row_revision=row_revision+1
        WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=? AND state=? AND row_revision=? AND attempt_generation=?`,
		token.generation, local, key.GroupID, key.EnvelopeType, key.SendKey, recipient, RecipientQueued, revision, token.generation-1)
	if err != nil {
		return fanoutAttemptToken{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fanoutAttemptToken{}, err
	}
	if n != 1 {
		return fanoutAttemptToken{}, fmt.Errorf("stale fanout reservation")
	}
	token.key.LocalNpub = local
	return token, nil
}

// startFanoutAttemptTx records S before the caller performs network publish P.
func startFanoutAttemptTx(tx queryExecer, token fanoutAttemptToken, targets int, now int64) error {
	if targets < 0 || now < 0 {
		return fmt.Errorf("fanout start values must be non-negative")
	}
	var attempts, revision int64
	err := tx.QueryRow(`SELECT attempts,row_revision FROM groupchat_fanout WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=? AND event_id=? AND queue_id=? AND attempt_generation=? AND attempt_phase='reserved' AND state=?`,
		token.key.LocalNpub, token.key.GroupID, token.key.EnvelopeType, token.key.SendKey, token.recipient, token.eventID, token.queueID, token.generation, RecipientQueued).Scan(&attempts, &revision)
	if err != nil {
		return err
	}
	if attempts < 0 || attempts == math.MaxInt64 || revision < 0 || revision == math.MaxInt64 {
		return fmt.Errorf("fanout start counter overflow or invalid")
	}
	result, err := tx.Exec(`UPDATE groupchat_fanout SET attempts=attempts+1,attempt_phase='started',last_attempt_at=MAX(last_attempt_at,?),relay_count=?,issue='',row_revision=row_revision+1
        WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=? AND event_id=? AND queue_id=? AND attempt_generation=? AND attempt_phase='reserved' AND state=? AND row_revision=?`,
		now, targets, token.key.LocalNpub, token.key.GroupID, token.key.EnvelopeType, token.key.SendKey, token.recipient, token.eventID, token.queueID, token.generation, RecipientQueued, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("stale fanout start")
	}
	return nil
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
		if v != math.Trunc(v) || v < math.MinInt64 || v >= math.MaxInt64 {
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
// confirmed value. attempts/last_attempt_at/relay_count/max_retries/
// accepted_at/updated_at are monotonic (MAX) on every transition.
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
		if to == RecipientRelayAccepted {
			sets = append(sets, "attempt_phase = 'accepted'")
		}
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
		if from == RecipientRelayAccepted {
			switch column {
			case "relay_acks", "accepted_at", "issue":
				continue
			default:
				return false, fmt.Errorf("accepted fanout row is frozen")
			}
		}
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
		case "relay_count":
			n, ok := fanoutInt(value)
			if !ok || n < 0 {
				return false, fmt.Errorf("fanout relay_count must be non-negative")
			}
			value = n
		case "attempts", "last_attempt_at", "accepted_at":
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
	var revision int64
	if err := tx.QueryRow(`SELECT row_revision FROM groupchat_fanout WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=? AND state=?`, localNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient, from).Scan(&revision); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if revision < 0 || revision == math.MaxInt64 {
		return false, fmt.Errorf("fanout row revision overflow")
	}
	sets = append(sets, "row_revision = row_revision + 1")
	args = append(args, localNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient, from, revision)
	guard := "" // see D2 above: the authoritative, not just in-memory, queue_id check.
	if entering && to == RecipientRelayAccepted {
		guard = " AND queue_id != ''"
	}
	result, err := tx.Exec(`UPDATE groupchat_fanout SET `+strings.Join(sets, ", ")+`
        WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ?
        AND recipient_npub = ? AND state = ? AND row_revision = ?`+guard, args...)
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
		relay_acks, relay_count, attempts, max_retries, last_attempt_at, accepted_at, created_at,
		retry_count, retry_queue_id, attempt_phase, accounting_origin, legacy_attempts_base
        FROM groupchat_fanout WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ?
        ORDER BY recipient_npub`, local, key.GroupID, key.EnvelopeType, key.SendKey)
	if err != nil {
		return FanoutReport{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var recipient RecipientDelivery
		var createdAt int64
		var retryCount sql.NullInt64
		var retryQueueID, phase, origin string
		var legacyBase int64
		if err := rows.Scan(&recipient.RecipientNpub, &recipient.EventID, &recipient.QueueID,
			&recipient.State, &recipient.Issue, &recipient.RelayAcks, &recipient.RelayCount,
			&recipient.Attempts, &recipient.MaxRetries, &recipient.LastAttemptAt, &recipient.AcceptedAt, &createdAt,
			&retryCount, &retryQueueID, &phase, &origin, &legacyBase); err != nil {
			return FanoutReport{}, err
		}
		if retryCount.Valid {
			count := int(retryCount.Int64)
			recipient.RetryCount = &count
		}
		if recipient.State == RecipientRelayAccepted {
			phase = "accepted"
		}
		recipient.RetryQueueID, recipient.AttemptPhase = retryQueueID, phase
		recipient.AccountingOrigin, recipient.LegacyAttemptsBase = origin, int(legacyBase)
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
		} else if report.CreatedAt != createdAt {
			return FanoutReport{}, fmt.Errorf("fanout recipients have inconsistent created_at")
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
