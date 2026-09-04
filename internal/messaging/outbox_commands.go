package messaging

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/urfave/cli/v3"
)

// OutboxCmd provides read-only diagnostics and cleanup for the outbox.
//
// This lives in internal/messaging (not internal/storage, despite being
// wired up under `storage outbox` in cmd/hyphae/main.go) because
// internal/messaging already imports internal/storage (for the SQLite
// message store) -- putting this command here and appending it to
// storage.StorageCmd.Commands at the composition root avoids an import
// cycle instead of moving outbox logic into internal/storage.
var OutboxCmd = &cli.Command{
	Name:  "outbox",
	Usage: "Inspect and manage the pending-message outbox",
	Commands: []*cli.Command{
		outboxListCmd,
		outboxClearCmd,
		outboxRetryCmd,
	},
}

var outboxListCmd = &cli.Command{
	Name:  "list",
	Usage: "List outbox entries with status, retry count, and age",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "failed-only",
			Usage: "Only show entries that are failed, or stuck pending with retries exhausted",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		ob, err := LoadOutbox()
		if err != nil {
			return fmt.Errorf("failed to load outbox: %w", err)
		}

		entries := ob.Entries
		if c.Bool("failed-only") {
			filtered := make([]types.OutboxEntry, 0, len(entries))
			for _, e := range entries {
				if isFailedOrStuck(e) {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}

		if len(entries) == 0 {
			fmt.Println("📭 Outbox is empty (or nothing matches --failed-only)")
			return nil
		}

		// Outbox entries are keyed by ID for status/retry updates
		// (UpdateOutboxStatus/IncrementOutboxRetry), but IDs are not
		// guaranteed unique -- an event that failed to sign keeps a
		// zero-value ID, and every such entry collides on the same ID.
		// Flagging duplicates here is what makes that visible instead of
		// a silent footgun.
		idCount := make(map[string]int, len(ob.Entries))
		for _, e := range ob.Entries {
			idCount[e.ID]++
		}

		fmt.Printf("📬 Outbox (%d entr%s)\n", len(entries), plural(len(entries)))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "#\tID\tSTATUS\tRETRIES\tAGE\tRECIPIENT")
		for i, e := range entries {
			status := e.Status
			if e.Status == "pending" && e.RetryCount >= e.MaxRetries {
				status = "pending (stuck)"
			}
			// Not truncated, unlike RECIPIENT below: this is the value
			// `retry --id`/`clear` need, and a truncated-then-copy-pasted ID
			// would never match anything.
			id := displayOutboxID(e.ID)
			if idCount[e.ID] > 1 {
				id += " ⚠️dup"
			}
			age := "-"
			if e.CreatedAt > 0 {
				age = time.Since(time.Unix(e.CreatedAt, 0)).Round(time.Second).String()
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%d/%d\t%s\t%s\n",
				i+1, id, status, e.RetryCount, e.MaxRetries, age, truncateOutboxField(e.RecipientNpub, 20))
		}
		w.Flush()
		return nil
	},
}

var outboxClearCmd = &cli.Command{
	Name:  "clear",
	Usage: "Permanently remove failed/exhausted outbox entries",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:     "failed",
			Usage:    "Clear entries that are status=failed, or have retry_count >= --min-failures",
			Required: true,
		},
		&cli.IntFlag{
			Name:  "min-failures",
			Usage: "Retry-count threshold for what counts as clearable",
			Value: 5,
		},
		&cli.BoolFlag{
			Name:  "yes",
			Usage: "Skip the confirmation prompt",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		minFailures := int(c.Int("min-failures"))

		ob, err := LoadOutbox()
		if err != nil {
			return fmt.Errorf("failed to load outbox: %w", err)
		}

		toClear := make([]types.OutboxEntry, 0)
		toKeep := make([]types.OutboxEntry, 0, len(ob.Entries))
		for _, e := range ob.Entries {
			if e.Status == "failed" || e.RetryCount >= minFailures {
				toClear = append(toClear, e)
			} else {
				toKeep = append(toKeep, e)
			}
		}

		if len(toClear) == 0 {
			fmt.Printf("Nothing to clear (no entries with status=failed or retry_count >= %d)\n", minFailures)
			return nil
		}

		if !c.Bool("yes") {
			fmt.Printf("About to permanently remove %d outbox entr%s (status=failed or retry_count >= %d). Continue? [y/N] ",
				len(toClear), plural(len(toClear)), minFailures)
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
				fmt.Println("Aborted -- nothing removed.")
				return nil
			}
		}

		ob.Entries = toKeep
		if err := SaveOutbox(ob); err != nil {
			return fmt.Errorf("failed to save outbox: %w", err)
		}

		fmt.Printf("✅ Removed %d entr%s; %d remain\n", len(toClear), plural(len(toClear)), len(toKeep))
		return nil
	},
}

var outboxRetryCmd = &cli.Command{
	Name:  "retry",
	Usage: "Manually retry a single outbox entry now, bypassing the daemon's backoff wait",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "id",
			Usage:    "Outbox entry ID (see `storage outbox list`)",
			Required: true,
		},
		&cli.StringSliceFlag{
			Name:  "relay",
			Usage: "Fallback relay URLs, used only if the entry has none of its own",
			Value: []string{"wss://relay.aastar.io"},
		},
		&cli.IntFlag{
			Name:  "timeout",
			Usage: "Per-relay dial timeout in seconds",
			Value: 5,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		ob, err := LoadOutbox()
		if err != nil {
			return fmt.Errorf("failed to load outbox: %w", err)
		}

		// `list` displays IDs hex-encoded (see displayOutboxID) since a
		// legacy entry's raw stored ID is arbitrary bytes, not printable
		// text. Try that hex form first; only fall back to a literal match
		// (for a caller that already has the raw ID, e.g. scripting against
		// LoadOutbox directly) if nothing matches the decoded bytes -- a
		// value that happens to be valid hex but was meant literally must
		// still be
		// reachable.
		id := c.String("id")
		matches := findOutboxMatches(ob.Entries, id)
		if len(matches) == 0 {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("no outbox entry with id %q", id))
		}
		if len(matches) > 1 {
			// AttemptSend's RemoveFromOutbox removes every entry sharing an
			// ID, not just the one retried -- with duplicates, a successful
			// send would silently delete the OTHER (still-unsent) entries
			// too. Refuse rather than risk that; `list` is how to see and
			// understand this pre-existing data issue.
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf(
				"%d entries share id %q (pre-existing outbox data issue, see specs/m1.5/README.md) -- refusing to retry, since a successful send would remove all of them, not just one; use `storage outbox list` to inspect", len(matches), id))
		}

		timeout := time.Duration(c.Int("timeout")) * time.Second
		result, err := AttemptSend(ctx, ob, matches[0], c.StringSlice("relay"), timeout)
		if !result.Attempted {
			return fmt.Errorf("retry failed: %w", err)
		}
		if err != nil {
			fmt.Printf("⚠️  %v\n", err)
		}

		switch {
		case result.Sent:
			fmt.Println("✅ Sent")
		case result.MarkedFailed:
			fmt.Println("❌ Still failing and retries are now exhausted -- marked failed")
		default:
			fmt.Println("❌ Still failing -- will be retried again by the daemon (or run this command again)")
		}
		return nil
	},
}

// findOutboxMatches resolves a user-supplied ID (copy-pasted from `list`,
// which always displays hex per displayOutboxID) to outbox entries. It
// tries hex-decoding first, matching against legacy entries that still
// store the raw event.ID bytes directly; if that yields nothing, it falls
// back to a literal match, which is what succeeds for entries written by
// AddToOutbox's current hex-encoded-storage behavior (the pasted hex string
// IS the stored value for those, so decoding it first and comparing the
// decoded bytes against a hex-string entry.ID would never match).
func findOutboxMatches(entries []types.OutboxEntry, id string) []types.OutboxEntry {
	if decoded, err := hex.DecodeString(id); err == nil {
		if matches := matchOutboxID(entries, string(decoded)); len(matches) > 0 {
			return matches
		}
	}
	return matchOutboxID(entries, id)
}

func matchOutboxID(entries []types.OutboxEntry, id string) []types.OutboxEntry {
	var matches []types.OutboxEntry
	for _, e := range entries {
		if e.ID == id {
			matches = append(matches, e)
		}
	}
	return matches
}

// isFailedOrStuck reports whether an outbox entry should be treated as
// effectively failed even if its raw Status still says "pending" --
// GetPendingOutbox stops returning an entry once RetryCount reaches
// MaxRetries, so such an entry can never actually reach the "failed" status
// transition (that only happens inside a send attempt, which never gets
// scheduled again). Surfacing that here is what makes `list --failed-only`
// tell the truth about entries the daemon will never revisit.
func isFailedOrStuck(e types.OutboxEntry) bool {
	return e.Status == "failed" || (e.Status == "pending" && e.RetryCount >= e.MaxRetries)
}

func truncateOutboxField(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// displayOutboxID renders an outbox entry ID as readable hex for `list`.
// AddToOutbox now stores IDs already hex-encoded, but entries written by an
// older binary (before that fix) may still be on disk holding the raw
// event.ID bytes directly -- hex-encoding those too keeps `list`'s output
// readable either way, without double-encoding an ID that's already hex.
// A 64-character string that's valid hex is assumed to already be encoded:
// a raw 32-byte Nostr event ID happening to consist entirely of ASCII hex
// digit bytes by chance is astronomically unlikely.
func displayOutboxID(id string) string {
	if len(id) == 64 {
		if _, err := hex.DecodeString(id); err == nil {
			return id
		}
	}
	return hex.EncodeToString([]byte(id))
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
