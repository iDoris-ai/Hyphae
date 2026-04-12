package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/urfave/cli/v3"
)

// identityCmd manages local identities (nicknames)
var identityCmd = &cli.Command{
	Name:  "identity",
	Usage: "Manage local identities",
	Description: `Create and manage local identities with secure key storage.
Identities are stored in ~/.agent-speaker/ with 600 permissions.`,
	Commands: []*cli.Command{
		{
			Name:  "create",
			Usage: "Create a new identity",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "nickname",
					Aliases:  []string{"n"},
					Usage:    "Nickname for this identity",
					Required: true,
				},
				&cli.BoolFlag{
					Name:    "default",
					Aliases: []string{"d"},
					Usage:   "Set as default identity",
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				ks, err := LoadKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				nickname := c.String("nickname")
				identity, err := ks.CreateIdentity(nickname)
				if err != nil {
					return err
				}

				if c.Bool("default") {
					if err := ks.SetDefault(nickname); err != nil {
						return err
					}
				}

				green := color.New(color.FgGreen).SprintFunc()
				yellow := color.New(color.FgYellow).SprintFunc()

				fmt.Printf("✅ Created identity '%s'\n", green(nickname))
				fmt.Printf("   Npub: %s\n", yellow(identity.Npub))
				fmt.Printf("   Nsec: %s (stored securely)\n", yellow("[hidden]"))
				fmt.Printf("\n🔐 Keys stored in ~/.agent-speaker/ (permissions: 600)\n")

				return nil
			},
		},
		{
			Name:  "list",
			Usage: "List all identities",
			Action: func(ctx context.Context, c *cli.Command) error {
				ks, err := LoadKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				identities := ks.ListIdentities()
				if len(identities) == 0 {
					fmt.Println("No identities found. Create one with:")
					fmt.Println("  agent-speaker identity create --nickname <name>")
					return nil
				}

				fmt.Println("👤 Identities:")
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "NICKNAME\tNPUB\tDEFAULT")

				for _, identity := range identities {
					defaultMark := ""
					if identity.Nickname == ks.DefaultIdentity {
						defaultMark = "✓"
					}
					npubShort := identity.Npub[:20] + "..."
					fmt.Fprintf(w, "%s\t%s\t%s\n", identity.Nickname, npubShort, defaultMark)
				}
				w.Flush()

				return nil
			},
		},
		{
			Name:  "use",
			Usage: "Set default identity",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "nickname",
					Aliases:  []string{"n"},
					Usage:    "Nickname to set as default",
					Required: true,
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				ks, err := LoadKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				nickname := c.String("nickname")
				if err := ks.SetDefault(nickname); err != nil {
					return err
				}

				fmt.Printf("✅ Default identity set to '%s'\n", nickname)
				return nil
			},
		},
		{
			Name:  "export",
			Usage: "Export identity nsec (be careful!)",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "nickname",
					Aliases:  []string{"n"},
					Usage:    "Nickname to export",
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				ks, err := LoadKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				nickname := c.String("nickname")
				identity, err := ks.GetIdentity(nickname)
				if err != nil {
					return err
				}

				red := color.New(color.FgRed).SprintFunc()
				fmt.Println(red("⚠️  WARNING: You are about to expose your private key!"))
				fmt.Println(red("   Never share this with anyone or store it insecurely."))
				fmt.Println()
				fmt.Printf("Identity: %s\n", identity.Nickname)
				fmt.Printf("Npub:     %s\n", identity.Npub)
				fmt.Printf("Nsec:     %s\n", identity.Nsec)

				return nil
			},
		},
	},
}

// contactCmd manages contacts (other people's nicknames)
var contactCmd = &cli.Command{
	Name:  "contact",
	Usage: "Manage contacts (other users)",
	Description: `Add and manage contacts using nicknames instead of npubs.
Contacts are stored locally and mapped to their npubs.`,
	Commands: []*cli.Command{
		{
			Name:  "add",
			Usage: "Add a contact",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "nickname",
					Aliases:  []string{"n"},
					Usage:    "Nickname for this contact",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "npub",
					Aliases:  []string{"p"},
					Usage:    "Contact's npub",
					Required: true,
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				ks, err := LoadKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				nickname := c.String("nickname")
				npub := c.String("npub")

				if err := ks.AddContact(nickname, npub); err != nil {
					return err
				}

				green := color.New(color.FgGreen).SprintFunc()
				yellow := color.New(color.FgYellow).SprintFunc()

				fmt.Printf("✅ Added contact '%s'\n", green(nickname))
				fmt.Printf("   Npub: %s\n", yellow(npub))

				return nil
			},
		},
		{
			Name:  "list",
			Usage: "List all contacts",
			Action: func(ctx context.Context, c *cli.Command) error {
				ks, err := LoadKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				contacts := ks.ListContacts()
				if len(contacts) == 0 {
					fmt.Println("No contacts found. Add one with:")
					fmt.Println("  agent-speaker contact add --nickname <name> --npub <npub>")
					return nil
				}

				fmt.Println("📇 Contacts:")
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "NICKNAME\tNPUB")

				for _, contact := range contacts {
					npubShort := contact.Npub[:20] + "..."
					fmt.Fprintf(w, "%s\t%s\n", contact.Nickname, npubShort)
				}
				w.Flush()

				return nil
			},
		},
	},
}
