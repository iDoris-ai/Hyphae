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

// jsonMode is set by the root command's Before hook, before any subcommand
// Action runs, and read again in main's top-level error handler after
// app.Run returns (which no longer has a *cli.Command in scope to query).
var jsonMode bool

func main() {
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
		Before: func(ctx context.Context, c *cli.Command) (context.Context, error) {
			jsonMode = common.JSONMode(c)
			return ctx, nil
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
		os.Exit(common.EmitError(jsonMode, err))
	}
}
