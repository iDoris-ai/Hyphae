package nostr

import (
	"context"
	"encoding/json"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/fatih/color"
	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/urfave/cli/v3"
)

var VerifyCmd = &cli.Command{
	Name:  "verify",
	Usage: "Verify a nostr event signature",
	Description: `Verify that a nostr event has a valid signature.
Example: agent-speaker verify '{"id":"...",...}'`,
	Arguments: []cli.Argument{
		&cli.StringArg{
			Name: "event",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		jsonStr := c.String("event")
		if jsonStr == "" {
			return fmt.Errorf("event JSON is required")
		}

		var event nostr.Event
		if err := json.Unmarshal([]byte(jsonStr), &event); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}

		valid := event.VerifySignature()

		if valid {
			green := color.New(color.FgGreen).SprintFunc()
			fmt.Printf("%s Signature is VALID\n", green("✅"))
			fmt.Printf("   Event ID: %s\n", event.ID)
			fmt.Printf("   Author:   %s\n", common.EncodeNpub(event.PubKey))
		} else {
			red := color.New(color.FgRed).SprintFunc()
			fmt.Printf("%s Signature is INVALID\n", red("❌"))
		}

		return nil
	},
}
