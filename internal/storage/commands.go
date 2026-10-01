package storage

import (
	"context"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/audit"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/urfave/cli/v3"
)

// StorageCmd provides storage management commands
var StorageCmd = &cli.Command{
	Name:        "storage",
	Usage:       "Manage local storage",
	Description: `View and manage the SQLite message database`,
	Commands: []*cli.Command{
		{
			Name:  "info",
			Usage: "Show storage information",
			Action: func(ctx context.Context, c *cli.Command) error {
				info, err := inspectStorageInfo(ctx)
				if err != nil {
					return err
				}
				common.Emit(common.JSONMode(c), info, func() {
					if !info.Exists {
						fmt.Println("📭 Database not created yet")
						fmt.Printf("   Path: %s\n", info.Path)
						return
					}
					fmt.Println("💾 Storage Information")
					fmt.Println("======================")
					fmt.Printf("Database: %s\n", info.Path)
					fmt.Printf("Size:     %d bytes (%.2f KB)\n", info.SizeBytes, float64(info.SizeBytes)/1024)
					fmt.Printf("Mode:     %s\n", info.Mode)
					fmt.Printf("Messages: %d\n", info.MessageCount)
					fmt.Println("\n📊 Tables:")
					for _, name := range info.Tables {
						fmt.Printf("   - %s\n", name)
					}
				})
				return nil
			},
		},
		{
			Name:  "audit-log",
			Usage: "View or verify the tamper-evident audit log",
			Flags: []cli.Flag{
				&cli.IntFlag{
					Name:  "limit",
					Usage: "Maximum entries to show (most recent first, 0 = all)",
					Value: 20,
				},
				&cli.BoolFlag{
					Name:  "verify",
					Usage: "Verify chain integrity instead of listing entries",
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				if c.Bool("verify") {
					ok, brokenAt, err := audit.VerifyChain()
					if err != nil {
						return fmt.Errorf("failed to verify audit chain: %w", err)
					}
					if ok {
						fmt.Println("✅ Audit chain intact")
						return nil
					}
					fmt.Printf("❌ Audit chain broken at seq %d\n", brokenAt)
					return fmt.Errorf("audit chain broken at seq %d", brokenAt)
				}

				entries, err := audit.List(int(c.Int("limit")))
				if err != nil {
					return fmt.Errorf("failed to read audit log: %w", err)
				}
				if len(entries) == 0 {
					fmt.Println("📭 Audit log is empty")
					return nil
				}

				fmt.Printf("📜 Audit Log (%d entries)\n\n", len(entries))
				for _, e := range entries {
					fmt.Printf("[%d] seq=%d actor=%s action=%s\n", e.Timestamp, e.Seq, e.Actor, e.Action)
				}
				return nil
			},
		},
		{
			Name:  "migrate",
			Usage: "Migrate from JSON backup",
			Action: func(ctx context.Context, c *cli.Command) error {
				fmt.Println("🔄 Migrating from JSON backup...")

				db, err := InitDB()
				if err != nil {
					return fmt.Errorf("failed to open database: %w", err)
				}
				defer db.Close()

				if err := MigrateFromJSON(db); err != nil {
					return fmt.Errorf("migration failed: %w", err)
				}

				fmt.Println("✅ Migration complete")
				return nil
			},
		},
	},
}
