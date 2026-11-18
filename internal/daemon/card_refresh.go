package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/iDoris-ai/hyphae/internal/friend"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/notify"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// cardRefresher keeps the identity's directory card live: the daemon calls
// it at start and hourly. The first card of a default-public identity is
// announced, because from then on others can find the person.
type cardRefresher struct {
	id       *types.Identity
	ks       *types.KeyStore
	relays   []string
	notify   bool
	logger   *daemonLogger
	desktop  func(title, message string) error
	nowUnix  func() int64
	refreshF func(context.Context, *types.Identity, []string, int64) (friend.RefreshResult, error)
}

func newCardRefresher(id *types.Identity, ks *types.KeyStore, relays []string, useNotify bool, logger *daemonLogger) *cardRefresher {
	r := &cardRefresher{id: id, ks: ks, relays: relays, notify: useNotify, logger: logger,
		desktop: notify.DesktopNotification, nowUnix: func() int64 { return time.Now().Unix() }}
	r.refreshF = func(ctx context.Context, id *types.Identity, relays []string, now int64) (friend.RefreshResult, error) {
		sk, err := identity.GetSecretKey(r.ks, id.Nickname)
		if err != nil {
			return friend.RefreshResult{}, err
		}
		return friend.Refresh(ctx, id, sk, relays, now)
	}
	return r
}

func (r *cardRefresher) run(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	res, err := r.refreshF(ctx, r.id, r.relays, r.nowUnix())
	ts := time.Now().Format("15:04:05")
	if err != nil {
		r.logger.printf("[%s] ⚠️  Directory card refresh failed: %v\n", ts, err)
		return
	}
	if !res.Published {
		return
	}
	if !res.Card.Visible {
		r.logger.printf("[%s] 🙈 Hide resent: new searches no longer find you.\n", ts)
		return
	}
	r.logger.printf("[%s] 📇 Directory card published (tags %s), live until %s.\n", ts,
		strings.Join(res.Card.Tags, ","), time.Unix(res.Card.ExpiresAt, 0).Format(time.DateTime))
	if !res.FirstTime {
		return
	}
	msg := fmt.Sprintf("People on your relay can now find you by tag %s. To hide: hyphae friend visibility hidden --as %s",
		strings.Join(res.Card.Tags, ","), r.id.Nickname)
	r.logger.printf("[%s] 📣 You are public (the default). %s\n", ts, msg)
	if r.notify {
		_ = r.desktop("已公开 / You are findable", msg)
	}
}
