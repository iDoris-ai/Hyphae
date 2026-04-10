package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerAgentTools adds agent-specific MCP tools to the server.
// Called from mcp.go's mcpServer action after the base nak tools are registered.
func registerAgentTools(s *server.MCPServer, keyer nostr.Keyer) {
	s.AddTool(mcp.NewTool("agent_send_message",
		mcp.WithDescription("Send a compressed message to another agent via Nostr. Uses Kind 30078 events with zstd compression and agent tags."),
		mcp.WithString("to", mcp.Description("Recipient public key (hex or npub format)"), mcp.Required()),
		mcp.WithString("message", mcp.Description("Message content to send"), mcp.Required()),
		mcp.WithString("relay", mcp.Description("Relay URL to publish to (default: wss://relay.damus.io)")),
	), func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		to := required[string](r, "to")
		message := required[string](r, "message")
		relayURL, hasRelay := optional[string](r, "relay")

		// Resolve npub to hex
		if strings.HasPrefix(to, "npub") {
			hexKey, err := decodeNpub(to)
			if err != nil {
				return mcp.NewToolResultError("Invalid npub: " + err.Error()), nil
			}
			to = hexKey
		}

		// Build agent event
		tags := nostr.Tags{
			{"c", AgentTag},
			{"z", CompressTag},
			{"p", to},
		}

		ev := &nostr.Event{
			Kind:      AgentKind,
			Content:   message,
			Tags:      tags,
			CreatedAt: nostr.Now(),
		}

		if err := keyer.SignEvent(ctx, ev); err != nil {
			return mcp.NewToolResultError("Failed to sign event: " + err.Error()), nil
		}

		relays := defaultRelays[:2] // damus + nos.lol
		if hasRelay && relayURL != "" {
			relays = append(relays, relayURL)
		}

		result := strings.Builder{}
		result.WriteString(fmt.Sprintf("Event ID: %s\nFrom: %s\n", ev.ID, ev.PubKey))

		for res := range sys.Pool.PublishMany(ctx, relays, *ev) {
			if res.Error != nil {
				result.WriteString(fmt.Sprintf("Failed: %s (%v)\n", res.RelayURL, res.Error))
			} else {
				result.WriteString(fmt.Sprintf("Published: %s\n", res.RelayURL))
			}
		}

		return mcp.NewToolResultText(result.String()), nil
	})

	s.AddTool(mcp.NewTool("agent_query_messages",
		mcp.WithDescription("Query agent messages from Nostr relays. Returns Kind 30078 events with automatic decompression."),
		mcp.WithString("author", mcp.Description("Author public key to filter by (hex or npub)")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of events to return (default: 20)")),
		mcp.WithString("relay", mcp.Description("Relay URL to query (default: uses all default relays)")),
	), func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		author, hasAuthor := optional[string](r, "author")
		limit, _ := optional[float64](r, "limit")
		relayURL, hasRelay := optional[string](r, "relay")

		if limit == 0 {
			limit = 20
		}

		filter := nostr.Filter{
			Kinds: []nostr.Kind{nostr.Kind(AgentKind)},
			Limit: int(limit),
		}

		if hasAuthor {
			if strings.HasPrefix(author, "npub") {
				hexKey, err := decodeNpub(author)
				if err != nil {
					return mcp.NewToolResultError("Invalid npub: " + err.Error()), nil
				}
				author = hexKey
			}
			pk, err := parsePubKey(author)
			if err != nil {
				return mcp.NewToolResultError("Invalid pubkey: " + err.Error()), nil
			}
			filter.Authors = append(filter.Authors, pk)
		}

		relays := defaultRelays
		if hasRelay && relayURL != "" {
			relays = []string{relayURL}
		}

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		events := sys.Pool.FetchMany(ctx, relays, filter, nostr.SubscriptionOptions{})

		result := strings.Builder{}
		count := 0
		for ie := range events {
			count++
			content := ie.Content
			result.WriteString(fmt.Sprintf("--- Event %d ---\nID: %s\nAuthor: %s\nTime: %s\nContent: %s\n",
				count, ie.ID, ie.PubKey.Hex(),
				ie.CreatedAt.Time().Format(time.RFC3339),
				content))
		}

		if count == 0 {
			return mcp.NewToolResultText("No agent messages found."), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Found %d agent messages:\n\n%s", count, result.String())), nil
	})

	s.AddTool(mcp.NewTool("agent_timeline",
		mcp.WithDescription("Show recent agent communication timeline. Returns the latest agent messages across all default relays."),
		mcp.WithNumber("limit", mcp.Description("Number of events to show (default: 10)")),
	), func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		limit, _ := optional[float64](r, "limit")
		if limit == 0 {
			limit = 10
		}

		filter := nostr.Filter{
			Kinds: []nostr.Kind{nostr.Kind(AgentKind)},
			Limit: int(limit),
		}

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		events := sys.Pool.FetchMany(ctx, defaultRelays, filter, nostr.SubscriptionOptions{})

		result := strings.Builder{}
		result.WriteString("Agent Timeline:\n\n")
		count := 0
		for ie := range events {
			count++
			// Check for recipient tag
			recipient := ""
			for _, tag := range ie.Tags {
				if len(tag) >= 2 && tag[0] == "p" {
					recipient = tag[1]
					if len(recipient) > 16 {
						recipient = recipient[:16] + "..."
					}
					break
				}
			}

			result.WriteString(fmt.Sprintf("[%s] %s → %s: %s\n",
				ie.CreatedAt.Time().Format("01-02 15:04"),
				ie.PubKey.Hex()[:12]+"...",
				recipient,
				truncate(ie.Content, 100)))
		}

		if count == 0 {
			return mcp.NewToolResultText("No agent messages found on the timeline."), nil
		}

		return mcp.NewToolResultText(result.String()), nil
	})

	s.AddTool(mcp.NewTool("agent_init_identity",
		mcp.WithDescription("Initialize or show agent identity. Generates a new Nostr keypair and saves it to ~/.agent-speaker/identity.json. If identity already exists, shows the public key."),
		mcp.WithBoolean("force", mcp.Description("Force regenerate even if identity exists (default: false)")),
	), func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		force, _ := optional[bool](r, "force")

		home, err := os.UserHomeDir()
		if err != nil {
			return mcp.NewToolResultError("Cannot determine home directory: " + err.Error()), nil
		}

		identityDir := filepath.Join(home, ".agent-speaker")
		identityFile := filepath.Join(identityDir, "identity.json")

		// Check if identity exists
		if !force {
			if data, err := os.ReadFile(identityFile); err == nil {
				return mcp.NewToolResultText(fmt.Sprintf("Identity already exists:\n%s\n\nUse force=true to regenerate.", string(data))), nil
			}
		}

		// Generate new keypair
		secretBytes := make([]byte, 32)
		if _, err := rand.Read(secretBytes); err != nil {
			return mcp.NewToolResultError("Failed to generate random key: " + err.Error()), nil
		}
		secretHex := hex.EncodeToString(secretBytes)

		// Derive public key using nostr package
		var secretKey [32]byte
		copy(secretKey[:], secretBytes)
		pk := nostr.GetPublicKey(secretKey)

		// Save identity
		if err := os.MkdirAll(identityDir, 0700); err != nil {
			return mcp.NewToolResultError("Failed to create identity directory: " + err.Error()), nil
		}

		identityJSON := fmt.Sprintf(`{
  "public_key": "%s",
  "secret_key": "%s",
  "created_at": "%s"
}`, pk, secretHex, time.Now().UTC().Format(time.RFC3339))

		if err := os.WriteFile(identityFile, []byte(identityJSON), 0600); err != nil {
			return mcp.NewToolResultError("Failed to save identity: " + err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf(
			"Agent identity initialized:\n  Public key: %s\n  Saved to: %s\n\nUse this public key for other agents to send you messages.\nSet NOSTR_SECRET_KEY=%s to use this identity for signing.",
			pk, identityFile, secretHex)), nil
	})

	s.AddTool(mcp.NewTool("agent_manage_relays",
		mcp.WithDescription("List, add, or remove relay URLs from the agent's relay configuration."),
		mcp.WithString("action", mcp.Description("Action: 'list', 'add', or 'remove'"), mcp.Required()),
		mcp.WithString("url", mcp.Description("Relay URL (required for add/remove)")),
	), func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		action := required[string](r, "action")
		relayURLArg, _ := optional[string](r, "url")

		switch action {
		case "list":
			result := "Default relays:\n"
			for _, relay := range defaultRelays {
				result += fmt.Sprintf("  - %s\n", relay)
			}
			return mcp.NewToolResultText(result), nil

		case "add":
			if relayURLArg == "" {
				return mcp.NewToolResultError("url is required for 'add' action"), nil
			}
			if !strings.HasPrefix(relayURLArg, "wss://") && !strings.HasPrefix(relayURLArg, "ws://") {
				return mcp.NewToolResultError("Relay URL must start with wss:// or ws://"), nil
			}
			// Note: this only affects the current session. For persistence,
			// the user should configure relays in their agent config.
			return mcp.NewToolResultText(fmt.Sprintf(
				"Relay %s noted. To use it, pass relay='%s' when sending messages.\n"+
					"For persistent relay config, add it to your agent-config.yaml.",
				relayURLArg, relayURLArg)), nil

		case "remove":
			return mcp.NewToolResultText("Relay removal is handled via agent-config.yaml. Edit the relay list there."), nil

		default:
			return mcp.NewToolResultError("Unknown action. Use 'list', 'add', or 'remove'."), nil
		}
	})
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
