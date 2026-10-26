package daemon

import (
	"context"
	"sync"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/stretchr/testify/require"
)

func TestGroupPublisherNeedsDaemonContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	groupPublishTarget.Lock()
	groupPublishTarget.ctx = nil
	groupPublishTarget.Unlock()
	err := groupPublisher{}.Send(groupchat.GroupSendCall{Token: groupchat.GroupSendToken{ID: "evt"}})
	require.ErrorContains(t, err, "no daemon context")
}

// ensureGroupPublisher (daemon loop) and Send (dispatch) share the target;
// run them together under -race.
func TestGroupPublishTargetIsSafeUnderConcurrentUse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	useDaemonGroupProviders(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ensureGroupPublisher(context.Background(), []string{"ws://127.0.0.1:1"})
		}()
		go func() {
			defer wg.Done()
			_ = groupPublisher{}.Send(groupchat.GroupSendCall{Token: groupchat.GroupSendToken{ID: "missing"}})
		}()
	}
	wg.Wait()
}
