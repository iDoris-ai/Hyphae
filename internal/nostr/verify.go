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

// verifyEventArg receives VerifyCmd's positional "event" argument. In this
// urfave/cli version, a single-value Argument (Max == 1) is only actually
// populated when Destination is set -- neither c.String(name) (that's for
// Flags only) nor c.Args().First() reads it. Without Destination, the
// command's own usage example ("agent-speaker verify '{...}'") always
// failed with "event JSON is required", silently, because nothing ever
// reads the positional value.
var verifyEventArg string

var VerifyCmd = &cli.Command{
	Name:  "verify",
	Usage: "Verify a nostr event signature",
	Description: `Verify that a nostr event has a valid signature.
Example: agent-speaker verify '{"id":"...",...}'`,
	Arguments: []cli.Argument{
		&cli.StringArg{
			Name:        "event",
			Max:         1,
			Destination: &verifyEventArg,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		jsonStr := verifyEventArg
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
