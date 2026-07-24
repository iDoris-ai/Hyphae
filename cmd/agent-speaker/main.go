package main

import (
	"context"
	"os"

	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/AuraAIHQ/agent-speaker/internal/daemon"
	"github.com/AuraAIHQ/agent-speaker/internal/group"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/internal/messaging"
	"github.com/AuraAIHQ/agent-speaker/internal/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/profile"
	"github.com/AuraAIHQ/agent-speaker/internal/storage"
	"github.com/AuraAIHQ/agent-speaker/internal/tui"
	"github.com/urfave/cli/v3"
)

var version = "dev"

func main() {
	// messaging.OutboxCmd lives in internal/messaging (not internal/storage)
	// to avoid an import cycle -- internal/messaging already imports
	// internal/storage for the SQLite message store, so internal/storage
	// can't import internal/messaging back. Wiring it into `storage outbox`
	// happens here, at the composition root, which can see both packages.
	storage.StorageCmd.Commands = append(storage.StorageCmd.Commands, messaging.OutboxCmd)

	app := &cli.Command{
		Name:    "agent-speaker",
		Usage:   "A nostr-based agent communication CLI",
		Version: version,
		Flags: []cli.Flag{
			// Local defaults to false in urfave/cli v3.0.0-beta1, which means
			// this flag is already inherited by every subcommand (see
			// TestPersistentFlag in the vendored library) — no extra opt-in needed.
			&cli.BoolFlag{
				Name:  "json",
				Usage: "Machine-readable output: stdout is a JSON envelope, stderr is a JSON error, exit code is semantic (also settable via AGENT_SPEAKER_OUTPUT=json)",
			},
		},
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
			// Group Chat
			group.GroupCmd,
			// Agent Profile
			profile.ProfileCmd,
			// TUI
			tui.TUICmd,
			// Daemon
			daemon.DaemonCmd,
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		// Scan raw argv rather than trusting any *cli.Command flag state: the
		// failure may have happened before the relevant subcommand's flags
		// were even fully parsed (e.g. a missing required flag), so there's
		// no reliable c.Bool("json") in scope at this point. See
		// JSONModeFromArgs's doc comment for why a value latched earlier via
		// a Before hook doesn't work here.
		os.Exit(common.EmitError(common.JSONModeFromArgs(os.Args), err))
	}
}
