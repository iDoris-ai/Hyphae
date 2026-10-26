//go:build integration

package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startFastDaemon runs the real daemon CLI with a one-second watch and retry
// loop, so queued group events leave the outbox without manual help.
func startFastDaemon(t *testing.T, bin, home, identity, relayURL string) *daemonCLIProcess {
	t.Helper()
	cmd := exec.Command(bin, "daemon", "--identity", identity, "--relay", relayURL,
		"--watch-interval", "1", "--retry-interval", "1", "--notify=false", "--auto-reply=false")
	cmd.Env = isolatedEnv(home)
	process := &daemonCLIProcess{
		cmd: cmd, stdout: &boundedOutput{max: 2 << 20}, stderr: &boundedOutput{max: 1 << 20},
		done: make(chan error, 1),
	}
	cmd.Stdout, cmd.Stderr = process.stdout, process.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	go func() { process.done <- cmd.Wait() }()
	t.Cleanup(func() { _ = process.stop(3 * time.Second) })
	return process
}

func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool, diag func() string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s\n%s", what, diag())
}

// TestInviteReachesOtherHomesThroughRelay: alice, bob and carol each live in a
// HOME that holds only their own key. alice's create queues two invites, her
// daemon publishes them to a real relay, and each invitee's daemon stores the
// invite -- nothing is copied between HOMEs by hand.
func TestInviteReachesOtherHomesThroughRelay(t *testing.T) {
	root := projectRoot(t)
	temp := t.TempDir()
	cliBin := filepath.Join(temp, "hyphae")
	relayBin := filepath.Join(temp, "hyphae-relay")
	buildBinary(t, root, cliBin, "./cmd/hyphae")
	buildBinary(t, root, relayBin, "./cmd/hyphae-relay")

	port := freeLoopbackPort(t)
	startRelay(t, relayBin, filepath.Join(temp, "relay-data"), port)
	relayURL := "ws://127.0.0.1:" + strconv.Itoa(port)

	homes := map[string]string{}
	npubs := map[string]string{}
	for _, nick := range []string{"alice", "bob", "carol"} {
		home := filepath.Join(temp, nick)
		if err := os.Mkdir(home, 0o700); err != nil {
			t.Fatal(err)
		}
		homes[nick] = home
		npubs[nick] = egIdentity(t, cliBin, home, nick, true)
		egMust(t, cliBin, home, "relay", "set", "--relay", relayURL)
	}
	daemons := map[string]*daemonCLIProcess{}
	diag := func() string {
		var b strings.Builder
		for nick, d := range daemons {
			b.WriteString(nick + " daemon:\n" + egTail(d.stdout.String()) + egTail(d.stderr.String()) + "\n")
		}
		return b.String()
	}
	for nick, home := range homes {
		daemons[nick] = startFastDaemon(t, cliBin, home, nick, relayURL)
	}

	groupID := egGroup(t, egMust(t, cliBin, homes["alice"], "groupchat", "create", "--name", "three-homes", "--identity", "alice",
		"--invitee", npubs["bob"], "--invitee", npubs["carol"]))

	for _, nick := range []string{"bob", "carol"} {
		nick := nick
		waitFor(t, nick+" receives the invite", 40*time.Second, func() bool {
			out, _, code := egRun(t, cliBin, homes[nick], nil, "groupchat", "invite", "list", "--group", groupID, "--identity", nick, "--json")
			if code != 0 {
				return false
			}
			for _, item := range egInvites2(t, out) {
				if item.InviteeNpub == npubs[nick] {
					return true
				}
			}
			return false
		}, diag)
		if got := egState(t, homes[nick], npubs[nick], groupID); got != "pending" {
			t.Fatalf("%s group state %s before accepting", nick, got)
		}
	}
}

type egInviteRow struct{ InviteID, InviteeNpub string }

func egInvites2(t *testing.T, out string) []egInviteRow {
	t.Helper()
	payload := egDecode[struct {
		Invites []struct {
			InviteID    string `json:"invite_id"`
			InviteeNpub string `json:"invitee_npub"`
		} `json:"invites"`
	}](t, out)
	rows := make([]egInviteRow, len(payload.Invites))
	for i, item := range payload.Invites {
		rows[i] = egInviteRow{item.InviteID, item.InviteeNpub}
	}
	return rows
}
