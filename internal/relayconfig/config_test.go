package relayconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveDefaultConfigAndExplicitPrecedence(t *testing.T) {
	r := &Resolver{Path: filepath.Join(t.TempDir(), "relays.json")}
	got, err := r.Resolve(nil)
	require.NoError(t, err)
	require.Equal(t, Resolution{Relays: []string{DefaultRelay}, Source: "default"}, got)
	require.NoError(t, r.Set([]string{"wss://config.example"}))
	got, err = r.Resolve(nil)
	require.NoError(t, err)
	require.Equal(t, Resolution{Relays: []string{"wss://config.example"}, Source: "config"}, got)
	got, err = r.Resolve([]string{"ws://explicit.example"})
	require.NoError(t, err)
	require.Equal(t, Resolution{Relays: []string{"ws://explicit.example"}, Source: "explicit"}, got)
	info, err := os.Stat(r.Path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	dirInfo, err := os.Stat(filepath.Dir(r.Path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())
}

func TestResolveRejectsCorruptAndUnknownVersion(t *testing.T) {
	r := &Resolver{Path: filepath.Join(t.TempDir(), "relays.json")}
	require.NoError(t, os.WriteFile(r.Path, []byte("{"), 0600))
	_, err := r.Resolve(nil)
	require.ErrorContains(t, err, "decode")
	require.NoError(t, os.WriteFile(r.Path, []byte(`{"version":2,"relays":["wss://x"]}`), 0600))
	_, err = r.Resolve(nil)
	require.ErrorContains(t, err, "version")
}

func TestValidateRelayURLs(t *testing.T) {
	for _, raw := range []string{"", "https://example.com", "ws://", "ws://:80", "ws://host:0", "wss://host:65536", "wss://user:secret@host", "wss://host#", "wss://host/path#frag"} {
		t.Run(raw, func(t *testing.T) { require.Error(t, Validate([]string{raw})) })
	}
	require.Error(t, Validate(nil))
	require.NoError(t, Validate([]string{"wss://relay.example", "ws://localhost:8080/path"}))
}
