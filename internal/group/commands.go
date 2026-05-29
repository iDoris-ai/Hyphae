package group

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/urfave/cli/v3"
)

// GroupCmd manages group chat
var GroupCmd = &cli.Command{
	Name:  "group",
	Usage: "Manage group chats",
	Description: `Create and manage group chats with multiple participants.
Example: agent-speaker group create --name "Dev Team" --members bob,charlie`,
	Commands: []*cli.Command{
		GroupCreateCmd,
		GroupListCmd,
		GroupAddCmd,
		GroupRemoveCmd,
		GroupLeaveCmd,
		GroupDeleteCmd,
		GroupChatCmd,
	},
}

// GroupCreateCmd creates a new group
var GroupCreateCmd = &cli.Command{
	Name:  "create",
	Usage: "Create a new group",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "name",
			Aliases:  []string{"n"},
			Usage:    "Group name",
			Required: true,
		},
		&cli.StringFlag{
			Name:    "description",
			Aliases: []string{"d"},
			Usage:   "Group description",
		},
		&cli.StringSliceFlag{
			Name:     "members",
			Aliases:  []string{"m"},
			Usage:    "Initial members (nicknames, comma-separated)",
			Required: true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		// Load current identity
		ks, err := identity.LoadKeyStore()
		if err != nil {
			return err
		}

		myIdentity, err := identity.GetIdentity(ks, "")
		if err != nil {
			return err
		}

		// Resolve member nicknames to npubs
		var memberNpubs []string
		memberNpubs = append(memberNpubs, myIdentity.Npub) // Add creator

		for _, nick := range c.StringSlice("members") {
			npub, err := identity.ResolveRecipient(ks, nick)
			if err != nil {
				return fmt.Errorf("failed to resolve member '%s': %w", nick, err)
			}
			// Avoid duplicates
			found := false
			for _, m := range memberNpubs {
				if m == npub {
					found = true
					break
				}
			}
			if !found {
				memberNpubs = append(memberNpubs, npub)
			}
		}

		// Create group
		db, err := NewDB()
		if err != nil {
			return err
		}

		group, err := db.CreateGroup(
			c.String("name"),
			c.String("description"),
			myIdentity.Npub,
			memberNpubs,
		)
		if err != nil {
			return err
		}

		green := color.New(color.FgGreen).SprintFunc()
		yellow := color.New(color.FgYellow).SprintFunc()

		fmt.Printf("✅ Created group '%s'\n", green(group.Name))
		fmt.Printf("   ID: %s\n", yellow(group.ID))
		fmt.Printf("   Members: %d\n", len(group.Members))
		fmt.Printf("\n💡 Use 'agent-speaker group chat --name \"%s\"' to start chatting\n", group.Name)

		return nil
	},
}

// GroupListCmd lists all groups
var GroupListCmd = &cli.Command{
	Name:  "list",
	Usage: "List all your groups",
	Action: func(ctx context.Context, c *cli.Command) error {
		ks, err := identity.LoadKeyStore()
		if err != nil {
			return err
		}

		myIdentity, err := identity.GetIdentity(ks, "")
		if err != nil {
			return err
		}

		db, err := NewDB()
		if err != nil {
			return err
		}

		groups, err := db.GetGroupsForUser(myIdentity.Npub)
		if err != nil {
			return err
		}

		if len(groups) == 0 {
			fmt.Println("No groups found. Create one with:")
			fmt.Println("  agent-speaker group create --name <name> --members <nicknames>")
			return nil
		}

		fmt.Println("👥 Your Groups:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tMEMBERS\tCREATED")

		for _, g := range groups {
			created := ""
			if g.CreatedAt > 0 {
				created = "✓"
			}
			fmt.Fprintf(w, "%s\t%d\t%s\n", g.Name, len(g.Members), created)
		}
		w.Flush()

		return nil
	},
}

// GroupAddCmd adds a member to a group
var GroupAddCmd = &cli.Command{
	Name:  "add-member",
	Usage: "Add a member to a group",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "name",
			Aliases:  []string{"n"},
			Usage:    "Group name",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "user",
			Aliases:  []string{"u"},
			Usage:    "User nickname to add",
			Required: true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		// Find group by name
		ks, err := identity.LoadKeyStore()
		if err != nil {
			return err
		}

		myIdentity, err := identity.GetIdentity(ks, "")
		if err != nil {
			return err
		}

		db, err := NewDB()
		if err != nil {
			return err
		}

		groups, err := db.GetGroupsForUser(myIdentity.Npub)
		if err != nil {
			return err
		}

		var groupID string
		for _, g := range groups {
			if g.Name == c.String("name") {
				groupID = g.ID
				break
			}
		}

		if groupID == "" {
			return fmt.Errorf("group '%s' not found", c.String("name"))
		}

		// Resolve user
		npub, err := identity.ResolveRecipient(ks, c.String("user"))
		if err != nil {
			return err
		}

		// Add member
		if err := db.AddMember(groupID, npub); err != nil {
			return err
		}

		fmt.Printf("✅ Added %s to group '%s'\n", c.String("user"), c.String("name"))
		return nil
	},
}

// GroupRemoveCmd removes a member from a group
var GroupRemoveCmd = &cli.Command{
	Name:  "remove-member",
	Usage: "Remove a member from a group",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "name",
			Aliases:  []string{"n"},
			Usage:    "Group name",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "user",
			Aliases:  []string{"u"},
			Usage:    "User nickname to remove",
			Required: true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		// Similar to add-member but removes
		fmt.Println("⚠️  Remove member functionality - not yet implemented")
		return nil
	},
}

// GroupLeaveCmd leaves a group
var GroupLeaveCmd = &cli.Command{
	Name:  "leave",
	Usage: "Leave a group",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "name",
			Aliases:  []string{"n"},
			Usage:    "Group name",
			Required: true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		ks, err := identity.LoadKeyStore()
		if err != nil {
			return err
		}

		myIdentity, err := identity.GetIdentity(ks, "")
		if err != nil {
			return err
		}

		db, err := NewDB()
		if err != nil {
			return err
		}

		groups, err := db.GetGroupsForUser(myIdentity.Npub)
		if err != nil {
			return err
		}

		var groupID string
		for _, g := range groups {
			if g.Name == c.String("name") {
				groupID = g.ID
				break
			}
		}

		if groupID == "" {
			return fmt.Errorf("group '%s' not found", c.String("name"))
		}

		if err := db.RemoveMember(groupID, myIdentity.Npub); err != nil {
			return err
		}

		fmt.Printf("👋 You left group '%s'\n", c.String("name"))
		return nil
	},
}

// GroupDeleteCmd deletes a group
var GroupDeleteCmd = &cli.Command{
	Name:  "delete",
	Usage: "Delete a group (creator only)",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "name",
			Aliases:  []string{"n"},
			Usage:    "Group name",
			Required: true,
		},
		&cli.BoolFlag{
			Name:    "force",
			Aliases: []string{"f"},
			Usage:   "Force deletion without confirmation",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		// Implementation similar to leave but deletes group
		fmt.Println("⚠️  Delete group functionality - not yet implemented")
		return nil
	},
}

// GroupChatCmd starts a group chat (placeholder for TUI)
var GroupChatCmd = &cli.Command{
	Name:  "chat",
	Usage: "Start chatting in a group (TUI)",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "name",
			Aliases:  []string{"n"},
			Usage:    "Group name",
			Required: true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		// This would integrate with TUI
		fmt.Printf("💬 Starting group chat: %s\n", c.String("name"))
		fmt.Println("🚧 TUI group chat - coming soon!")
		fmt.Println()
		fmt.Println("For now, use regular messaging to group members")
		return nil
	},
}
