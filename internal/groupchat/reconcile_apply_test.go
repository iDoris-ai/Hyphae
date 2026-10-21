package groupchat

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

func TestApplyReconcile(t *testing.T) {
	t.Run("accepted projection once", func(t *testing.T) {
		f := cleanupPendingFixture(t)
		before, err := os.ReadFile(f.outboxPath)
		require.NoError(t, err)
		require.NoError(t, f.member.store.applyReconcile(f.key, f.recipient, time.Now().Unix()))
		after, err := os.ReadFile(f.outboxPath)
		require.NoError(t, err)
		require.NotEqual(t, string(before), string(after))
		require.Equal(t, string(RecipientRelayAccepted), f.row(t).State)
		unchanged := f.snapshot(t)
		require.NoError(t, f.member.store.applyReconcile(f.key, f.recipient, time.Now().Unix()))
		unchanged()
	})

	t.Run("lock before write", func(t *testing.T) {
		f := cleanupPendingFixture(t)
		before, err := os.ReadFile(f.outboxPath)
		require.NoError(t, err)
		unchanged := f.snapshot(t)
		release := make(chan struct{})
		locked := make(chan struct{})
		go func() {
			_ = messaging.WithGroupOutboxLock(func(string) error {
				close(locked)
				<-release
				return nil
			})
		}()
		<-locked
		done := make(chan error, 1)
		go func() {
			done <- f.member.store.applyReconcile(f.key, f.recipient, time.Now().Unix())
		}()
		select {
		case err := <-done:
			t.Fatalf("apply finished while the outbox lock was held: %v", err)
		case <-time.After(500 * time.Millisecond):
		}
		unchanged()
		close(release)
		require.NoError(t, <-done)
		after, err := os.ReadFile(f.outboxPath)
		require.NoError(t, err)
		require.NotEqual(t, string(before), string(after))
	})

	t.Run("hold writes nothing", func(t *testing.T) {
		f := newFailureFixture(t)
		unchanged := f.snapshot(t)
		require.NoError(t, f.member.store.applyReconcile(f.key, f.recipient, time.Now().Unix()))
		unchanged()
	})

	t.Run("conflict keeps bytes", func(t *testing.T) {
		f := newFailureFixture(t)
		_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET queue_id=? WHERE event_id=?`, "ffffffffffffffffffffffffffffffff", f.eventID)
		require.NoError(t, err)
		unchanged := f.snapshot(t)
		require.NoError(t, f.member.store.applyReconcile(f.key, f.recipient, time.Now().Unix()))
		unchanged()
	})

	t.Run("stale recovery does not open an epoch", func(t *testing.T) {
		f := newFailureFixture(t)
		newQueue := f.crashD6BeforeT2(t)
		require.NoError(t, f.member.store.applyReconcile(f.key, f.recipient, 1_800_000_000))
		require.Equal(t, newQueue, f.recovery(t).QueueID)
		f.exhaust(t)
		require.Equal(t, string(RecipientFailed), f.row(t).State)
		unchanged := f.snapshot(t)
		require.NoError(t, f.member.store.applyReconcile(f.key, f.recipient, 1_800_000_000))
		unchanged()
		require.Equal(t, newQueue, f.recovery(t).QueueID)
		require.Equal(t, newQueue, f.outboxEntry(t).QueueID)
	})

	t.Run("concurrent witness replay", func(t *testing.T) {
		f := newFailureFixture(t)
		newQueue := f.crashD6BeforeT2(t)
		errCh := make(chan error, 4)
		for range 4 {
			go func() {
				errCh <- f.member.store.applyReconcile(f.key, f.recipient, 1_800_000_000)
			}()
		}
		for range 4 {
			require.NoError(t, <-errCh)
		}
		after := f.recovery(t)
		require.Equal(t, string(RecipientQueued), after.State)
		require.Equal(t, newQueue, after.QueueID)
		require.Equal(t, newQueue, f.outboxEntry(t).QueueID)
	})

	t.Run("replay error is returned", func(t *testing.T) {
		f := newFailureFixture(t)
		newQueue := f.crashD6BeforeT2(t)
		_, err := f.member.db.Exec(`CREATE TRIGGER inject_t2_failure BEFORE UPDATE OF last_recovery_id ON groupchat_fanout
			BEGIN SELECT RAISE(ABORT, 'injected T2 prime failure'); END`)
		require.NoError(t, err)
		require.Error(t, f.member.store.applyReconcile(f.key, f.recipient, 1_800_000_000))
		require.Equal(t, string(RecipientFailed), f.row(t).State)
		require.NotEqual(t, newQueue, f.recovery(t).QueueID)
		require.Equal(t, newQueue, f.outboxEntry(t).QueueID)
	})

	t.Run("load error is returned", func(t *testing.T) {
		f := newFailureFixture(t)
		require.NoError(t, f.member.db.Close())
		require.Error(t, f.member.store.applyReconcile(f.key, f.recipient, 1_800_000_000))
	})

	t.Run("negative recovery clock", func(t *testing.T) {
		f := newFailureFixture(t)
		_ = f.crashD6BeforeT2(t)
		unchanged := f.snapshot(t)
		require.Error(t, f.member.store.applyReconcile(f.key, f.recipient, -1))
		unchanged()
		require.Equal(t, string(RecipientFailed), f.row(t).State)
	})

	t.Run("witnessed recovery", func(t *testing.T) {
		f := newFailureFixture(t)
		newQueue := f.crashD6BeforeT2(t)
		require.NoError(t, f.member.store.applyReconcile(f.key, f.recipient, 1_800_000_000))
		after := f.recovery(t)
		require.Equal(t, string(RecipientQueued), after.State)
		require.Equal(t, newQueue, after.QueueID)
		unchanged := f.snapshot(t)
		require.NoError(t, f.member.store.applyReconcile(f.key, f.recipient, 1_800_000_000))
		unchanged()
	})

	t.Run("no relay publish", func(t *testing.T) {
		file := applyReconcileSource(t)
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		require.NoError(t, err)
		var callsRecovery bool
		ast.Inspect(parsed, func(n ast.Node) bool {
			var name string
			switch node := n.(type) {
			case *ast.Ident:
				name = node.Name
			case *ast.SelectorExpr:
				name = node.Sel.Name
			default:
				return true
			}
			if name == "replayFanoutRecoveryTx" {
				callsRecovery = true
			}
			if name == "authorizeFanoutRecovery" {
				t.Fatal("authorizeFanoutRecovery")
			}
			if strings.Contains(strings.ToLower(name), "publish") {
				t.Fatalf("selector %s", name)
			}
			return true
		})
		require.True(t, callsRecovery)
		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(path, "relay") || strings.Contains(path, "websocket") || strings.Contains(path, "/nostr") || strings.Contains(path, "daemon") {
				t.Fatalf("import %s", path)
			}
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			if _, ok := n.(*ast.ForStmt); ok {
				t.Fatal("loop")
			}
			if _, ok := n.(*ast.RangeStmt); ok {
				t.Fatal("loop")
			}
			return true
		})
	})
}

func cleanupPendingFixture(t *testing.T) failureFixture {
	t.Helper()
	f := newFailureFixture(t)
	before, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.NoError(t, f.member.store.commitFanoutAccepted(f.startAttempt(t, 1), time.Now().Unix()))
	require.NoError(t, os.WriteFile(f.outboxPath, before, 0600))
	return f
}

func applyReconcileSource(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Join(filepath.Dir(file), "reconcile_apply.go")
}
