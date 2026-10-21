package groupchat

import (
	"database/sql"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

func TestDecideReconcile(t *testing.T) {
	outbox := filepath.Join(t.TempDir(), "outbox.json")
	sqlite := filepath.Join(t.TempDir(), "messages.db")
	writeSentinel(t, outbox, []byte("outbox-sentinel"))
	writeSentinel(t, sqlite, []byte("sqlite-sentinel"))
	beforeOutbox := readSentinel(t, outbox)
	beforeSQLite := readSentinel(t, sqlite)

	t.Run("accepted exact evidence", func(t *testing.T) {
		ev := acceptedEvidence()
		if got := decideReconcile(ev); got != ReconcileAccepted {
			t.Fatalf("decision %s", got)
		}
		ev.relayAcks = 0
		if got := decideReconcile(ev); got != ReconcileHold {
			t.Fatalf("inexact acks %s", got)
		}
	})

	t.Run("missing witness on a failed row", func(t *testing.T) {
		ev := acceptedEvidence()
		ev.row.state = string(RecipientFailed)
		ev.row.token.queueID = "old-queue"
		ev.entry.QueueID = "new-queue"
		ev.entry.RecoveryWitness = nil
		if got := decideReconcile(ev); got != ReconcileHold {
			t.Fatalf("decision %s", got)
		}
	})

	t.Run("queue conflict", func(t *testing.T) {
		ev := acceptedEvidence()
		ev.row.state = string(RecipientQueued)
		ev.entry.QueueID = "other-queue"
		if got := decideReconcile(ev); got != ReconcileHold {
			t.Fatalf("decision %s", got)
		}
	})

	t.Run("orphan with no row", func(t *testing.T) {
		ev := acceptedEvidence()
		ev.row = nil
		if got := decideReconcile(ev); got != ReconcileOrphan {
			t.Fatalf("decision %s", got)
		}
	})

	t.Run("empty snapshot", func(t *testing.T) {
		if got := decideReconcile(reconcileEvidence{}); got != ReconcileHold {
			t.Fatalf("decision %s", got)
		}
	})

	t.Run("blank route", func(t *testing.T) {
		ev := acceptedEvidence()
		ev.entry.Route = ""
		if got := classifyFanoutBookkeeping(ev.row, ev.entry, ev.receipt, ev.relayAcks, ev.acceptedAt); got != BookkeepingCleanupPending {
			t.Fatalf("class %s", got)
		}
		if got := decideReconcile(ev); got != ReconcileHold {
			t.Fatalf("decision %s", got)
		}
	})

	t.Run("hold classes", func(t *testing.T) {
		cases := []struct {
			name  string
			class FanoutBookkeeping
			ev    reconcileEvidence
		}{
			{"queued same queue", BookkeepingNormal, queuedSameQueueEvidence()},
			{"accepted cleaned", BookkeepingNormal, acceptedCleanEvidence()},
			{"prepared absent", BookkeepingNormal, preparedAbsentEvidence()},
			{"pending failure", BookkeepingPendingFailure, pendingFailureEvidence()},
			{"pending recovery", BookkeepingPendingRecovery, pendingRecoveryEvidence()},
			{"queue missing", BookkeepingQueueMissing, queueMissingEvidence()},
			{"unconsumed receipt", BookkeepingConflict, unconsumedReceiptEvidence()},
			{"unconsumed witness", BookkeepingConflict, unconsumedWitnessEvidence()},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if c.ev.row == nil {
					t.Fatal("row")
				}
				if got := classifyFanoutBookkeeping(c.ev.row, c.ev.entry, c.ev.receipt, c.ev.relayAcks, c.ev.acceptedAt); got != c.class {
					t.Fatalf("class %s", got)
				}
				if got := decideReconcile(c.ev); got != ReconcileHold {
					t.Fatalf("decision %s", got)
				}
			})
		}
	})

	t.Run("same inputs", func(t *testing.T) {
		ev := acceptedEvidence()
		row := *ev.row
		first := decideReconcile(ev)
		second := decideReconcile(ev)
		if first != second || first != ReconcileAccepted {
			t.Fatalf("first %s second %s", first, second)
		}
		if *ev.row != row {
			t.Fatal("row bytes changed")
		}
	})

	t.Run("no relay import", func(t *testing.T) {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("caller")
		}
		src := filepath.Join(filepath.Dir(file), "reconcile.go")
		parsed, err := parser.ParseFile(token.NewFileSet(), src, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(path, "relay") || strings.Contains(path, "websocket") || strings.Contains(path, "/nostr") {
				t.Fatalf("import %s", path)
			}
		}
	})

	if got := readSentinel(t, outbox); string(got) != string(beforeOutbox) {
		t.Fatalf("outbox changed %q", got)
	}
	if got := readSentinel(t, sqlite); string(got) != string(beforeSQLite) {
		t.Fatalf("sqlite changed %q", got)
	}
}

func acceptedEvidence() reconcileEvidence {
	const eventJSON = `{"id":"e1"}`
	row := &fanoutFailureRow{
		token:      fanoutAttemptToken{queueID: "queue-1"},
		state:      string(RecipientRelayAccepted),
		eventJSON:  eventJSON,
		maxRetries: 4,
	}
	entry := &messaging.RawGroupEntry{
		Route:   messaging.OutboxRouteGroup,
		QueueID: "queue-1",
		Entry:   types.OutboxEntry{EventJSON: eventJSON, MaxRetries: 4, Route: messaging.OutboxRouteGroup},
	}
	return reconcileEvidence{row: row, entry: entry, relayAcks: 1, acceptedAt: 10}
}

func queuedSameQueueEvidence() reconcileEvidence {
	ev := acceptedEvidence()
	ev.row.state = string(RecipientQueued)
	ev.entry.Entry.Status = messaging.OutboxStatusGroupPending
	return ev
}

func acceptedCleanEvidence() reconcileEvidence {
	ev := acceptedEvidence()
	ev.entry = nil
	return ev
}

func preparedAbsentEvidence() reconcileEvidence {
	ev := acceptedEvidence()
	ev.row.state = string(RecipientPrepared)
	ev.entry = nil
	return ev
}

func queueMissingEvidence() reconcileEvidence {
	ev := acceptedEvidence()
	ev.row.state = string(RecipientQueued)
	ev.entry = nil
	return ev
}

func unconsumedReceiptEvidence() reconcileEvidence {
	ev := acceptedEvidence()
	ev.receipt = "o3-open"
	return ev
}

func unconsumedWitnessEvidence() reconcileEvidence {
	ev := acceptedEvidence()
	ev.entry.RecoveryWitness = &messaging.RawRecoveryWitness{}
	ev.entry.Fields = map[string]json.RawMessage{
		"recovery_witness": json.RawMessage(`{"unconsumed":true}`),
	}
	return ev
}

func pendingFailureEvidence() reconcileEvidence {
	ev := acceptedEvidence()
	ev.row.state = string(RecipientQueued)
	ev.row.phase = "started"
	ev.row.token.generation = 2
	ev.row.lastResultGeneration = 1
	ev.row.retryCount = sql.NullInt64{Valid: true}
	ev.row.receipt = "consumed"
	ev.receipt = "o3-open"
	want := ev.row.failureResult()
	ev.entry.Entry.RetryCount = int(want.RetryCount)
	ev.entry.Entry.Status = want.Status
	ev.entry.Entry.LastAttempt = want.LastAttemptAt
	ev.entry.FailureResult = &want
	return ev
}

func pendingRecoveryEvidence() reconcileEvidence {
	ev := acceptedEvidence()
	ev.row.state = string(RecipientFailed)
	ev.row.token.queueID = "old-queue"
	ev.row.token.eventID = "event-1"
	ev.row.token.generation = 3
	ev.row.revision = 4
	ev.entry.QueueID = "new-queue"
	ev.entry.Entry.Status = messaging.OutboxStatusGroupPending
	ev.entry.Entry.CreatedAt = 20
	ev.entry.RecoveryWitness = &messaging.RawRecoveryWitness{
		FanoutKey: messaging.OutboxFanoutKey{
			EnvelopeType: string(ev.row.token.key.EnvelopeType),
		},
		EventID:                   "event-1",
		EventJSONDigest:           messaging.OutboxEventDigest(ev.row.eventJSON),
		MaxRetries:                ev.row.maxRetries,
		ExpectedRowRevision:       ev.row.revision,
		ExpectedAttemptGeneration: ev.row.token.generation,
		OldQueueID:                "old-queue",
		NewQueueID:                "new-queue",
		AuthorizedAt:              20,
	}
	return ev
}

func writeSentinel(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func readSentinel(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
