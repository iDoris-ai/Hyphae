package main

import (
	"context"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/AuraAIHQ/agent-speaker/internal/daemon"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/internal/messaging"
	"github.com/AuraAIHQ/agent-speaker/internal/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/storage"
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
			// Nostr base commands
			nostr.KeyCmd,
			nostr.EventCmd,
			nostr.ReqCmd,
			nostr.PublishCmd,
			nostr.DecodeCmd,
			nostr.EncodeCmd,
			nostr.VerifyCmd,
			nostr.RelayCmd,
			// Identity management
			identity.IdentityCmd,
			identity.ContactCmd,
			// Messaging
			messaging.AgentCmd,
			messaging.HistoryCmd,
			// Storage
			storage.StorageCmd,
			// Daemon
			daemon.DaemonCmd,
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
