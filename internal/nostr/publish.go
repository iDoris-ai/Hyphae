package nostr

import (
	"context"
	"encoding/json"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/urfave/cli/v3"
)

var PublishCmd = &cli.Command{
	Name:  "publish",
	Usage: "Publish a JSON event",
	Description: `Publish a nostr event from JSON.
Example: agent-speaker publish '{"kind":1,"content":"Hello"}'`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "sec",
			Aliases:  []string{"s"},
			Usage:    "Secret key",
			Required: true,
		},
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
			Usage:   "Relay URLs",
			Value:   []string{"wss://relay.aastar.io"},
		},
	},
	Arguments: []cli.Argument{
		&cli.StringArg{
			Name: "json",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		jsonStr := c.String("json")
		if jsonStr == "" {
			return fmt.Errorf("JSON event is required")
		}

		var event nostr.Event
		if err := json.Unmarshal([]byte(jsonStr), &event); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}

		secKeyStr := c.String("sec")
		secKey, err := common.ParseSecretKey(secKeyStr)
		if err != nil {
			return fmt.Errorf("invalid secret key: %w", err)
		}

		pubKey := secKey.Public()
		event.PubKey = pubKey
		event.Sign(secKey)

		relays := c.StringSlice("relay")
		results := common.PublishToRelays(ctx, &event, relays)

		success := 0
		for relay, err := range results {
			if err != nil {
				fmt.Printf("❌ %s: %v\n", relay, err)
			} else {
				fmt.Printf("✅ %s\n", relay)
				success++
			}
		}

		fmt.Printf("\nPublished to %d/%d relays\n", success, len(relays))
		return nil
	},
}
