package storage

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

// StorageCmd provides storage management commands
var StorageCmd = &cli.Command{
	Name:  "storage",
	Usage: "Manage local storage",
	Description: `View and manage the SQLite message database`,
	Commands: []*cli.Command{
		{
			Name:  "info",
			Usage: "Show storage information",
			Action: func(ctx context.Context, c *cli.Command) error {
				dbPath := GetDBPath()

				// Check if database exists
				info, err := os.Stat(dbPath)
				if err != nil {
					if os.IsNotExist(err) {
						fmt.Println("📭 Database not created yet")
						fmt.Printf("   Path: %s\n", dbPath)
						return nil
					}
					return fmt.Errorf("failed to stat database: %w", err)
				}

				fmt.Println("💾 Storage Information")
				fmt.Println("======================")
				fmt.Printf("Database: %s\n", dbPath)
				fmt.Printf("Size:     %d bytes (%.2f KB)\n", info.Size(), float64(info.Size())/1024)
				fmt.Printf("Mode:     %s\n", info.Mode())

				// Initialize to get stats
				db, err := InitDB()
				if err != nil {
					return fmt.Errorf("failed to open database: %w", err)
				}

				// Get message count
				var count int
				err = db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
				if err != nil {
					return fmt.Errorf("failed to count messages: %w", err)
				}

				fmt.Printf("Messages: %d\n", count)

				// Get table info
				fmt.Println("\n📊 Tables:")
				rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table'")
				if err != nil {
					return err
				}
				defer rows.Close()

				for rows.Next() {
					var name string
					if err := rows.Scan(&name); err != nil {
						continue
					}
					fmt.Printf("   - %s\n", name)
				}

				return nil
			},
		},
		{
			Name:  "migrate",
			Usage: "Migrate from JSON backup",
			Action: func(ctx context.Context, c *cli.Command) error {
				fmt.Println("🔄 Migrating from JSON backup...")

				if err := MigrateFromJSON(); err != nil {
					return fmt.Errorf("migration failed: %w", err)
				}

				fmt.Println("✅ Migration complete")
				return nil
			},
		},
	},
}
