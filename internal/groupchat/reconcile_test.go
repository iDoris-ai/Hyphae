package groupchat

import (
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
		QueueID: "queue-1",
		Entry:   types.OutboxEntry{EventJSON: eventJSON, MaxRetries: 4},
	}
	return reconcileEvidence{row: row, entry: entry, relayAcks: 1, acceptedAt: 10}
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
