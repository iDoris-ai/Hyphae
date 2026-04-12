package main

import (
	"context"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/urfave/cli/v3"
)

var version = "dev"

func main() {
	color.NoColor = false

	app := &cli.Command{
		Name:    "agent-speaker",
		Usage:   "A nostr-based agent communication CLI",
		Version: version,
		Commands: []*cli.Command{
			// Identity and contact management
			identityCmd,
			contactCmd,
			
			// Messaging
			agentCmdV2,
			
			// Message history
			historyCmd,
			
			// Legacy commands (for advanced users)
			keyCmd,
			eventCmd,
			reqCmd,
			relayCmd,
			publishCmd,
			decodeCmd,
			encodeCmd,
			verifyCmd,
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
