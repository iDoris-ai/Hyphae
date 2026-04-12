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
	// Set up color output
	color.NoColor = false

	app := &cli.Command{
		Name:    "agent-speaker",
		Usage:   "A nostr-based agent communication CLI",
		Version: version,
		Commands: []*cli.Command{
			keyCmd,
			eventCmd,
			reqCmd,
			agentCmd,
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
