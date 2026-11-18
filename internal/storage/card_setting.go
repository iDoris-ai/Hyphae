package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// CardSetting is one identity's directory-card preference and the last
// publish. PublishedAt is the created_at of the last card or tombstone any
// relay accepted; the next one must be newer. With no stored row the identity
// is visible (the default), and the caller fills in the default name and tag.
type CardSetting struct {
	Visible     bool
	Name        string
	Tags        []string
	About       string
	PublishedAt int64
	ExpiresAt   int64
	// Relays lists every relay the card was ever published to, so hiding can
	// send the tombstone to all of them.
	Relays []string
	Stored bool
}

const cardSettingSchema = `
CREATE TABLE IF NOT EXISTS card_setting (
	identity_npub TEXT PRIMARY KEY,
	visible       INTEGER NOT NULL CHECK (visible IN (0, 1)),
	name          TEXT NOT NULL DEFAULT '',
	tags          TEXT NOT NULL DEFAULT '[]',
	about         TEXT NOT NULL DEFAULT '',
	published_at  INTEGER NOT NULL DEFAULT 0,
	expires_at    INTEGER NOT NULL DEFAULT 0,
	relays        TEXT NOT NULL DEFAULT '[]'
);`

func migrateCardSetting(db *sql.DB) error {
	if _, err := db.Exec(cardSettingSchema); err != nil {
		return fmt.Errorf("failed to create card_setting table: %w", err)
	}
	return nil
}

// LoadCardSetting returns the stored setting, or {Visible: true} when none.
func LoadCardSetting(db *sql.DB, identity string) (CardSetting, error) {
	var (
		s              CardSetting
		visible        int
		tags, relaysJS string
	)
	err := db.QueryRow(`SELECT visible, name, tags, about, published_at, expires_at, relays
		FROM card_setting WHERE identity_npub = ?`, identity).
		Scan(&visible, &s.Name, &tags, &s.About, &s.PublishedAt, &s.ExpiresAt, &relaysJS)
	if errors.Is(err, sql.ErrNoRows) {
		return CardSetting{Visible: true}, nil
	}
	if err != nil {
		return s, fmt.Errorf("load card setting: %w", err)
	}
	if err := json.Unmarshal([]byte(tags), &s.Tags); err != nil {
		return s, fmt.Errorf("card setting tags: %w", err)
	}
	if err := json.Unmarshal([]byte(relaysJS), &s.Relays); err != nil {
		return s, fmt.Errorf("card setting relays: %w", err)
	}
	s.Visible, s.Stored = visible == 1, true
	return s, nil
}

// ErrStaleCardSetting reports a save whose PublishedAt is older than the
// stored one: another writer published since this setting was loaded.
var ErrStaleCardSetting = errors.New("card setting changed since it was loaded")

// SaveCardSetting stores s for identity, replacing any earlier row unless
// that row records a newer publish (ErrStaleCardSetting). An equal
// PublishedAt overwrites, so this guard alone does not stop two writers in
// the same second: every load → publish → save must run inside
// WithCardLock, which serializes them.
func SaveCardSetting(db *sql.DB, identity string, s CardSetting) error {
	if identity == "" {
		return errors.New("save card setting: empty identity")
	}
	tags, err := json.Marshal(nonNil(s.Tags))
	if err != nil {
		return err
	}
	relays, err := json.Marshal(nonNil(s.Relays))
	if err != nil {
		return err
	}
	visible := 0
	if s.Visible {
		visible = 1
	}
	res, err := db.Exec(`INSERT INTO card_setting
		(identity_npub, visible, name, tags, about, published_at, expires_at, relays)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (identity_npub) DO UPDATE SET visible = excluded.visible,
			name = excluded.name, tags = excluded.tags, about = excluded.about,
			published_at = excluded.published_at, expires_at = excluded.expires_at,
			relays = excluded.relays
		WHERE card_setting.published_at <= excluded.published_at`,
		identity, visible, s.Name, string(tags), s.About, s.PublishedAt, s.ExpiresAt, string(relays))
	if err != nil {
		return fmt.Errorf("save card setting: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("save card setting: %w", err)
	} else if n == 0 {
		return ErrStaleCardSetting
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
