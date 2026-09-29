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
		jsonMode := common.JSONMode(c)
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
		if jsonMode {
			result := make([]outboxListEntry, 0, len(entries))
			for _, e := range entries {
				result = append(result, outboxListEntry{
					ID: displayOutboxID(e.ID), RecipientNpub: e.RecipientNpub,
					Relays: nonNilStrings(e.Relays), Status: e.Status,
					RetryCount: e.RetryCount, MaxRetries: e.MaxRetries,
					CreatedAt: e.CreatedAt, LastAttempt: e.LastAttempt,
					DuplicateID: idCount[e.ID] > 1,
					Stuck:       e.Status == "pending" && e.RetryCount >= e.MaxRetries,
				})
			}
			common.Emit(true, result, nil)
			return nil
		}
		if len(entries) == 0 {
			fmt.Println("📭 Outbox is empty (or nothing matches --failed-only)")
			return nil
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

type outboxListEntry struct {
	ID            string   `json:"id"`
	RecipientNpub string   `json:"recipient_npub"`
	Relays        []string `json:"relays"`
	Status        string   `json:"status"`
	RetryCount    int      `json:"retry_count"`
	MaxRetries    int      `json:"max_retries"`
	CreatedAt     int64    `json:"created_at"`
	LastAttempt   int64    `json:"last_attempt"`
	DuplicateID   bool     `json:"duplicate_id"`
	Stuck         bool     `json:"stuck"`
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

var outboxClearCmd = &cli.Command{
	Name:  "clear",
	Usage: "Permanently remove failed/exhausted outbox entries",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "failed",
			Usage: "Clear entries that are status=failed, or have retry_count >= --min-failures",
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
		jsonMode := common.JSONMode(c)
		minFailures := int(c.Int("min-failures"))
		if !c.Bool("failed") {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("--failed is required"))
		}
		if jsonMode {
			if !c.Bool("yes") {
				return common.NewExitError(common.ErrCodeUser, fmt.Errorf("JSON clear requires --yes"))
			}
			if minFailures < 1 {
				return common.NewExitError(common.ErrCodeUser, fmt.Errorf("--min-failures must be at least 1"))
			}
		}

		ob, err := LoadOutbox()
		if err != nil {
			return fmt.Errorf("failed to load outbox: %w", err)
		}
		needsQueueIDs := false
		for _, entry := range ob.Entries {
			if isOutboxClearable(entry, minFailures) && entry.QueueID == "" {
				needsQueueIDs = true
				break
			}
		}
		if needsQueueIDs {
			ob, err = UpdateOutbox(func(latest *types.Outbox) error {
				for i := range latest.Entries {
					entry := &latest.Entries[i]
					if isOutboxClearable(*entry, minFailures) && entry.QueueID == "" {
						queueID, err := newOutboxQueueID()
						if err != nil {
							return err
						}
						entry.QueueID = queueID
					}
				}
				return nil
			})
			if err != nil {
				return fmt.Errorf("failed to prepare outbox clear snapshot: %w", err)
			}
		}

		toClear := make([]types.OutboxEntry, 0)
		for _, e := range ob.Entries {
			if isOutboxClearable(e, minFailures) {
				toClear = append(toClear, e)
			}
		}

		if len(toClear) == 0 {
			if jsonMode {
				common.Emit(true, map[string]int{"removed": 0, "remaining": len(ob.Entries)}, nil)
				return nil
			}
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

		updated, removed, err := clearConfirmedOutboxEntries(toClear)
		if err != nil {
			return fmt.Errorf("failed to save outbox: %w", err)
		}

		if jsonMode {
			common.Emit(true, map[string]int{"removed": removed, "remaining": len(updated.Entries)}, nil)
			return nil
		}
		fmt.Printf("✅ Removed %d entr%s; %d remain\n", removed, plural(removed), len(updated.Entries))
		return nil
	},
}

func isOutboxClearable(entry types.OutboxEntry, minFailures int) bool {
	return entry.Status == "failed" || entry.RetryCount >= minFailures
}

func clearConfirmedOutboxEntries(confirmed []types.OutboxEntry) (*types.Outbox, int, error) {
	removed := 0
	updated, err := UpdateOutbox(func(latest *types.Outbox) error {
		removed = removeConfirmedOutboxEntries(latest, confirmed)
		return nil
	})
	return updated, removed, err
}

var outboxRetryCmd = &cli.Command{
	Name:  "retry",
	Usage: "Manually retry a single outbox entry now, bypassing the daemon's backoff wait",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "id",
			Usage: "Outbox entry ID (see `storage outbox list`)",
		},
		&cli.StringSliceFlag{
			Name:  "relay",
			Usage: "Fallback relay URLs, used only if the entry has none of its own",
		},
		&cli.IntFlag{
			Name:  "timeout",
			Usage: "Per-relay dial timeout in seconds",
			Value: 5,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		jsonMode := common.JSONMode(c)
		id := c.String("id")
		if id == "" {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("--id is required"))
		}
		timeoutSeconds := int64(c.Int("timeout"))
		const maxTimeoutSeconds = int64((1<<63 - 1) / int64(time.Second))
		if timeoutSeconds <= 0 || timeoutSeconds > maxTimeoutSeconds {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("--timeout must be between 1 and %d seconds", maxTimeoutSeconds))
		}

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
		matches := findOutboxMatches(ob.Entries, id)
		if len(matches) == 0 {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("no outbox entry with id %q", id))
		}
		if len(matches) > 1 {
			// An ID alone cannot distinguish entries that share it. Refuse
			// rather than guess which queued payload the caller intended;
			// `list` marks these entries so they can be inspected.
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf(
				"%d entries share id %q -- refusing to guess which one to retry; use `storage outbox list` to inspect", len(matches), id))
		}

		entry := matches[0]
		relays := entry.Relays
		if len(relays) == 0 {
			relays, err = common.ResolveRelays(c)
			if err != nil {
				return err
			}
		}
		result, sendErr := AttemptSend(ctx, ob, entry, relays, time.Duration(timeoutSeconds)*time.Second)
		output := retryResult(entry.ID, result)
		if !result.Attempted && !result.Superseded && !result.QueueStateUnknown {
			// A pre-publish failure can leave the selected snapshot stale; do not
			// claim that it remains queued unless AttemptSend confirmed that.
			output.QueueStateUnknown = true
		}
		if sendErr != nil {
			err := fmt.Errorf("%s: %w", retryStatusMessage(output), sendErr)
			if result.Superseded {
				return common.NewExitErrorWithData(common.ErrCodeWriteConflict, err, output)
			}
			return common.NewExitErrorWithData(common.ErrCodeOther, err, output)
		}
		if result.Superseded {
			err := fmt.Errorf("%s: queue entry changed or was removed during retry", retryStatusMessage(output))
			return common.NewExitErrorWithData(common.ErrCodeWriteConflict, err, output)
		}
		if !result.Attempted {
			err := fmt.Errorf("%s: retry did not attempt a relay publish", retryStatusMessage(output))
			return common.NewExitErrorWithData(common.ErrCodeOther, err, output)
		}
		common.Emit(jsonMode, output, func() { printOutboxRetryResult(output) })
		return nil
	},
}

type outboxRetryResult struct {
	EventID           string `json:"event_id"`
	Attempted         bool   `json:"attempted"`
	Sent              bool   `json:"sent"`
	Queued            bool   `json:"queued"`
	MarkedFailed      bool   `json:"marked_failed"`
	HistoryStored     bool   `json:"history_stored"`
	Superseded        bool   `json:"superseded"`
	QueueStateUnknown bool   `json:"queue_state_unknown"`
}

func retryResult(id string, result SendResult) outboxRetryResult {
	return outboxRetryResult{
		EventID: displayOutboxID(id), Attempted: result.Attempted, Sent: result.Sent,
		Queued: result.Queued, MarkedFailed: result.MarkedFailed,
		HistoryStored: result.HistoryStored, Superseded: result.Superseded,
		QueueStateUnknown: result.QueueStateUnknown,
	}
}

func retryStatusMessage(result outboxRetryResult) string {
	ack := "relay ACK not received"
	if result.Sent {
		ack = "relay ACK received"
	} else if !result.Attempted {
		ack = "relay publish not attempted"
	}
	queue := "queue status not confirmed"
	switch {
	case result.QueueStateUnknown:
		queue = "queue state unknown"
	case result.Superseded:
		queue = "queue entry superseded"
	case result.Queued:
		queue = "queued for retry"
	case result.MarkedFailed:
		queue = "marked failed"
	case result.Sent:
		queue = "queue entry removed"
	}
	history := "this retry did not confirm a history write"
	if result.HistoryStored {
		history = "history stored by this retry"
	}
	return fmt.Sprintf("event %s; %s; %s; %s", result.EventID, ack, queue, history)
}

func printOutboxRetryResult(result outboxRetryResult) {
	switch {
	case result.Sent:
		fmt.Println("✅ Sent")
	case result.MarkedFailed:
		fmt.Println("❌ Still failing and retries are now exhausted -- marked failed")
	default:
		fmt.Println("❌ Still failing -- will be retried again by the daemon (or run this command again)")
	}
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
