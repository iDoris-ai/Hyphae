package common

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/relayconfig"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestResolveRelaysPrecedenceAndErrorClassification(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resolve := func(args ...string) ([]string, error) {
		var got []string
		cmd := &cli.Command{Name: "send", Flags: []cli.Flag{&cli.StringSliceFlag{Name: "relay"}}, Action: func(_ context.Context, c *cli.Command) error {
			var err error
			got, err = ResolveRelays(c)
			return err
		}}
		err := cmd.Run(context.Background(), append([]string{"send"}, args...))
		return got, err
	}
	got, err := resolve()
	require.NoError(t, err)
	require.Equal(t, []string{relayconfig.DefaultRelay}, got)
	r, err := relayconfig.New()
	require.NoError(t, err)
	require.NoError(t, r.Set([]string{"wss://configured.example"}))
	got, err = resolve()
	require.NoError(t, err)
	require.Equal(t, []string{"wss://configured.example"}, got)
	got, err = resolve("--relay", "ws://explicit.example")
	require.NoError(t, err)
	require.Equal(t, []string{"ws://explicit.example"}, got)
	_, err = resolve("--relay", "wss://user:secret@example")
	var exitErr *ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, ErrCodeUser, exitErr.Code)
	_, err = resolve("--relay", "")
	require.Error(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(r.Path), "relays.json"), []byte("broken"), 0600))
	_, err = resolve()
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, ErrCodeOther, exitErr.Code)
}
