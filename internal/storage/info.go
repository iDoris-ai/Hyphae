package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
)

type storageInfo struct {
	Path         string   `json:"path"`
	Exists       bool     `json:"exists"`
	SizeBytes    int64    `json:"size_bytes"`
	Mode         string   `json:"mode"`
	MessageCount int64    `json:"message_count"`
	Tables       []string `json:"tables"`
}

func inspectStorageInfo(ctx context.Context) (storageInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return storageInfo{}, fmt.Errorf("failed to get user home: %w", err)
	}
	path := filepath.Join(home, ".hyphae", "messages.db")
	result := storageInfo{Path: path, Tables: []string{}}
	fileInfo, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return result, nil
		}
		return storageInfo{}, fmt.Errorf("failed to stat database: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return storageInfo{}, fmt.Errorf("database path is not a regular file")
	}

	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(path))
	if err != nil {
		return storageInfo{}, fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return storageInfo{}, fmt.Errorf("failed to open database: %w", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages").Scan(&result.MessageCount); err != nil {
		return storageInfo{}, fmt.Errorf("failed to count messages: %w", err)
	}
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		return storageInfo{}, fmt.Errorf("failed to list database tables: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return storageInfo{}, fmt.Errorf("failed to read database table: %w", err)
		}
		result.Tables = append(result.Tables, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return storageInfo{}, fmt.Errorf("failed to read database tables: %w", err)
	}
	if err := rows.Close(); err != nil {
		return storageInfo{}, fmt.Errorf("failed to close database table query: %w", err)
	}
	sort.Strings(result.Tables)
	result.Exists = true
	result.SizeBytes = fileInfo.Size()
	result.Mode = fileInfo.Mode().String()
	return result, nil
}

func sqliteReadOnlyDSN(path string) string {
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := url.Values{}
	query.Set("mode", "ro")
	u.RawQuery = query.Encode()
	return u.String()
}
