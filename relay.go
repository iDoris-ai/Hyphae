package main

import (
	"context"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/urfave/cli/v3"
)

var relayCmd = &cli.Command{
	Name:  "relay",
	Usage: "Relay information and testing",
	Commands: []*cli.Command{
		{
			Name:  "info",
			Usage: "Get relay connection info",
			Arguments: []cli.Argument{
				&cli.StringArg{
					Name: "relay_url",
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				relayURL := c.String("relay_url")
				if relayURL == "" {
					relayURL = "wss://relay.aastar.io"
				}

				relay, err := nostr.RelayConnect(ctx, relayURL, nostr.RelayOptions{})
				if err != nil {
					return fmt.Errorf("failed to connect: %w", err)
				}
				defer relay.Close()

				fmt.Printf("Connected to %s\n", relayURL)
				return nil
			},
		},
	},
}
