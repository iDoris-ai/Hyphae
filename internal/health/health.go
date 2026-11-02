// Package health implements `hyphae health`.
package health

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/nostr"
	"github.com/iDoris-ai/hyphae/internal/relayconfig"
	"github.com/urfave/cli/v3"
)

// Package variables so tests can shorten the bounds.
var (
	relayTimeout = 3 * time.Second
	totalTimeout = 5 * time.Second
)

const maxRelays = 16

type keystoreCheck struct {
	OK         bool  `json:"ok"`
	Exists     bool  `json:"exists"`
	Encrypted  *bool `json:"encrypted,omitempty"`
	Identities *int  `json:"identities,omitempty"`
}

type relayItem struct {
	URL       string `json:"url"`
	Reachable bool   `json:"reachable"`
	LatencyMS *int64 `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

type relaysCheck struct {
	OK        bool        `json:"ok"`
	Items     []relayItem `json:"items"`
	Truncated bool        `json:"truncated,omitempty"`
}

type healthChecks struct {
	Keystore keystoreCheck `json:"keystore"`
	Relays   relaysCheck   `json:"relays"`
}

type healthData struct {
	Status string       `json:"status"`
	Checks healthChecks `json:"checks"`
}

// Cmd is `hyphae health`. Exit code is 0 whenever a report was produced.
var Cmd = &cli.Command{
	Name:  "health",
	Usage: "Report keystore and relay health (exit 0 even when degraded/failed)",
	Action: func(ctx context.Context, c *cli.Command) error {
		r, err := relayconfig.New()
		if err != nil {
			return common.NewExitError(common.ErrCodeUser, err)
		}
		res, err := r.Resolve(nil)
		if err != nil {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("relay configuration: %w", err))
		}
		d := buildHealth(ctx, res.Relays)
		common.Emit(common.JSONMode(c), d, func() {
			k := d.Checks.Keystore
			fmt.Printf("status: %s\n", d.Status)
			if k.OK {
				fmt.Printf("keystore: exists=%v encrypted=%v identities=%d\n", k.Exists, *k.Encrypted, *k.Identities)
			} else {
				fmt.Println("keystore: unreadable")
			}
			for _, it := range d.Checks.Relays.Items {
				fmt.Printf("relay %s: reachable=%v %s\n", it.URL, it.Reachable, it.Error)
			}
		})
		return nil
	},
}

func buildHealth(ctx context.Context, urls []string) healthData {
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	d := healthData{Checks: healthChecks{Keystore: checkKeystore()}}
	urls = append([]string(nil), urls...)
	sort.Strings(urls)
	if len(urls) > maxRelays {
		urls, d.Checks.Relays.Truncated = urls[:maxRelays], true
	}
	items := make([]relayItem, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items[i] = probe(ctx, u)
		}()
	}
	wg.Wait()
	up := 0
	for _, it := range items {
		if it.Reachable {
			up++
		}
	}
	d.Checks.Relays.Items = items
	d.Checks.Relays.OK = len(items) > 0 && up == len(items)
	switch {
	case !d.Checks.Keystore.OK || up == 0:
		d.Status = "failed"
	case up < len(items):
		d.Status = "degraded"
	default:
		d.Status = "ok"
	}
	return d
}

// checkKeystore reads keystore.json via LoadKeyStore, which only reads the
// file: no password, no write lock, no directory creation.
func checkKeystore() keystoreCheck {
	ks, err := identity.LoadKeyStore()
	if err != nil {
		// A missing file never reaches here (LoadKeyStore returns an empty store).
		return keystoreCheck{Exists: true}
	}
	enc, n := ks.Encrypted, len(ks.Identities)
	return keystoreCheck{OK: true, Exists: ks.StoreExists, Encrypted: &enc, Identities: &n}
}

func probe(ctx context.Context, url string) relayItem {
	pctx, cancel := context.WithTimeout(ctx, relayTimeout)
	defer cancel()
	start := time.Now()
	err := nostr.ProbeRelay(pctx, url)
	if err != nil {
		return relayItem{URL: url, Error: classify(err)}
	}
	ms := time.Since(start).Milliseconds()
	return relayItem{URL: url, Reachable: true, LatencyMS: &ms}
}

// classify maps a connection error to a fixed vocabulary; raw text can carry
// URLs and IPs and is never exposed.
func classify(err error) string {
	var dnsErr *net.DNSError
	var certErr x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invErr x509.CertificateInvalidError
	var recErr tls.RecordHeaderError
	switch {
	case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		return "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.As(err, &dnsErr):
		return "dns"
	case errors.As(err, &certErr), errors.As(err, &hostErr), errors.As(err, &invErr), errors.As(err, &recErr):
		return "tls"
	}
	return "other"
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
