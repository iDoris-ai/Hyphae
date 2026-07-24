package nostr

import (
	"context"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/urfave/cli/v3"
)

// relayInfoURLArg receives the "info" subcommand's positional "relay_url"
// argument. See the identical verifyEventArg pattern + comment in
// verify.go: a single-value Argument in this urfave/cli version is only
// populated via Destination.
var relayInfoURLArg string

var RelayCmd = &cli.Command{
	Name:  "relay",
	Usage: "Relay information and testing",
	Commands: []*cli.Command{
		{
			Name:  "info",
			Usage: "Get relay connection info",
			Arguments: []cli.Argument{
				&cli.StringArg{
					Name:        "relay_url",
					Max:         1,
					Destination: &relayInfoURLArg,
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				relayURL := relayInfoURLArg
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
