package identity

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/iDoris-ai/hyphae/internal/audit"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/urfave/cli/v3"
)

// IdentityCmd manages local identities (nicknames)
var IdentityCmd = &cli.Command{
	Name:  "identity",
	Usage: "Manage local identities",
	Description: `Create and manage local identities with secure key storage.
Identities are stored in ~/.hyphae/ with 600 permissions.`,
	Commands: []*cli.Command{
		checkPasswordCommand(),
		{
			Name:  "create",
			Usage: "Create a new identity",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "nickname",
					Aliases: []string{"n"},
					Usage:   "Nickname for this identity",
				},
				&cli.BoolFlag{
					Name:    "default",
					Aliases: []string{"d"},
					Usage:   "Set as default identity",
				},
				&cli.StringFlag{
					Name:    "password",
					Aliases: []string{"p"},
					Usage:   "Password to encrypt keystore (recommended)",
				},
				&cli.BoolFlag{
					Name:  "password-stdin",
					Usage: "Read the keystore password from stdin",
				},
				&cli.BoolFlag{
					Name:  "password-prompt",
					Usage: "Prompt for password interactively",
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				passwordProvided := c.IsSet("password")
				passwordStdin := c.Bool("password-stdin")
				passwordPrompt := c.Bool("password-prompt")
				selectedPasswordMethods := 0
				for _, selected := range []bool{passwordProvided, passwordStdin, passwordPrompt} {
					if selected {
						selectedPasswordMethods++
					}
				}
				if selectedPasswordMethods > 1 {
					return common.NewExitError(common.ErrCodeUser, fmt.Errorf("choose only one of --password, --password-stdin, or --password-prompt"))
				}
				if passwordPrompt && common.JSONMode(c) {
					return common.NewExitError(common.ErrCodeAuth, fmt.Errorf("--password-prompt is unavailable in JSON mode; use --password-stdin"))
				}

				nickname := c.String("nickname")
				if nickname == "" {
					return common.NewExitError(common.ErrCodeUser, fmt.Errorf("--nickname is required"))
				}

				password := c.String("password")
				if passwordStdin {
					var err error
					password, err = readPasswordStdin(os.Stdin)
					if err != nil {
						return common.NewExitError(common.ErrCodeAuth, err)
					}
				}

				ks, err := LoadKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				var identity *types.Identity

				if ks.Encrypted {
					if password == "" {
						if common.JSONMode(c) {
							return common.NewExitError(common.ErrCodeAuth, fmt.Errorf("encrypted keystore requires --password-stdin or --password in JSON mode (prefer --password-stdin)"))
						}
						password, err = PromptPassword("Keystore password: ")
						if err != nil {
							return fmt.Errorf("failed to read password: %w", err)
						}
					}
					if err := UnlockKeyStore(ks, password); err != nil {
						return common.NewExitError(common.ErrCodeAuth, fmt.Errorf("failed to unlock keystore: %w", err))
					}
					identity, err = CreateIdentityWithPassword(ks, nickname, password)
					if err != nil {
						return err
					}
				} else if password != "" || c.Bool("password-prompt") {
					if len(ks.Identities) > 0 {
						return common.NewExitError(common.ErrCodeUser, fmt.Errorf("encrypt the existing keystore with 'identity change-password' before creating another identity with a password"))
					}
					if password == "" {
						password, err = PromptPasswordWithConfirm()
						if err != nil {
							return fmt.Errorf("failed to set password: %w", err)
						}
					}
					identity, err = CreateIdentityWithPassword(ks, nickname, password)
					if err != nil {
						return err
					}
				} else {
					identity, err = CreateIdentity(ks, nickname)
					if err != nil {
						return err
					}
				}

				if c.Bool("default") {
					if err := SetDefault(ks, nickname); err != nil {
						return err
					}
				}

				green := color.New(color.FgGreen).SprintFunc()
				yellow := color.New(color.FgYellow).SprintFunc()
				cyan := color.New(color.FgCyan).SprintFunc()

				if err := audit.LogAction(nickname, audit.ActionIdentityCreated, map[string]any{"npub": identity.Npub}); err != nil {
					fmt.Fprintf(os.Stderr, "⚠️  audit log failed: %v\n", err)
				}
				type identityOutput struct {
					Nickname  string `json:"nickname"`
					Npub      string `json:"npub"`
					Default   bool   `json:"default"`
					Encrypted bool   `json:"encrypted"`
				}
				common.Emit(common.JSONMode(c), identityOutput{
					Nickname: nickname, Npub: identity.Npub,
					Default: ks.DefaultIdentity == nickname, Encrypted: ks.Encrypted,
				}, func() {
					fmt.Printf("✅ Created identity '%s'\n", green(nickname))
					fmt.Printf("   Npub: %s\n", yellow(identity.Npub))
					fmt.Printf("   Nsec: %s (stored securely)\n", yellow("[hidden]"))
					if ks.Encrypted {
						fmt.Printf("   Encryption: %s\n", cyan("AES-256-GCM + scrypt"))
					}
					fmt.Printf("\n🔐 Keys stored in ~/.hyphae/ (permissions: 600)\n")
				})

				return nil
			},
		},
		{
			Name:  "list",
			Usage: "List all identities",
			Action: func(ctx context.Context, c *cli.Command) error {
				jsonMode := common.JSONMode(c)

				ks, err := LoadKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				identities := ListIdentities(ks)

				// Never serialize types.Identity directly for --json: it carries Nsec.
				// Build a redacted view with only what human mode already prints.
				type identityEntry struct {
					Nickname  string `json:"nickname"`
					Npub      string `json:"npub"`
					Default   bool   `json:"default"`
					Encrypted bool   `json:"encrypted"`
				}
				entries := make([]identityEntry, 0, len(identities))
				for _, identity := range identities {
					entries = append(entries, identityEntry{
						Nickname:  identity.Nickname,
						Npub:      identity.Npub,
						Default:   identity.Nickname == ks.DefaultIdentity,
						Encrypted: ks.Encrypted,
					})
				}

				common.Emit(jsonMode, entries, func() {
					if len(identities) == 0 {
						fmt.Println("No identities found. Create one with:")
						fmt.Println("  hyphae identity create --nickname <name>")
						return
					}

					fmt.Println("👤 Identities:")
					w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
					fmt.Fprintln(w, "NICKNAME\tNPUB\tDEFAULT\tENCRYPTED")

					for _, e := range entries {
						defaultMark := ""
						if e.Default {
							defaultMark = "✓"
						}
						npubShort := e.Npub[:20] + "..."
						encryptedMark := ""
						if e.Encrypted {
							encryptedMark = "🔐"
						}
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Nickname, npubShort, defaultMark, encryptedMark)
					}
					w.Flush()
				})

				return nil
			},
		},
		{
			Name:  "use",
			Usage: "Set default identity",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "nickname",
					Aliases: []string{"n"},
					Usage:   "Nickname to set as default",
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				jsonMode := common.JSONMode(c)
				nickname := c.String("nickname")
				if nickname == "" {
					return common.NewExitError(common.ErrCodeUser, fmt.Errorf("--nickname is required"))
				}
				ks, err := LoadKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				if _, exists := ks.Identities[nickname]; !exists {
					return common.NewExitError(common.ErrCodeUser, fmt.Errorf("identity '%s' not found", nickname))
				}
				if err := SetDefault(ks, nickname); err != nil {
					return err
				}

				common.Emit(jsonMode, map[string]any{
					"nickname":  nickname,
					"npub":      ks.Identities[nickname].Npub,
					"default":   true,
					"encrypted": ks.Encrypted,
				}, func() { fmt.Printf("✅ Default identity set to '%s'\n", nickname) })
				return nil
			},
		},
		{
			Name:  "export",
			Usage: "Export identity nsec (be careful!)",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "nickname",
					Aliases: []string{"n"},
					Usage:   "Nickname to export",
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				ks, err := LoadAndUnlockKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				nickname := c.String("nickname")
				identity, err := GetIdentity(ks, nickname)
				if err != nil {
					return err
				}

				nsec := identity.Nsec
				if ks.Encrypted {
					dnsec, err := decryptWithKey(identity.Nsec, *ks.MasterKey)
					if err != nil {
						return fmt.Errorf("failed to decrypt nsec: %w", err)
					}
					nsec = dnsec
				}

				red := color.New(color.FgRed).SprintFunc()
				fmt.Println(red("⚠️  WARNING: You are about to expose your private key!"))
				fmt.Println(red("   Never share this with anyone or store it insecurely."))
				fmt.Println()
				fmt.Printf("Identity: %s\n", identity.Nickname)
				fmt.Printf("Npub:     %s\n", identity.Npub)
				fmt.Printf("Nsec:     %s\n", nsec)

				return nil
			},
		},
		{
			Name:  "change-password",
			Usage: "Change keystore password",
			Action: func(ctx context.Context, c *cli.Command) error {
				ks, err := LoadKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				if !ks.Encrypted {
					// Encrypt an unencrypted keystore
					pw, err := PromptPasswordWithConfirm()
					if err != nil {
						return fmt.Errorf("failed to set password: %w", err)
					}
					if err := EncryptKeyStore(ks, pw); err != nil {
						return err
					}
					fmt.Println("✅ Keystore encrypted successfully")
					return nil
				}

				oldPw, err := PromptPassword("Current password: ")
				if err != nil {
					return fmt.Errorf("failed to read password: %w", err)
				}
				newPw, err := PromptPasswordWithConfirm()
				if err != nil {
					return fmt.Errorf("failed to set new password: %w", err)
				}
				if err := ChangePassword(ks, oldPw, newPw); err != nil {
					return fmt.Errorf("failed to change password: %w", err)
				}
				fmt.Println("✅ Password changed successfully")
				return nil
			},
		},
	},
}

// ContactCmd manages contacts (other people's nicknames)
var ContactCmd = &cli.Command{
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
					Name:    "nickname",
					Aliases: []string{"n"},
					Usage:   "Nickname for this contact",
				},
				&cli.StringFlag{
					Name:    "npub",
					Aliases: []string{"p"},
					Usage:   "Contact's npub",
				},
				&cli.StringFlag{
					Name:  "role",
					Usage: "Contact's role: human or agent (default human)",
					Value: string(types.RoleHuman),
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				jsonMode := common.JSONMode(c)
				nickname := c.String("nickname")
				npub := c.String("npub")
				if nickname == "" || npub == "" {
					return common.NewExitError(common.ErrCodeUser, fmt.Errorf("--nickname and --npub are required"))
				}
				if _, err := common.ParsePublicKey(npub); err != nil {
					return common.NewExitError(common.ErrCodeUser, fmt.Errorf("--npub must be a valid public key"))
				}
				ks, err := LoadKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				role := types.Role(c.String("role"))
				if role != types.RoleHuman && role != types.RoleAgent {
					return common.NewExitError(common.ErrCodeUser, fmt.Errorf("invalid --role %q: must be %q or %q", role, types.RoleHuman, types.RoleAgent))
				}

				if err := AddContactWithRole(ks, nickname, npub, role); err != nil {
					return err
				}

				actor := ks.DefaultIdentity
				if actor == "" {
					actor = "unknown"
				}
				if err := audit.LogAction(actor, audit.ActionContactAdded, map[string]any{"nickname": nickname, "npub": ks.Contacts[nickname].Npub}); err != nil {
					fmt.Fprintf(os.Stderr, "⚠️  audit log failed: %v\n", err)
				}

				green := color.New(color.FgGreen).SprintFunc()
				yellow := color.New(color.FgYellow).SprintFunc()

				common.Emit(jsonMode, map[string]string{
					"nickname": nickname,
					"npub":     ks.Contacts[nickname].Npub,
					"role":     role.String(),
				}, func() {
					fmt.Printf("✅ Added contact '%s'\n", green(nickname))
					fmt.Printf("   Npub: %s\n", yellow(npub))
				})

				return nil
			},
		},
		{
			Name:  "list",
			Usage: "List all contacts",
			Action: func(ctx context.Context, c *cli.Command) error {
				jsonMode := common.JSONMode(c)
				ks, err := LoadKeyStore()
				if err != nil {
					return fmt.Errorf("failed to load keystore: %w", err)
				}

				contacts := ListContacts(ks)
				type contactEntry struct {
					Nickname string `json:"nickname"`
					Npub     string `json:"npub"`
					Role     string `json:"role"`
				}
				entries := make([]contactEntry, 0, len(contacts))
				for _, contact := range contacts {
					entries = append(entries, contactEntry{
						Nickname: contact.Nickname,
						Npub:     contact.Npub,
						Role:     contact.Role.String(),
					})
				}

				common.Emit(jsonMode, entries, func() {
					if len(contacts) == 0 {
						fmt.Println("No contacts found. Add one with:")
						fmt.Println("  hyphae contact add --nickname <name> --npub <npub>")
						return
					}

					fmt.Println("📇 Contacts:")
					w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
					fmt.Fprintln(w, "NICKNAME\tNPUB\tROLE")

					for _, contact := range contacts {
						npubShort := contact.Npub[:20] + "..."
						fmt.Fprintf(w, "%s\t%s\t%s\n", contact.Nickname, npubShort, contact.Role.String())
					}
					w.Flush()
				})

				return nil
			},
		},
	},
}
