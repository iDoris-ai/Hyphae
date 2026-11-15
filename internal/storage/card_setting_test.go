package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCardSettingDefaultsAndRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, err := OpenDB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	s, err := LoadCardSetting(db, "npub-a")
	require.NoError(t, err)
	require.Equal(t, CardSetting{Visible: true}, s, "no row means public by default")

	want := CardSetting{Visible: false, Name: "小王", Tags: []string{"友好", "火锅"}, About: "hi",
		PublishedAt: 10, ExpiresAt: 20, Relays: []string{"wss://a", "wss://b"}}
	require.NoError(t, SaveCardSetting(db, "npub-a", want))
	got, err := LoadCardSetting(db, "npub-a")
	require.NoError(t, err)
	want.Stored = true
	require.Equal(t, want, got)

	// Replacing keeps one row and stores nil slices as empty lists.
	require.NoError(t, SaveCardSetting(db, "npub-a", CardSetting{Visible: true, PublishedAt: 10}))
	got, err = LoadCardSetting(db, "npub-a")
	require.NoError(t, err)
	require.Equal(t, CardSetting{Visible: true, Tags: []string{}, Relays: []string{}, PublishedAt: 10, Stored: true}, got)

	// A save from a stale load never moves PublishedAt back or drops relays.
	require.ErrorIs(t, SaveCardSetting(db, "npub-a", CardSetting{PublishedAt: 9}), ErrStaleCardSetting)
	got, err = LoadCardSetting(db, "npub-a")
	require.NoError(t, err)
	require.Equal(t, int64(10), got.PublishedAt)
	require.True(t, got.Visible)

	require.Error(t, SaveCardSetting(db, "", CardSetting{}), "empty identity would be shared by everyone")

	other, err := LoadCardSetting(db, "npub-b")
	require.NoError(t, err)
	require.False(t, other.Stored)
}

func TestCardSettingRejectsCorruptRows(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, err := OpenDB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`INSERT INTO card_setting (identity_npub, visible, tags) VALUES ('x', 1, 'not json')`)
	require.NoError(t, err)
	_, err = LoadCardSetting(db, "x")
	require.Error(t, err)
	_, err = db.Exec(`INSERT INTO card_setting (identity_npub, visible, relays) VALUES ('y', 1, '{')`)
	require.NoError(t, err)
	_, err = LoadCardSetting(db, "y")
	require.Error(t, err)
	_, err = db.Exec(`INSERT INTO card_setting (identity_npub, visible) VALUES ('z', 2)`)
	require.Error(t, err, "visible is 0 or 1")
}

func TestWithCardLockSerializes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	inside := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithCardLock(func() error {
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside
	second := make(chan struct{})
	go func() {
		_ = WithCardLock(func() error { close(second); return nil })
	}()
	select {
	case <-second:
		t.Fatal("second holder ran while the first held the lock")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-done)
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("second holder never ran")
	}
	boom := errors.New("boom")
	require.ErrorIs(t, WithCardLock(func() error { return boom }), boom)
}
