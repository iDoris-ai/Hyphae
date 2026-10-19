package groupchat

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestGroupchatSendJSONPasswordAndLogicalID(t *testing.T) {
	t.Run("json and human success", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newOpenDBGroup(t, "alice", "")
		logical := mustOpaque(t)
		args := []string{"groupchat", "send", "--group-id", g.draft.Group.ID, "--body", "hello", "--logical-id", logical, "--identity", "alice"}

		out, err := runGroupchat(t, "", append([]string{"--json"}, args...)...)
		require.NoError(t, err)
		var envelope struct {
			OK   bool         `json:"ok"`
			Data FanoutReport `json:"data"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &envelope))
		require.True(t, envelope.OK)
		require.Equal(t, logical, envelope.Data.LogicalID)
		require.Equal(t, 2, envelope.Data.Queued)
		require.Len(t, envelope.Data.Recipients, 2)
		for _, recipient := range envelope.Data.Recipients {
			require.NotNil(t, recipient.RetryCount)
			require.Zero(t, *recipient.RetryCount)
			require.Equal(t, recipient.QueueID, recipient.RetryQueueID)
		}
		require.NotContains(t, out, "queued 2 logical")

		humanLogical := mustOpaque(t)
		humanArgs := []string{"groupchat", "send", "--group-id", g.draft.Group.ID, "--body", "hello again", "--logical-id", humanLogical, "--identity", "alice"}
		human, err := runGroupchat(t, "", humanArgs...)
		require.NoError(t, err)
		require.Contains(t, human, "queued 2 logical "+humanLogical)
		require.NotContains(t, human, `"ok"`)
	})

	t.Run("encrypted keystore", func(t *testing.T) {
		const password = " group secret "
		t.Setenv("HOME", t.TempDir())
		g := newOpenDBGroup(t, "alice", password)
		logical := mustOpaque(t)
		base := []string{"groupchat", "send", "--group-id", g.draft.Group.ID, "--body", "secret hello", "--logical-id", logical, "--identity", "alice"}

		out, err := runGroupchat(t, password+"\n", append([]string{"--json"}, append(base, "--password-stdin")...)...)
		require.NoError(t, err)
		require.NotContains(t, out, password)
		var envelope struct {
			OK   bool         `json:"ok"`
			Data FanoutReport `json:"data"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &envelope))
		require.True(t, envelope.OK)
		require.Equal(t, logical, envelope.Data.LogicalID)
		require.Equal(t, 2, envelope.Data.Queued)

		missing, err := runGroupchat(t, password+"\n", append([]string{"--json"}, base...)...)
		require.Empty(t, missing)
		var exitErr *common.ExitError
		require.ErrorAs(t, err, &exitErr)
		require.Equal(t, common.ErrCodeAuth, exitErr.Code)
		require.NotContains(t, err.Error(), password)

		wrongLogical := mustOpaque(t)
		wrongArgs := []string{"--json", "groupchat", "send", "--group-id", g.draft.Group.ID, "--body", "nope", "--logical-id", wrongLogical, "--identity", "alice", "--password-stdin"}
		wrongOut, err := runGroupchat(t, "wrong secret\n", wrongArgs...)
		require.Empty(t, wrongOut)
		require.ErrorAs(t, err, &exitErr)
		require.Equal(t, common.ErrCodeAuth, exitErr.Code)
		require.NotContains(t, err.Error(), "wrong secret")
	})

	t.Run("error names the logical id", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		ks := &types.KeyStore{}
		_, err := identity.CreateIdentity(ks, "alice")
		require.NoError(t, err)
		require.NoError(t, identity.SaveKeyStore(ks))
		logical := mustOpaque(t)
		_, err = runGroupchat(t, "", "groupchat", "send", "--group-id", mustOpaque(t), "--body", "missing", "--logical-id", logical, "--identity", "alice")
		require.ErrorIs(t, err, ErrGroupNotFound)
		require.ErrorContains(t, err, "logical "+logical)

		_, err = runGroupchat(t, "", "groupchat", "send", "--group-id", mustOpaque(t), "--body", "missing", "--identity", "alice")
		require.ErrorIs(t, err, ErrGroupNotFound)
		require.Regexp(t, regexp.MustCompile(`logical [0-9a-f]{32}:`), err.Error())
	})
}

func newOpenDBGroup(t *testing.T, nickname, password string) *threeMemberGroup {
	t.Helper()
	ks := &types.KeyStore{}
	var created *types.Identity
	var err error
	if password == "" {
		created, err = identity.CreateIdentity(ks, nickname)
	} else {
		created, err = identity.CreateIdentityWithPassword(ks, nickname, password)
	}
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))
	sk, err := identity.GetSecretKey(ks, nickname)
	require.NoError(t, err)
	require.Equal(t, created.Npub, common.EncodeNpub(sk.Public()))
	db, err := storage.OpenDB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStore(db)
	require.NoError(t, err)
	g := &threeMemberGroup{
		t:     t,
		alice: testMember{sk: sk, npub: created.Npub, db: db, store: store},
		bob:   newMember(t),
		carol: newMember(t),
	}
	draft, err := g.alice.store.CreateGroup("planning", g.alice.npub, []string{g.bob.npub, g.carol.npub})
	require.NoError(t, err)
	g.draft = draft
	g.invites = map[string]Envelope{}
	for _, invite := range draft.Invitations {
		g.invites[invite.InviteeNpub] = invite
		target := g.member(invite.InviteeNpub)
		accepted, recvErr := target.store.ReceiveInvite(buildIncoming(t, g.alice, target, invite))
		require.NoError(t, recvErr)
		require.True(t, accepted)
	}
	g.activate()
	return g
}

func runGroupchat(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var runErr error
	out := captureStdout(t, func() {
		withStdin(t, stdin, func() {
			root := &cli.Command{
				Name:     "hyphae",
				Flags:    []cli.Flag{&cli.BoolFlag{Name: "json"}},
				Commands: []*cli.Command{Cmd},
			}
			runErr = root.Run(context.Background(), append([]string{"hyphae"}, args...))
		})
	})
	return out, runErr
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	require.NoError(t, w.Close())
	os.Stdout = orig
	buf, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(buf)
}

func withStdin(t *testing.T, content string, fn func()) {
	t.Helper()
	orig := os.Stdin
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	os.Stdin = r
	defer func() {
		os.Stdin = orig
		_ = r.Close()
	}()
	fn()
}
