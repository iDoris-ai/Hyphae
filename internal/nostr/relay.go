package nostr

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/relayconfig"
	"github.com/urfave/cli/v3"
)

var relayInfoURLArg string

var RelayCmd = &cli.Command{
	Name: "relay", Usage: "Manage relay settings and test connections",
	Commands: []*cli.Command{
		{Name: "list", Usage: "List configured relays", Action: relayList},
		{Name: "set", Usage: "Replace the configured relay list", Flags: []cli.Flag{
			&cli.StringSliceFlag{Name: "relay", Aliases: []string{"r"}, Usage: "Relay URL (repeatable)"},
		}, Action: relaySet},
		{
			Name: "info", Usage: "Test a bounded WebSocket connection",
			Arguments: []cli.Argument{&cli.StringArg{Name: "relay_url", Max: 1, Destination: &relayInfoURLArg}},
			Flags:     []cli.Flag{&cli.IntFlag{Name: "timeout", Usage: "Connection timeout in seconds", Value: 5}},
			Action:    relayInfo,
		},
	},
}

func relayList(ctx context.Context, c *cli.Command) error {
	r, err := relayconfig.New()
	if err != nil {
		return err
	}
	result, err := r.Resolve(nil)
	if err != nil {
		return common.NewExitError(common.ErrCodeOther, fmt.Errorf("relay configuration: %w", err))
	}
	common.Emit(common.JSONMode(c), result, func() {
		fmt.Printf("Relays (%s):\n", result.Source)
		for _, url := range result.Relays {
			fmt.Println("  " + url)
		}
	})
	return nil
}

func relaySet(ctx context.Context, c *cli.Command) error {
	r, err := relayconfig.New()
	if err != nil {
		return err
	}
	urls := c.StringSlice("relay")
	if err := relayconfig.Validate(urls); err != nil {
		return common.NewExitError(common.ErrCodeUser, err)
	}
	if err := r.Set(urls); err != nil {
		return common.NewExitError(common.ErrCodeOther, fmt.Errorf("save relay configuration: %w", err))
	}
	result := relayconfig.Resolution{Relays: urls, Source: "config"}
	common.Emit(common.JSONMode(c), result, func() { fmt.Printf("Saved %d relay(s)\n", len(urls)) })
	return nil
}

func relayInfo(ctx context.Context, c *cli.Command) error {
	url := relayInfoURLArg
	if url == "" {
		r, err := relayconfig.New()
		if err != nil {
			return err
		}
		result, err := r.Resolve(nil)
		if err != nil {
			return common.NewExitError(common.ErrCodeOther, fmt.Errorf("relay configuration: %w", err))
		}
		url = result.Relays[0]
	} else if err := relayconfig.Validate([]string{url}); err != nil {
		return common.NewExitError(common.ErrCodeUser, err)
	}
	seconds := c.Int("timeout")
	if seconds <= 0 || int64(seconds) > math.MaxInt64/int64(time.Second) {
		return common.NewExitError(common.ErrCodeUser, errors.New("timeout must be a positive number of seconds within range"))
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	relay, err := nostr.RelayConnect(probeCtx, url, nostr.RelayOptions{})
	if err != nil {
		return common.NewExitError(common.ErrCodeNetwork, fmt.Errorf("failed to connect: %w", err))
	}
	defer relay.Close()
	common.Emit(common.JSONMode(c), map[string]any{"url": url, "connected": true}, func() { fmt.Printf("Connected to %s\n", url) })
	return nil
}
