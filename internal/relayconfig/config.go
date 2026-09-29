// Package relayconfig loads and atomically replaces a user's relay list.
package relayconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const DefaultRelay = "wss://relay.aastar.io"

type Resolution struct {
	Relays []string `json:"relays"`
	Source string   `json:"source"`
}

type file struct {
	Version int      `json:"version"`
	Relays  []string `json:"relays"`
}

type Resolver struct{ Path string }

func New() (*Resolver, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Resolver{Path: filepath.Join(home, ".hyphae", "relays.json")}, nil
}

func Validate(relays []string) error {
	if len(relays) == 0 {
		return errors.New("at least one relay is required")
	}
	for _, raw := range relays {
		u, err := url.Parse(raw)
		if err != nil {
			return errors.New("invalid relay URL syntax")
		}
		if u.Scheme != "ws" && u.Scheme != "wss" {
			return errors.New("relay URL must use ws:// or wss://")
		}
		if u.Hostname() == "" {
			return errors.New("relay URL must include a host")
		}
		if u.User != nil {
			return errors.New("relay URL must not contain credentials")
		}
		if strings.Contains(raw, "#") {
			return errors.New("relay URL must not contain a fragment")
		}
		if strings.ContainsAny(raw, "\r\n") {
			return errors.New("relay URL contains a newline")
		}
		if port := u.Port(); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return errors.New("relay URL port must be between 1 and 65535")
			}
		}
	}
	return nil
}

func (r *Resolver) Resolve(explicit []string) (Resolution, error) {
	if len(explicit) != 0 {
		if err := Validate(explicit); err != nil {
			return Resolution{}, err
		}
		return Resolution{Relays: append([]string(nil), explicit...), Source: "explicit"}, nil
	}
	b, err := os.ReadFile(r.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Resolution{Relays: []string{DefaultRelay}, Source: "default"}, nil
	}
	if err != nil {
		return Resolution{}, fmt.Errorf("read relay config: %w", err)
	}
	var cfg file
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Resolution{}, fmt.Errorf("decode relay config: %w", err)
	}
	if cfg.Version != 1 {
		return Resolution{}, fmt.Errorf("unsupported relay config version %d", cfg.Version)
	}
	if err := Validate(cfg.Relays); err != nil {
		return Resolution{}, fmt.Errorf("invalid relay config: %w", err)
	}
	return Resolution{Relays: cfg.Relays, Source: "config"}, nil
}

func (r *Resolver) Set(relays []string) error {
	if err := Validate(relays); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.Path), 0700); err != nil {
		return fmt.Errorf("create relay config directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(r.Path), 0700); err != nil {
		return fmt.Errorf("secure relay config directory: %w", err)
	}
	b, err := json.Marshal(file{Version: 1, Relays: relays})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.Path), ".relays-*.tmp")
	if err != nil {
		return fmt.Errorf("create relay config temp file: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, r.Path); err != nil {
		return fmt.Errorf("replace relay config: %w", err)
	}
	dir, err := os.Open(filepath.Dir(r.Path))
	if err != nil {
		return fmt.Errorf("open relay config directory for sync: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync relay config directory: %w", err)
	}
	return nil
}
