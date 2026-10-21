package groupchat

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingProvider struct {
	calls atomic.Int32
	mu    sync.Mutex
	last  GroupSendCall
	err   error
}

func (p *recordingProvider) Send(call GroupSendCall) error {
	p.calls.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.last = call
	return p.err
}

func (p *recordingProvider) snapshot() (int32, GroupSendCall) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls.Load(), p.last
}

func TestGroupProviderRegistry(t *testing.T) {
	fresh := GroupSendToken{ID: "evt-1", ExpiresAt: 50}

	t.Run("missing provider", func(t *testing.T) {
		reg := NewGroupProviderRegistry()
		decoy := &recordingProvider{}
		require.NoError(t, reg.Register("other", decoy))
		ran, err := reg.Dispatch("missing", fresh, 49, ReconcileAccepted)
		require.ErrorIs(t, err, ErrGroupProviderNotRegistered)
		require.False(t, ran)
		require.Zero(t, decoy.calls.Load())
		_, err = reg.Lookup("missing")
		require.ErrorIs(t, err, ErrGroupProviderNotRegistered)
	})

	t.Run("lookup by name", func(t *testing.T) {
		reg := NewGroupProviderRegistry()
		provider := &recordingProvider{}
		require.NoError(t, reg.Register("group", provider))
		found, err := reg.Lookup("group")
		require.NoError(t, err)
		require.Equal(t, GroupSendProvider(provider), found)
		ran, err := reg.Dispatch("group", fresh, 49, ReconcileAccepted)
		require.NoError(t, err)
		require.True(t, ran)
		calls, got := provider.snapshot()
		require.EqualValues(t, 1, calls)
		require.Equal(t, fresh, got.Token)
		require.Equal(t, ReconcileAccepted, got.Decision)
		replacement := &recordingProvider{}
		require.NoError(t, reg.Register("group", replacement))
		found, err = reg.Lookup("group")
		require.NoError(t, err)
		require.Equal(t, GroupSendProvider(replacement), found)
	})

	t.Run("expired token", func(t *testing.T) {
		reg := NewGroupProviderRegistry()
		provider := &recordingProvider{}
		require.NoError(t, reg.Register("group", provider))
		for _, token := range []GroupSendToken{
			{ID: "evt-1", ExpiresAt: 50},
			{ID: "evt-1", ExpiresAt: 49},
		} {
			ran, err := reg.Dispatch("group", token, 50, ReconcileAccepted)
			require.NoError(t, err)
			require.False(t, ran)
		}
		require.Zero(t, provider.calls.Load())
	})

	t.Run("held decision", func(t *testing.T) {
		reg := NewGroupProviderRegistry()
		provider := &recordingProvider{}
		require.NoError(t, reg.Register("group", provider))
		ran, err := reg.Dispatch("group", fresh, 49, ReconcileHold)
		require.NoError(t, err)
		require.False(t, ran)
		ran, err = reg.Dispatch("group", GroupSendToken{ID: "evt-1", ExpiresAt: 1}, 49, ReconcileHold)
		require.NoError(t, err)
		require.False(t, ran)
		require.Zero(t, provider.calls.Load())
	})

	t.Run("orphan is handed over", func(t *testing.T) {
		reg := NewGroupProviderRegistry()
		provider := &recordingProvider{}
		require.NoError(t, reg.Register("group", provider))
		ran, err := reg.Dispatch("group", fresh, 49, ReconcileOrphan)
		require.NoError(t, err)
		require.True(t, ran)
		_, got := provider.snapshot()
		require.Equal(t, ReconcileOrphan, got.Decision)
	})

	t.Run("provider error is returned", func(t *testing.T) {
		reg := NewGroupProviderRegistry()
		provider := &recordingProvider{err: errors.New("send failed")}
		require.NoError(t, reg.Register("group", provider))
		ran, err := reg.Dispatch("group", fresh, 49, ReconcileAccepted)
		require.True(t, ran)
		require.EqualError(t, err, "send failed")
		require.EqualValues(t, 1, provider.calls.Load())
	})

	t.Run("negative clock", func(t *testing.T) {
		reg := NewGroupProviderRegistry()
		provider := &recordingProvider{}
		require.NoError(t, reg.Register("group", provider))
		ran, err := reg.Dispatch("group", fresh, -1, ReconcileAccepted)
		require.NoError(t, err)
		require.False(t, ran)
		require.Zero(t, provider.calls.Load())
	})

	t.Run("nil registry and blank register", func(t *testing.T) {
		var reg *GroupProviderRegistry
		provider := &recordingProvider{}
		_, err := reg.Lookup("group")
		require.ErrorIs(t, err, ErrGroupProviderNotRegistered)
		ran, err := reg.Dispatch("group", fresh, 49, ReconcileAccepted)
		require.False(t, ran)
		require.ErrorIs(t, err, ErrGroupProviderNotRegistered)
		require.ErrorIs(t, reg.Register("group", provider), ErrGroupProviderNotRegistered)
		reg = NewGroupProviderRegistry()
		require.Error(t, reg.Register("", provider))
		require.Error(t, reg.Register("group", nil))
		_, err = reg.Lookup("group")
		require.ErrorIs(t, err, ErrGroupProviderNotRegistered)
		other := NewGroupProviderRegistry()
		require.NoError(t, reg.Register("group", provider))
		_, err = other.Lookup("group")
		require.ErrorIs(t, err, ErrGroupProviderNotRegistered)
	})

	t.Run("concurrent dispatch", func(t *testing.T) {
		reg := NewGroupProviderRegistry()
		provider := &recordingProvider{}
		require.NoError(t, reg.Register("group", provider))
		errCh := make(chan error, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for n := 0; n < 20; n++ {
					ran, err := reg.Dispatch("group", fresh, 49, ReconcileAccepted)
					if err != nil || !ran {
						errCh <- errors.New("dispatch missed a live call")
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			require.NoError(t, err)
		}
		require.EqualValues(t, 160, provider.calls.Load())
	})

	t.Run("no relay dial", func(t *testing.T) {
		_, file, _, ok := runtime.Caller(0)
		require.True(t, ok)
		path := filepath.Join(filepath.Dir(file), "provider.go")
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		var hasSend bool
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
			if name == "Send" {
				hasSend = true
			}
			switch name {
			case "Dial", "Listen", "Publish":
				t.Fatalf("selector %s", name)
			}
			return true
		})
		require.True(t, hasSend)
		for _, imp := range parsed.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			switch {
			case strings.Contains(importPath, "relay"),
				strings.Contains(importPath, "websocket"),
				strings.Contains(importPath, "/nostr"),
				strings.Contains(importPath, "daemon"),
				importPath == "net",
				strings.HasPrefix(importPath, "net/"):
				t.Fatalf("import %s", importPath)
			}
		}
	})
}
