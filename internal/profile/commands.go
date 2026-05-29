package profile

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"fiatjaf.com/nostr"
	"github.com/fatih/color"
	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/urfave/cli/v3"
)

// ProfileCmd is the main profile command
var ProfileCmd = &cli.Command{
	Name:  "profile",
	Usage: "Manage agent profiles",
	Commands: []*cli.Command{
		profilePublishCmd,
		profileShowCmd,
		profileListCmd,
		profileSearchCmd,
		profileDiscoverCmd,
	},
}

var profilePublishCmd = &cli.Command{
	Name:  "publish",
	Usage: "Publish your agent profile to nostr relays",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "as",
			Aliases: []string{"a"},
			Usage:   "Your nickname",
		},
		&cli.StringFlag{
			Name:    "name",
			Aliases: []string{"n"},
			Usage:   "Agent display name",
		},
		&cli.StringFlag{
			Name:    "description",
			Aliases: []string{"d"},
			Usage:   "Agent description",
		},
		&cli.StringSliceFlag{
			Name:  "capability",
			Usage: "Capabilities in format 'name:description'",
		},
		&cli.StringFlag{
			Name:  "availability",
			Usage: "Availability status (available, busy, away, offline)",
			Value: types.AvailabilityAvailable,
		},
		&cli.StringSliceFlag{
			Name:  "rate",
			Usage: "Rates in format 'service:unit:price:description'",
		},
		&cli.StringFlag{
			Name:  "currency",
			Usage: "Currency for rates",
			Value: "USD",
		},
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
			Usage:   "Relay URLs",
			Value:   []string{"wss://relay.aastar.io"},
		},
		&cli.StringFlag{
			Name:  "json-file",
			Usage: "Load profile from JSON file",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		ks, err := identity.LoadKeyStore()
		if err != nil {
			return fmt.Errorf("failed to load keystore: %w", err)
		}

		agent, err := identity.GetIdentity(ks, c.String("as"))
		if err != nil {
			return err
		}

		sk, err := identity.GetSecretKey(ks, agent.Nickname)
		if err != nil {
			return err
		}
		pk := sk.Public()

		var profile *types.AgentProfile

		// Load from JSON file if provided
		jsonFile := c.String("json-file")
		if jsonFile != "" {
			data, err := os.ReadFile(jsonFile)
			if err != nil {
				return fmt.Errorf("failed to read JSON file: %w", err)
			}
			profile, err = types.AgentProfileFromJSON(data)
			if err != nil {
				return err
			}
		} else {
			// Build profile from flags
			name := c.String("name")
			if name == "" {
				name = agent.Nickname
			}

			profile = &types.AgentProfile{
				Name:         name,
				Description:  c.String("description"),
				Availability: c.String("availability"),
				Version:      "1.0",
				Capabilities: make([]types.Capability, 0),
				RateSheet: &types.RateSheet{
					Currency: c.String("currency"),
					Rates:    make([]types.RateEntry, 0),
				},
			}

			// Parse capabilities
			for _, capStr := range c.StringSlice("capability") {
				cap := parseCapability(capStr)
				profile.Capabilities = append(profile.Capabilities, cap)
			}

			// Parse rates
			for _, rateStr := range c.StringSlice("rate") {
				entry := parseRateEntry(rateStr)
				if entry.Service != "" {
					profile.RateSheet.Rates = append(profile.RateSheet.Rates, entry)
				}
			}
		}

		if err := profile.Validate(); err != nil {
			return err
		}

		profile.UpdatedAt = int64(nostr.Now())

		// Create event
		event, err := ProfileToEvent(profile, pk)
		if err != nil {
			return err
		}

		if err := event.Sign(sk); err != nil {
			return fmt.Errorf("failed to sign event: %w", err)
		}

		// Publish
		relays := c.StringSlice("relay")
		success := 0
		for _, url := range relays {
			relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
			if err != nil {
				fmt.Printf("   ❌ %s: connect failed: %v\n", url, err)
				continue
			}

			pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = relay.Publish(pubCtx, *event)
			cancel()
			relay.Close()

			if err != nil {
				fmt.Printf("   ❌ %s: publish failed: %v\n", url, err)
			} else {
				fmt.Printf("   ✅ %s\n", url)
				success++
			}
		}

		// Store locally
		if success > 0 {
			db, err := NewDB()
			if err == nil {
				npub := common.EncodeNpub(pk)
				_ = db.StoreProfile(npub, profile)
				db.Close()
			}
		}

		fmt.Printf("📤 Profile '%s' published to %d/%d relays\n", profile.Name, success, len(relays))
		return nil
	},
}

var profileShowCmd = &cli.Command{
	Name:  "show",
	Usage: "Show your or another agent's profile",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "as",
			Aliases: []string{"a"},
			Usage:   "Your nickname (for showing your own profile)",
		},
		&cli.StringFlag{
			Name:    "npub",
			Aliases: []string{"p"},
			Usage:   "Npub to look up (for showing another agent's profile)",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		npub := c.String("npub")

		if npub == "" {
			ks, err := identity.LoadKeyStore()
			if err != nil {
				return fmt.Errorf("failed to load keystore: %w", err)
			}
			agent, err := identity.GetIdentity(ks, c.String("as"))
			if err != nil {
				return err
			}
			npub = agent.Npub
		}

		db, err := NewDB()
		if err != nil {
			return err
		}
		defer db.Close()

		profile, err := db.GetProfile(npub)
		if err != nil {
			return err
		}
		if profile == nil {
			fmt.Println("No profile found locally.")
			fmt.Println("Use 'profile discover --npub <npub>' to fetch from relays.")
			return nil
		}

		printProfile(npub, profile)
		return nil
	},
}

var profileListCmd = &cli.Command{
	Name:  "list",
	Usage: "List all locally stored agent profiles",
	Action: func(ctx context.Context, c *cli.Command) error {
		db, err := NewDB()
		if err != nil {
			return err
		}
		defer db.Close()

		profiles, err := db.ListProfiles()
		if err != nil {
			return err
		}
		if len(profiles) == 0 {
			fmt.Println("No profiles found locally.")
			return nil
		}

		fmt.Println("👤 Agent Profiles:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tAVAILABILITY\tNPUB")
		for _, p := range profiles {
			npubShort := p.Npub[:20] + "..."
			fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, p.Availability, npubShort)
		}
		w.Flush()

		return nil
	},
}

var profileSearchCmd = &cli.Command{
	Name:  "search",
	Usage: "Search locally stored profiles",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "query",
			Aliases:  []string{"q"},
			Usage:    "Search query",
			Required: true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		db, err := NewDB()
		if err != nil {
			return err
		}
		defer db.Close()

		profiles, err := db.SearchProfiles(c.String("query"))
		if err != nil {
			return err
		}
		if len(profiles) == 0 {
			fmt.Println("No profiles matching your query.")
			return nil
		}

		fmt.Printf("🔍 Found %d profile(s):\n", len(profiles))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tAVAILABILITY\tNPUB")
		for _, p := range profiles {
			npubShort := p.Npub[:20] + "..."
			fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, p.Availability, npubShort)
		}
		w.Flush()

		return nil
	},
}

var profileDiscoverCmd = &cli.Command{
	Name:  "discover",
	Usage: "Discover agent profiles from nostr relays",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "npub",
			Aliases: []string{"p"},
			Usage:   "Specific npub to discover",
		},
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
			Usage:   "Relay URLs",
			Value:   []string{"wss://relay.aastar.io"},
		},
		&cli.IntFlag{
			Name:  "limit",
			Usage: "Maximum profiles to fetch",
			Value: 10,
		},
		&cli.IntFlag{
			Name:  "timeout",
			Usage: "Timeout in seconds",
			Value: 5,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		npub := c.String("npub")
		relays := c.StringSlice("relay")
		limit := int(c.Int("limit"))
		timeoutSec := time.Duration(c.Int("timeout")) * time.Second

		var authors []nostr.PubKey
		if npub != "" {
			pk, err := common.ParsePublicKey(npub)
			if err != nil {
				return fmt.Errorf("invalid npub: %w", err)
			}
			authors = append(authors, pk)
		}

		filter := BuildFilter(authors, limit)

		db, err := NewDB()
		if err != nil {
			return err
		}
		defer db.Close()

		found := 0
		for _, url := range relays {
			relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
			if err != nil {
				fmt.Printf("   ⚠️  Failed to connect to %s: %v\n", url, err)
				continue
			}

			sub, err := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
			if err != nil {
				relay.Close()
				fmt.Printf("   ⚠️  Failed to subscribe on %s: %v\n", url, err)
				continue
			}

			timeout := time.AfterFunc(timeoutSec, func() { sub.Unsub() })
			for evt := range sub.Events {
				profile, err := EventToProfile(&evt)
				if err != nil {
					continue
				}

				evtNpub := common.EncodeNpub(evt.PubKey)
				if err := db.StoreProfile(evtNpub, profile); err == nil {
					found++
					fmt.Printf("   ✅ Found: %s (%s)\n", profile.Name, evtNpub[:20]+"...")
				}
			}
			timeout.Stop()
			relay.Close()
		}

		if found == 0 {
			fmt.Println("No profiles found on relays.")
		} else {
			fmt.Printf("\n🎉 Discovered %d profile(s)\n", found)
		}

		return nil
	},
}

// parseCapability parses a capability string "name:description"
func parseCapability(s string) types.Capability {
	var cap types.Capability
	for i, ch := range s {
		if ch == ':' {
			cap.Name = s[:i]
			cap.Description = s[i+1:]
			break
		}
	}
	if cap.Name == "" {
		cap.Name = s
	}
	return cap
}

// parseRateEntry parses a rate string "service:unit:price:description"
func parseRateEntry(s string) types.RateEntry {
	var entry types.RateEntry
	parts := splitRateString(s)
	switch len(parts) {
	case 1:
		entry.Service = parts[0]
	case 2:
		entry.Service = parts[0]
		entry.Unit = parts[1]
	case 3:
		entry.Service = parts[0]
		entry.Unit = parts[1]
		fmt.Sscanf(parts[2], "%f", &entry.Price)
	default:
		entry.Service = parts[0]
		entry.Unit = parts[1]
		fmt.Sscanf(parts[2], "%f", &entry.Price)
		entry.Description = parts[3]
	}
	return entry
}

// splitRateString splits by colon but handles empty parts
func splitRateString(s string) []string {
	var parts []string
	start := 0
	for i, ch := range s {
		if ch == ':' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// printProfile prints a profile in a human-readable format
func printProfile(npub string, profile *types.AgentProfile) {
	green := color.New(color.FgGreen).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()

	fmt.Printf("\n%s %s\n", green("👤"), green(profile.Name))
	fmt.Printf("   Npub: %s\n", yellow(npub))
	if profile.Description != "" {
		fmt.Printf("   %s\n", profile.Description)
	}
	fmt.Printf("   Availability: %s\n", cyan(profile.Availability))
	fmt.Printf("   Version: %s\n", profile.Version)

	if len(profile.Capabilities) > 0 {
		fmt.Println("   Capabilities:")
		for _, cap := range profile.Capabilities {
			fmt.Printf("     • %s", cyan(cap.Name))
			if cap.Description != "" {
				fmt.Printf(" - %s", cap.Description)
			}
			fmt.Println()
		}
	}

	if profile.RateSheet != nil && len(profile.RateSheet.Rates) > 0 {
		fmt.Printf("   Rate Sheet (%s):\n", profile.RateSheet.Currency)
		for _, rate := range profile.RateSheet.Rates {
			fmt.Printf("     • %s: %.2f/%s\n", rate.Service, rate.Price, rate.Unit)
		}
	}

	if profile.Contact != nil {
		fmt.Println("   Contact:")
		if profile.Contact.Email != "" {
			fmt.Printf("     • Email: %s\n", profile.Contact.Email)
		}
		if profile.Contact.Website != "" {
			fmt.Printf("     • Website: %s\n", profile.Contact.Website)
		}
		if profile.Contact.Relay != "" {
			fmt.Printf("     • Relay: %s\n", profile.Contact.Relay)
		}
		if profile.Contact.NostrDMs {
			fmt.Println("     • Nostr DMs: enabled")
		}
	}

	fmt.Println()
}
