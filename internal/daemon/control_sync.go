package daemon

import (
	"context"
	"time"

	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// runControlSync applies friend_accept control messages for id every
// interval, outside the inbox scan (control messages use their own filter, so
// the scan and its status stream are unchanged). The first sync waits one
// interval; accepts are not urgent.
func runControlSync(ctx context.Context, id *types.Identity, ks *types.KeyStore, relays []string,
	every time.Duration, logger *daemonLogger) {
	sk, err := identity.GetSecretKey(ks, id.Nickname)
	if err != nil {
		logger.printf("[%s] ⚠️  friend_accept sync disabled: %v\n", time.Now().Format("15:04:05"), err)
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		friends, _ := messaging.SyncControl(ctx, relays, sk, 5*time.Second)
		for _, f := range friends {
			logger.printf("\n[%s] 🤝 %s accepted you as a friend; the stranger limit is lifted\n", time.Now().Format("15:04:05"), f)
		}
	}
}
