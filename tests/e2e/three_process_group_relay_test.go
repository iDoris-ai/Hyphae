package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// TestThreeProcessGroupRelay is the M1 transport/crypto harness.
// It is a test helper, not a group CLI: three OS processes call the existing
// library paths (NIP-44 sign, outbox sender-binding, VerifyIncoming, ReceiveMessage).
// AttemptSendRouted still has no group handler, and this test does not invent one.
func TestThreeProcessGroupRelay(t *testing.T) {
	if role := os.Getenv("HYPHAE_M1_ROLE"); role != "" {
		os.Exit(runRole(role))
	}
	if os.Getenv("HYPHAE_M1_HARNESS") == "selftest" {
		os.Exit(runDropSelftest())
	}

	var summaries []runSummary
	defer func() { writeRelayReport(t, summaries) }()

	gap := assertGroupRouteHandlerMissing(t)
	for run := 1; run <= 3; run++ {
		summary, err := runPositiveScenario(t, run)
		if err != nil {
			t.Fatalf("positive run %d: %v", run, err)
		}
		summaries = append(summaries, summary)
	}
	selftestCode := execSelftest(t)
	if selftestCode != dropExitCode {
		t.Fatalf("SELFTEST dropped-recipient machine exit = %d, want %d", selftestCode, dropExitCode)
	}
	summaries = append(summaries, runSummary{Run: 0, Mode: "selftest", ExitCode: selftestCode, Gap: gap})
	if summaries[0].Gap == "" {
		summaries[0].Gap = gap
	}
}

const (
	dropExitCode = 3
	bodyOne      = "relay-proof-one"
	bodyTwo      = "relay-proof-two"
	foreignBody  = "foreign-not-member"
	wrongBody    = "wrong-group-body"
	roleAlice    = "alice"
	roleBob      = "bob"
	roleCharlie  = "charlie"
)

type runSummary struct {
	Run            int             `json:"run"`
	Mode           string          `json:"mode"`
	ExitCode       int             `json:"exit_code,omitempty"`
	Gap            string          `json:"gap,omitempty"`
	GroupID        string          `json:"group_id,omitempty"`
	LogicalIDs     []string        `json:"logical_ids,omitempty"`
	PIDs           []int           `json:"pids,omitempty"`
	EventIDs       []string        `json:"event_ids,omitempty"`
	RelayOK        int             `json:"relay_accepted,omitempty"`
	RelayDup       int             `json:"relay_min_deliveries,omitempty"`
	PublicReceipts json.RawMessage `json:"public_receipts,omitempty"`
}

type identityCard struct {
	Role string `json:"role"`
	Npub string `json:"npub"`
	Hex  string `json:"hex"`
	PID  int    `json:"pid"`
}

type msgReceipt struct {
	LogicalID      string `json:"logical_id"`
	EventID        string `json:"event_id"`
	Signature      string `json:"signature"`
	CipherSHA256   string `json:"ciphertext_sha256"`
	PlainSHA256    string `json:"plaintext_sha256"`
	SenderNpub     string `json:"sender_npub"`
	RecipientNpub  string `json:"recipient_npub"`
	Inserts        int    `json:"inserts"`
	DuplicateSkips int    `json:"duplicate_skips"`
	Deliveries     int    `json:"deliveries"`
}

type negReceipt struct {
	Name        string `json:"name"`
	EventID     string `json:"event_id"`
	Refused     bool   `json:"refused"`
	DBUnchanged bool   `json:"db_unchanged"`
	RelayStored bool   `json:"relay_stored"`
	ErrorClass  string `json:"error_class"`
}

type peerReceipt struct {
	Role            string       `json:"role"`
	PID             int          `json:"pid"`
	PPID            int          `json:"ppid"`
	Npub            string       `json:"npub"`
	Hex             string       `json:"pubkey_hex"`
	GroupID         string       `json:"group_id"`
	Messages        []msgReceipt `json:"messages"`
	Negatives       []negReceipt `json:"negatives"`
	DMInboxRows     int          `json:"dm_inbox_rows"`
	GroupRows       int          `json:"group_message_rows"`
	KeyFileMode     uint32       `json:"key_file_mode"`
	SawDuplicate    bool         `json:"saw_duplicate_delivery"`
	SawReorder      bool         `json:"saw_reordered_delivery"`
	FirstLogical    []string     `json:"first_logical_order"`
	ReservedRefused bool         `json:"reserved_group_refused_by_dm_inbox"`
}

type publishedMsg struct {
	LogicalID    string `json:"logical_id"`
	BodySHA256   string `json:"body_sha256"`
	Recipient    string `json:"recipient_npub"`
	EventID      string `json:"event_id"`
	Signature    string `json:"signature"`
	CipherSHA256 string `json:"ciphertext_sha256"`
}

type manifest struct {
	GroupID  string         `json:"group_id"`
	Messages []publishedMsg `json:"messages"`
	Order    []string       `json:"publish_order"`
}

type negativeCase struct {
	Name          string          `json:"name"`
	EventID       string          `json:"event_id"`
	TargetNpub    string          `json:"target_npub"`
	Expect        string          `json:"expect"`
	RelayAccepted bool            `json:"relay_accepted"`
	Event         json.RawMessage `json:"event,omitempty"`
}

type negativeCatalog struct {
	Cases []negativeCase `json:"cases"`
}

func assertGroupRouteHandlerMissing(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	relay := newMisRelay("")
	defer relay.close()

	sender, recipient := nostr.Generate(), nostr.Generate()
	event, err := messaging.BuildAgentMessageEvent(sender, recipient.Public(), "gap-probe", nostr.Now())
	if err != nil {
		t.Fatalf("gap probe event: %v", err)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	entry, existed, err := messaging.EnqueueGroupOutboxEntry(string(raw), common.EncodeNpub(recipient.Public()), []string{relay.URL}, 3)
	if err != nil || existed {
		t.Fatalf("enqueue gap probe: existed=%t err=%v", existed, err)
	}
	before, err := os.ReadFile(filepath.Join(home, ".hyphae", "outbox.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := messaging.AttemptSendRouted(ctx, &types.Outbox{Entries: []types.OutboxEntry{entry}}, entry, []string{relay.URL}, time.Second, messaging.AttemptOptions{})
	if !errors.Is(err, messaging.ErrGroupRouteHandlerMissing) {
		t.Fatalf("group route gap: err=%v", err)
	}
	if result.Attempted || result.Sent {
		t.Fatalf("missing handler must not publish, attempted=%t sent=%t", result.Attempted, result.Sent)
	}
	after, err := os.ReadFile(filepath.Join(home, ".hyphae", "outbox.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("missing group handler mutated the outbox")
	}
	if got := relay.acceptedCount(); got != 0 {
		t.Fatalf("missing group handler still reached the relay (%d events)", got)
	}
	return "AttemptSendRouted group route returns ErrGroupRouteHandlerMissing before dial; no exported caller builds prepared fanout rows or installs NewFanoutAttemptHandlerFor"
}

func runPositiveScenario(t *testing.T, run int) (runSummary, error) {
	t.Helper()
	return runScenario(scenarioConfig{Mode: "positive", Run: run, Deadline: 25 * time.Second})
}

func runDropSelftest() int {
	summary, err := runScenario(scenarioConfig{Mode: "drop", Run: 0, Deadline: 12 * time.Second})
	if err != nil {
		fmt.Fprintf(os.Stderr, "selftest setup: %s\n", scrub(err.Error()))
		return 1
	}
	if summary.ExitCode != dropExitCode {
		fmt.Fprintf(os.Stderr, "selftest charlie exit %d\n", summary.ExitCode)
		return 1
	}
	return dropExitCode
}

type scenarioConfig struct {
	Mode     string
	Run      int
	Deadline time.Duration
}

type scenarioChild struct {
	role   string
	home   string
	cmd    *exec.Cmd
	out    *bytes.Buffer
	errBuf *bytes.Buffer
}

func runScenario(cfg scenarioConfig) (runSummary, error) {
	root, err := os.MkdirTemp("", "hyphae-m1-three-process-")
	if err != nil {
		return runSummary{}, err
	}
	defer os.RemoveAll(root)
	pub := filepath.Join(root, "pub")
	if err := os.MkdirAll(pub, 0o700); err != nil {
		return runSummary{}, err
	}
	relay := newMisRelay("")
	defer relay.close()

	roles := []string{roleAlice, roleBob, roleCharlie}
	children := make([]scenarioChild, 0, 3)
	for _, role := range roles {
		home := filepath.Join(root, role)
		if err := os.MkdirAll(home, 0o700); err != nil {
			return runSummary{}, err
		}
		cmd, stdout, stderr, err := commandForRole(role, home, relay.URL, pub, cfg.Mode)
		if err != nil {
			return runSummary{}, err
		}
		if err := cmd.Start(); err != nil {
			return runSummary{}, err
		}
		children = append(children, scenarioChild{role: role, home: home, cmd: cmd, out: stdout, errBuf: stderr})
	}
	defer func() {
		for _, c := range children {
			if c.cmd.Process != nil && c.cmd.ProcessState == nil {
				_ = c.cmd.Process.Kill()
			}
		}
	}()

	pids := make([]int, 0, 3)
	for _, c := range children {
		pids = append(pids, c.cmd.Process.Pid)
	}
	if err := waitUntil(2*time.Second, func() bool {
		alive := 0
		for _, pid := range pids {
			if processAlive(pid) {
				alive++
			}
		}
		return alive == 3
	}); err != nil {
		return runSummary{}, fmt.Errorf("three processes were not alive together: %w", err)
	}
	if err := assertNoKeyInProcessEnv(children); err != nil {
		return runSummary{}, err
	}

	if cfg.Mode == "drop" {
		if err := armDrop(relay, pub); err != nil {
			return runSummary{}, err
		}
	}

	done := make(chan childResult, len(children))
	for _, c := range children {
		c := c
		go func() {
			err := c.cmd.Wait()
			code := 0
			if c.cmd.ProcessState != nil {
				code = c.cmd.ProcessState.ExitCode()
			}
			if err != nil && code == 0 {
				code = 1
			}
			done <- childResult{role: c.role, code: code, stdout: c.out.String(), stderr: c.errBuf.String(), home: c.home, pid: c.cmd.Process.Pid}
		}()
	}

	results := map[string]childResult{}
	timeout := time.After(cfg.Deadline)
	waitAll := cfg.Mode != "drop"
	for {
		if waitAll && len(results) == len(children) {
			break
		}
		if !waitAll {
			if charlie, ok := results[roleCharlie]; ok {
				summary := runSummary{Run: cfg.Run, Mode: cfg.Mode, PIDs: pids, RelayOK: relay.acceptedCount(), ExitCode: charlie.code}
				for _, c := range children {
					if c.cmd.Process != nil && results[c.role].pid == 0 {
						_ = c.cmd.Process.Kill()
					}
				}
				if charlie.code != dropExitCode {
					return summary, fmt.Errorf("dropped charlie exit %d stderr=%s", charlie.code, scrub(charlie.stderr))
				}
				return summary, nil
			}
		}
		select {
		case result := <-done:
			results[result.role] = result
		case <-timeout:
			for _, c := range children {
				if _, ok := results[c.role]; !ok && c.cmd.Process != nil {
					_ = c.cmd.Process.Kill()
				}
			}
			return runSummary{}, fmt.Errorf("scenario %s timed out", cfg.Mode)
		}
	}
	for _, result := range results {
		if strings.Contains(result.stdout+result.stderr, "nsec1") {
			return runSummary{}, errors.New("process output contained an nsec")
		}
	}

	summary := runSummary{Run: cfg.Run, Mode: cfg.Mode, PIDs: pids, RelayOK: relay.acceptedCount()}
	if cfg.Mode == "drop" {
		charlie := results[roleCharlie]
		summary.ExitCode = charlie.code
		if charlie.code != dropExitCode {
			return summary, fmt.Errorf("dropped charlie exit %d stderr=%s", charlie.code, scrub(charlie.stderr))
		}
		if results[roleAlice].code == 0 && results[roleBob].code == 0 {
			// Alice and Bob may still finish; charlie must be the nonzero machine exit.
		}
		return summary, nil
	}

	for _, role := range roles {
		if results[role].code != 0 {
			return summary, fmt.Errorf("%s exit %d relay_accepted=%d\nstderr:\n%s", role, results[role].code, relay.acceptedCount(), scrub(results[role].stderr+"\n---\n"+results[roleAlice].stderr+"\n---\n"+results[roleBob].stderr+"\n---\n"+results[roleCharlie].stderr))
		}
	}
	if err := validatePositive(pub, relay, results, &summary); err != nil {
		return summary, err
	}
	return summary, nil
}

type childResult struct {
	role   string
	code   int
	stdout string
	stderr string
	home   string
	pid    int
}

func commandForRole(role, home, relayURL, pub, mode string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer, error) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestThreeProcessGroupRelay$", "-test.count=1", "-test.timeout=40s")
	cmd.Dir = home
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = []string{
		"HOME=" + home,
		"TMPDIR=" + os.TempDir(),
		"PATH=" + os.Getenv("PATH"),
		"LANG=" + os.Getenv("LANG"),
		"HYPHAE_M1_ROLE=" + role,
		"HYPHAE_M1_RELAY=" + relayURL,
		"HYPHAE_M1_PUB=" + pub,
		"HYPHAE_M1_MODE=" + mode,
	}
	return cmd, stdout, stderr, nil
}

func armDrop(relay *misRelay, pub string) error {
	var charlie identityCard
	if err := waitJSON(filepath.Join(pub, roleCharlie+".json"), &charlie, 5*time.Second); err != nil {
		return err
	}
	relay.setDropHex(charlie.Hex)
	return os.WriteFile(filepath.Join(pub, "drop-armed"), []byte("armed\n"), 0o600)
}

func assertNoKeyInProcessEnv(children []scenarioChild) error {
	deadline := time.Now().Add(3 * time.Second)
	for _, c := range children {
		keyPath := filepath.Join(c.home, ".hyphae", "role.key")
		for {
			info, err := os.Stat(keyPath)
			if err == nil {
				if info.Mode().Perm() != 0o600 {
					return fmt.Errorf("%s key mode %o", c.role, info.Mode().Perm())
				}
				secret, err := os.ReadFile(keyPath)
				if err != nil {
					return err
				}
				out, _ := exec.Command("ps", "eww", "-p", strconv.Itoa(c.cmd.Process.Pid)).CombinedOutput()
				if bytes.Contains(out, bytes.TrimSpace(secret)) {
					return fmt.Errorf("%s private key appeared in process environment", c.role)
				}
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%s key file was not created", c.role)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	return nil
}

func validatePositive(pub string, relay *misRelay, results map[string]childResult, summary *runSummary) error {
	var man manifest
	if err := readJSON(filepath.Join(pub, "manifest.json"), &man); err != nil {
		return err
	}
	receipts := map[string]peerReceipt{}
	for _, role := range []string{roleAlice, roleBob, roleCharlie} {
		var receipt peerReceipt
		if err := readJSON(filepath.Join(pub, "receipt-"+role+".json"), &receipt); err != nil {
			return err
		}
		if receipt.PID != results[role].pid {
			return fmt.Errorf("%s receipt pid %d != exec pid %d", role, receipt.PID, results[role].pid)
		}
		if receipt.KeyFileMode != 0o600 {
			return fmt.Errorf("%s key mode %o", role, receipt.KeyFileMode)
		}
		if receipt.DMInboxRows != 0 {
			return fmt.Errorf("%s DM inbox rows = %d", role, receipt.DMInboxRows)
		}
		receipts[role] = receipt
		secret, err := os.ReadFile(filepath.Join(results[role].home, ".hyphae", "role.key"))
		if err != nil {
			return err
		}
		blob := results[role].stdout + results[role].stderr
		if strings.Contains(blob, string(bytes.TrimSpace(secret))) || strings.Contains(blob, bodyOne) || strings.Contains(blob, bodyTwo) {
			return fmt.Errorf("%s output leaked a private key or plaintext", role)
		}
	}
	pids := []int{receipts[roleAlice].PID, receipts[roleBob].PID, receipts[roleCharlie].PID}
	if pids[0] == pids[1] || pids[1] == pids[2] || pids[0] == pids[2] || pids[0] == os.Getpid() {
		return fmt.Errorf("pids are not three distinct child processes: %v parent %d", pids, os.Getpid())
	}
	if receipts[roleBob].GroupID != man.GroupID || receipts[roleCharlie].GroupID != man.GroupID || receipts[roleAlice].GroupID != man.GroupID {
		return errors.New("group id diverged")
	}
	if !receipts[roleBob].SawDuplicate || !receipts[roleCharlie].SawDuplicate {
		return errors.New("recipient did not observe a duplicate delivery")
	}
	if !receipts[roleBob].SawReorder || !receipts[roleCharlie].SawReorder {
		return fmt.Errorf("recipient did not observe reorder bob=%v charlie=%v", receipts[roleBob].FirstLogical, receipts[roleCharlie].FirstLogical)
	}
	if !receipts[roleBob].ReservedRefused || !receipts[roleCharlie].ReservedRefused {
		return errors.New("DM inbox guard did not refuse the reserved group payload")
	}
	byRecipient := map[string]map[string]publishedMsg{}
	minDeliveries := 0
	for _, msg := range man.Messages {
		if byRecipient[msg.Recipient] == nil {
			byRecipient[msg.Recipient] = map[string]publishedMsg{}
		}
		byRecipient[msg.Recipient][msg.LogicalID] = msg
		got := relay.deliveryCount(msg.EventID)
		if got < 3 {
			return fmt.Errorf("relay deliveries for %s = %d, want >= 3", msg.EventID, got)
		}
		if minDeliveries == 0 || got < minDeliveries {
			minDeliveries = got
		}
		if !relay.accepted(msg.EventID) {
			return fmt.Errorf("relay did not accept signed event %s", msg.EventID)
		}
		summary.EventIDs = append(summary.EventIDs, msg.EventID)
	}
	for _, role := range []string{roleBob, roleCharlie} {
		receipt := receipts[role]
		if receipt.GroupRows != 2 || len(receipt.Messages) != 2 {
			return fmt.Errorf("%s stored %d messages", role, receipt.GroupRows)
		}
		for _, got := range receipt.Messages {
			want, ok := byRecipient[receipt.Npub][got.LogicalID]
			if !ok {
				return fmt.Errorf("%s stored unexpected logical id", role)
			}
			if got.EventID != want.EventID || got.Signature != want.Signature || got.CipherSHA256 != want.CipherSHA256 || got.PlainSHA256 != want.BodySHA256 {
				return fmt.Errorf("%s binding mismatch for %s", role, got.LogicalID)
			}
			if got.SenderNpub != receipts[roleAlice].Npub || got.Inserts != 1 || got.DuplicateSkips < 2 || got.Deliveries < 3 {
				return fmt.Errorf("%s accept-once mismatch %+v", role, got)
			}
		}
		if len(receipt.Negatives) < 4 {
			return fmt.Errorf("%s recorded %d negative cases", role, len(receipt.Negatives))
		}
		for _, neg := range receipt.Negatives {
			if !neg.Refused || !neg.DBUnchanged {
				return fmt.Errorf("%s negative %s was not a clean refusal", role, neg.Name)
			}
		}
	}
	if err := hashDBBodies(results[roleBob].home, man); err != nil {
		return fmt.Errorf("bob db: %w", err)
	}
	if err := hashDBBodies(results[roleCharlie].home, man); err != nil {
		return fmt.Errorf("charlie db: %w", err)
	}
	summary.GroupID = man.GroupID
	summary.LogicalIDs = append([]string(nil), man.Order...)
	summary.PIDs = pids
	summary.RelayDup = minDeliveries
	bundle := map[string]json.RawMessage{}
	for _, name := range []string{"manifest.json", "receipt-alice.json", "receipt-bob.json", "receipt-charlie.json", "negatives.json"} {
		data, err := os.ReadFile(filepath.Join(pub, name))
		if err != nil {
			return err
		}
		if strings.Contains(string(data), bodyOne) || strings.Contains(string(data), bodyTwo) || strings.Contains(string(data), foreignBody) || strings.Contains(string(data), wrongBody) {
			return fmt.Errorf("%s contains plaintext", name)
		}
		bundle[name] = data
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	summary.PublicReceipts = raw
	return nil
}

func hashDBBodies(home string, man manifest) error {
	db, err := sql.Open("sqlite", readOnlyFileDSN(filepath.Join(home, ".hyphae", "messages.db")))
	if err != nil {
		return err
	}
	defer db.Close()
	var dm int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&dm); err != nil {
		return err
	}
	if dm != 0 {
		return fmt.Errorf("DM inbox rows %d", dm)
	}
	rows, err := db.Query(`SELECT logical_id, event_id, sender_npub, body FROM groupchat_messages ORDER BY logical_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	// Pairwise NIP-44 gives each recipient a distinct event id for the same logical id.
	want := map[string]publishedMsg{}
	for _, msg := range man.Messages {
		want[msg.LogicalID+"/"+msg.EventID] = msg
	}
	count := 0
	for rows.Next() {
		var logical, eventID, sender, body string
		if err := rows.Scan(&logical, &eventID, &sender, &body); err != nil {
			return err
		}
		count++
		got, ok := want[logical+"/"+eventID]
		if !ok || sha256hex(body) != got.BodySHA256 || sender == "" {
			return fmt.Errorf("stored row does not match published binding for %s", logical)
		}
	}
	if count != 2 {
		return fmt.Errorf("row count %d", count)
	}
	return rows.Err()
}

func readOnlyFileDSN(path string) string {
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := url.Values{}
	query.Set("mode", "ro")
	u.RawQuery = query.Encode()
	return u.String()
}

func execSelftest(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestThreeProcessGroupRelay$", "-test.count=1", "-test.timeout=40s")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(cleanBaseEnv(), "HYPHAE_M1_HARNESS=selftest", "HOME="+t.TempDir())
	err := cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if err != nil && code == 0 {
		t.Fatalf("selftest wait: %v\n%s", err, scrub(stderr.String()))
	}
	if code != dropExitCode {
		t.Logf("selftest stderr:\n%s", scrub(stderr.String()))
	}
	return code
}

func cleanBaseEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + os.TempDir(),
		"LANG=" + os.Getenv("LANG"),
	}
}

func runRole(role string) int {
	if err := executeRole(role); err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %s\n", role, scrub(err.Error()))
		if errors.Is(err, errDropped) {
			return dropExitCode
		}
		return 1
	}
	return 0
}

var errDropped = errors.New("dropped recipient")

func executeRole(role string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(home, ".hyphae"), 0o700); err != nil {
		return err
	}
	sk := nostr.Generate()
	keyPath := filepath.Join(home, ".hyphae", "role.key")
	encoded := hex.EncodeToString(sk[:])
	if err := os.WriteFile(keyPath, []byte(encoded+"\n"), 0o600); err != nil {
		return err
	}
	loaded, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	sk, err = nostr.SecretKeyFromHex(strings.TrimSpace(string(loaded)))
	if err != nil {
		return err
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("key mode %o", info.Mode().Perm())
	}
	card := identityCard{Role: role, Npub: common.EncodeNpub(sk.Public()), Hex: common.PubKeyToHex(sk.Public()), PID: os.Getpid()}
	pub := os.Getenv("HYPHAE_M1_PUB")
	if err := writeJSON(filepath.Join(pub, role+".json"), card); err != nil {
		return err
	}
	mode := os.Getenv("HYPHAE_M1_MODE")
	if mode == "drop" {
		if err := waitFile(filepath.Join(pub, "drop-armed"), 5*time.Second); err != nil {
			return err
		}
	}
	db, err := storage.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	if err := os.Chmod(filepath.Join(home, ".hyphae", "messages.db"), 0o600); err != nil {
		return err
	}
	store, err := groupchat.NewStore(db)
	if err != nil {
		return err
	}
	peer := &roleProc{
		role: role, sk: sk, card: card, db: db, store: store, pub: pub, relayURL: os.Getenv("HYPHAE_M1_RELAY"),
		mode: mode, keyMode: uint32(info.Mode().Perm()), held: map[string]nostr.Event{},
		deliveries: map[string]int{}, inserts: map[string]int{}, skips: map[string]int{},
		logicalAt: map[string]int{},
	}
	return peer.run()
}

type roleProc struct {
	role       string
	sk         nostr.SecretKey
	card       identityCard
	db         *sql.DB
	store      *groupchat.Store
	pub        string
	relayURL   string
	mode       string
	keyMode    uint32
	groupID    string
	aliceNpub  string
	acceptSent bool
	activeSent bool
	invitesOut bool
	actsOut    bool
	msgsOut    bool
	negsOut    bool
	baseline   string
	sawDup     bool
	sawReorder bool
	order      []string
	held       map[string]nostr.Event
	deliveries map[string]int
	inserts    map[string]int
	skips      map[string]int
	logicalAt  map[string]int
	negatives  []negReceipt
	reserved   bool
	messages   []msgReceipt
	mallory    nostr.SecretKey
	catalog    negativeCatalog
}

func (p *roleProc) run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
	defer cancel()
	relay, err := nostr.RelayConnect(ctx, p.relayURL, nostr.RelayOptions{})
	if err != nil {
		return err
	}
	defer relay.Close()
	filter := messaging.BuildAgentMessageFilter(p.card.Hex)
	sub, err := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{Label: "m1-" + p.role})
	if err != nil {
		return err
	}
	defer sub.Unsub()
	select {
	case <-sub.EndOfStoredEvents:
	case <-time.After(3 * time.Second):
		return errors.New("subscription produced no EOSE")
	}
	if err := os.WriteFile(filepath.Join(p.pub, "subscribed-"+p.role), []byte("ok\n"), 0o600); err != nil {
		return err
	}
	peers, err := p.waitPeers(ctx)
	if err != nil {
		return err
	}
	if p.role == roleAlice {
		p.aliceNpub = p.card.Npub
	} else {
		p.aliceNpub = peers[roleAlice].Npub
	}

	deadline := time.After(20 * time.Second)
	if p.mode == "drop" && p.role == roleCharlie {
		deadline = time.After(8 * time.Second)
	}
	for {
		if err := p.progress(peers); err != nil {
			return err
		}
		if p.finished() {
			return p.writeReceipt()
		}
		select {
		case event, ok := <-sub.Events:
			if !ok {
				return errors.New("subscription closed")
			}
			if err := p.onEvent(event); err != nil {
				return err
			}
		case <-time.After(40 * time.Millisecond):
		case <-deadline:
			if p.mode == "drop" && p.role == roleCharlie && len(p.messages) == 0 {
				return errDropped
			}
			return fmt.Errorf("role deadline group=%s invites=%t acts=%t msgs=%t negs=%t messages=%d dup=%t reorder=%t reserved=%t negatives=%d held=%d deliveries=%d", p.groupID, p.invitesOut, p.actsOut, p.msgsOut, p.negsOut, len(p.messages), p.sawDup, p.sawReorder, p.reserved, len(p.negatives), len(p.held), len(p.deliveries))
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (p *roleProc) finished() bool {
	if p.mode == "drop" {
		return false
	}
	if p.role == roleAlice {
		return p.negsOut && fileExists(filepath.Join(p.pub, "receipt-"+roleBob+".json")) && fileExists(filepath.Join(p.pub, "receipt-"+roleCharlie+".json"))
	}
	return len(p.messages) == 2 && p.sawDup && p.sawReorder && p.reserved && p.negativesComplete()
}

func (p *roleProc) caseApplies(want negativeCase) bool {
	if want.Expect == "verify-other" {
		return want.TargetNpub == p.card.Npub
	}
	return want.TargetNpub == "" || want.TargetNpub == p.card.Npub
}

func (p *roleProc) negativesComplete() bool {
	if !fileExists(filepath.Join(p.pub, "negatives.json")) {
		return false
	}
	if len(p.catalog.Cases) == 0 {
		if err := readJSON(filepath.Join(p.pub, "negatives.json"), &p.catalog); err != nil {
			return false
		}
	}
	byID := map[string]negReceipt{}
	for _, neg := range p.negatives {
		byID[neg.EventID] = neg
	}
	matched := 0
	for _, want := range p.catalog.Cases {
		if !p.caseApplies(want) {
			continue
		}
		got, ok := byID[want.EventID]
		if !ok || !got.Refused || !got.DBUnchanged {
			return false
		}
		matched++
	}
	return matched > 0
}

func (p *roleProc) waitPeers(ctx context.Context) (map[string]identityCard, error) {
	want := []string{roleAlice, roleBob, roleCharlie}
	out := map[string]identityCard{}
	for _, role := range want {
		var card identityCard
		if err := waitJSON(filepath.Join(p.pub, role+".json"), &card, 5*time.Second); err != nil {
			return nil, err
		}
		out[role] = card
	}
	for _, role := range want {
		if role == p.role {
			continue
		}
		if err := waitFile(filepath.Join(p.pub, "subscribed-"+role), 5*time.Second); err != nil {
			return nil, err
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return out, nil
}

func (p *roleProc) progress(peers map[string]identityCard) error {
	if p.role != roleAlice {
		return p.memberProgress()
	}
	if !p.invitesOut {
		if err := p.publishInvites(peers); err != nil {
			return err
		}
	}
	if p.actsOut && !p.msgsOut && fileExists(filepath.Join(p.pub, "active-"+roleBob)) && fileExists(filepath.Join(p.pub, "active-"+roleCharlie)) {
		if err := p.publishMessages(peers); err != nil {
			return err
		}
	}
	if p.msgsOut && !p.negsOut && fileExists(filepath.Join(p.pub, "messages-ready-"+roleBob)) && fileExists(filepath.Join(p.pub, "messages-ready-"+roleCharlie)) {
		if err := p.publishNegatives(peers); err != nil {
			return err
		}
	}
	return nil
}

func (p *roleProc) memberProgress() error {
	if len(p.messages) == 2 && p.sawDup && p.baseline == "" {
		hash, err := p.rowHash()
		if err != nil {
			return err
		}
		p.baseline = hash
		if err := os.WriteFile(filepath.Join(p.pub, "messages-ready-"+p.role), []byte("ok\n"), 0o600); err != nil {
			return err
		}
	}
	if p.baseline != "" && fileExists(filepath.Join(p.pub, "manifest.json")) && fileExists(filepath.Join(p.pub, "negatives.json")) {
		return p.observeCatalog()
	}
	return nil
}

func (p *roleProc) publishInvites(peers map[string]identityCard) error {
	draft, err := p.store.CreateGroup("relay-proof", p.card.Npub, []string{peers[roleBob].Npub, peers[roleCharlie].Npub})
	if err != nil {
		return err
	}
	p.groupID = draft.Group.ID
	for _, invite := range draft.Invitations {
		if _, err := p.publishEnvelope(invite, npubKey(peers, invite.InviteeNpub), invite.InviteeNpub); err != nil {
			return err
		}
	}
	p.invitesOut = true
	fmt.Fprintf(os.Stderr, "phase invites group=%s\n", p.groupID)
	return nil
}

func (p *roleProc) publishActivations(activations []groupchat.Envelope, peers map[string]identityCard) error {
	if p.actsOut {
		return nil
	}
	p.actsOut = true
	for _, activation := range activations {
		event, err := p.publishEnvelope(activation, npubKey(peers, activation.InviteeNpub), activation.InviteeNpub)
		if err != nil {
			return err
		}
		if err := p.store.MarkActivationQueued(p.card.Npub, p.groupID, activation.InviteID, event.ID.Hex(), true); err != nil {
			return err
		}
	}
	return nil
}

func (p *roleProc) publishMessages(peers map[string]identityCard) error {
	logicals := make([]string, 0, 2)
	bodies := []string{bodyOne, bodyTwo}
	var man manifest
	man.GroupID = p.groupID
	for i, body := range bodies {
		logical, err := groupchat.NewOpaqueID()
		if err != nil {
			return err
		}
		logicals = append(logicals, logical)
		if _, err := p.store.StoreLocalMessage(p.card.Npub, p.groupID, logical, body, time.Now().Unix()); err != nil {
			return err
		}
		env := groupchat.Envelope{Type: groupchat.EnvelopeMessage, Version: groupchat.Version, GroupID: p.groupID, LogicalID: logical, Body: body}
		for _, role := range []string{roleBob, roleCharlie} {
			event, err := p.publishEnvelope(env, nostr.MustPubKeyFromHex(peers[role].Hex), peers[role].Npub)
			if err != nil {
				return err
			}
			raw, _ := json.Marshal(event)
			man.Messages = append(man.Messages, publishedMsg{
				LogicalID: logical, BodySHA256: sha256hex(body), Recipient: peers[role].Npub,
				EventID: event.ID.Hex(), Signature: hex.EncodeToString(event.Sig[:]), CipherSHA256: sha256hex(string(rawContent(event))),
			})
			_ = i
			_ = raw
		}
	}
	man.Order = logicals
	if err := writeJSON(filepath.Join(p.pub, "manifest.json"), man); err != nil {
		return err
	}
	p.msgsOut = true
	return nil
}

func (p *roleProc) publishNegatives(peers map[string]identityCard) error {
	p.mallory = nostr.Generate()
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(home, ".hyphae", "mallory.key"), []byte(hex.EncodeToString(p.mallory[:])+"\n"), 0o600); err != nil {
		return err
	}
	var catalog negativeCatalog
	add := func(name, target string, event nostr.Event, expect string, accepted bool, include bool) error {
		item := negativeCase{Name: name, EventID: event.ID.Hex(), TargetNpub: target, Expect: expect, RelayAccepted: accepted}
		if include {
			raw, err := json.Marshal(event)
			if err != nil {
				return err
			}
			item.Event = raw
		}
		catalog.Cases = append(catalog.Cases, item)
		return nil
	}
	for _, role := range []string{roleBob, roleCharlie} {
		wrongID, err := groupchat.NewOpaqueID()
		if err != nil {
			return err
		}
		logical, err := groupchat.NewOpaqueID()
		if err != nil {
			return err
		}
		env := groupchat.Envelope{Type: groupchat.EnvelopeMessage, Version: groupchat.Version, GroupID: wrongID, LogicalID: logical, Body: wrongBody}
		event, err := p.publishEnvelope(env, nostr.MustPubKeyFromHex(peers[role].Hex), peers[role].Npub)
		if err != nil {
			return err
		}
		if err := add("wrong-group-"+role, peers[role].Npub, event, "receive-refuse", true, false); err != nil {
			return err
		}
		foreignLogical, err := groupchat.NewOpaqueID()
		if err != nil {
			return err
		}
		foreignEnv := groupchat.Envelope{Type: groupchat.EnvelopeMessage, Version: groupchat.Version, GroupID: p.groupID, LogicalID: foreignLogical, Body: foreignBody}
		foreignEvent, err := publishSigned(p.mallory, nostr.MustPubKeyFromHex(peers[role].Hex), peers[role].Npub, foreignEnv, p.relayURL)
		if err != nil {
			return err
		}
		if err := add("foreign-sender-"+role, peers[role].Npub, foreignEvent, "receive-refuse", true, false); err != nil {
			return err
		}
	}
	var man manifest
	if err := readJSON(filepath.Join(p.pub, "manifest.json"), &man); err != nil {
		return err
	}
	for _, msg := range man.Messages {
		other := peers[roleCharlie].Npub
		if msg.Recipient == other {
			other = peers[roleBob].Npub
		}
		item := negativeCase{Name: "wrong-recipient-" + msg.EventID[:12], EventID: msg.EventID, TargetNpub: other, Expect: "verify-other", RelayAccepted: true}
		catalog.Cases = append(catalog.Cases, item)
	}
	sample, err := p.sampleEvent(peers[roleBob])
	if err != nil {
		return err
	}
	claimed := sample
	malloryShell, err := messaging.BuildAgentMessageEvent(p.mallory, nostr.MustPubKeyFromHex(peers[roleBob].Hex), "placeholder", nostr.Now())
	if err != nil {
		return err
	}
	claimed.Content = sample.Content
	claimed.PubKey = p.mallory.Public()
	claimed.CreatedAt = malloryShell.CreatedAt
	claimed.Kind = malloryShell.Kind
	claimed.Tags = malloryShell.Tags
	if err := claimed.Sign(p.mallory); err != nil {
		return err
	}
	if err := publishRaw(p.relayURL, claimed); err != nil {
		return fmt.Errorf("copied claimed sender was not relay-accepted: %w", err)
	}
	if err := add("copied-claimed-sender", peers[roleBob].Npub, claimed, "verify-refuse", true, false); err != nil {
		return err
	}
	copied := claimed
	copied.PubKey = p.sk.Public()
	copiedRelayErr := publishRaw(p.relayURL, copied)
	if copiedRelayErr == nil {
		return errors.New("copied pubkey was accepted by the relay")
	}
	if err := add("copied-pubkey", peers[roleBob].Npub, copied, "verify-refuse", false, true); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(p.pub, "negatives.json"), catalog); err != nil {
		return err
	}
	p.negsOut = true
	return p.writeReceipt()
}

func (p *roleProc) sampleEvent(peer identityCard) (nostr.Event, error) {
	logical, err := groupchat.NewOpaqueID()
	if err != nil {
		return nostr.Event{}, err
	}
	env := groupchat.Envelope{Type: groupchat.EnvelopeMessage, Version: groupchat.Version, GroupID: p.groupID, LogicalID: logical, Body: "sample-cipher"}
	return buildSigned(p.sk, nostr.MustPubKeyFromHex(peer.Hex), env)
}

func (p *roleProc) publishEnvelope(env groupchat.Envelope, recipient nostr.PubKey, recipientNpub string) (nostr.Event, error) {
	return publishSigned(p.sk, recipient, recipientNpub, env, p.relayURL)
}

func (p *roleProc) onEvent(event nostr.Event) error {
	p.deliveries[event.ID.Hex()]++
	verified, err := groupchat.VerifyIncoming(event, p.sk)
	if err != nil {
		return p.noteVerifyRefusal(event, err)
	}
	if err := messaging.RejectReservedGroupPayload(verified.Plaintext()); err != nil {
		p.reserved = true
	} else {
		return errors.New("DM inbox guard accepted a reserved group payload")
	}
	env, err := groupchat.Decode(verified.Plaintext())
	if err != nil {
		return err
	}
	switch env.Type {
	case groupchat.EnvelopeInvite:
		return p.onInvite(verified, env)
	case groupchat.EnvelopeAccept:
		return p.onAccept(verified)
	case groupchat.EnvelopeActivate:
		return p.onActivation(event, verified)
	case groupchat.EnvelopeMessage:
		return p.onMessage(event, verified, env)
	default:
		return p.noteReceiveRefusal(event, "unsupported", errors.New("unsupported envelope"))
	}
}

func (p *roleProc) onInvite(verified groupchat.VerifiedIncoming, env groupchat.Envelope) error {
	if _, err := p.store.ReceiveInvite(verified); err != nil {
		return err
	}
	p.groupID = env.GroupID
	if p.acceptSent {
		return nil
	}
	accept, err := p.store.AcceptInvite(env.InviteID, p.card.Npub)
	if err != nil {
		return err
	}
	p.acceptSent = true
	_, err = publishSigned(p.sk, mustKey(p.aliceNpub), p.aliceNpub, accept, p.relayURL)
	fmt.Fprintf(os.Stderr, "phase accept-sent err=%v\n", err)
	return err
}

func (p *roleProc) onAccept(verified groupchat.VerifiedIncoming) error {
	activations, err := p.store.ReceiveAcceptance(verified)
	if err != nil {
		return err
	}
	if len(activations) == 0 || p.actsOut {
		return nil
	}
	peers, err := p.readPeerCards()
	if err != nil {
		return err
	}
	return p.publishActivations(activations, peers)
}

func (p *roleProc) onActivation(event nostr.Event, verified groupchat.VerifiedIncoming) error {
	applied, err := p.store.ReceiveActivation(verified)
	if err != nil {
		if errors.Is(err, groupchat.ErrInvalidTransition) || errors.Is(err, groupchat.ErrInviteNotFound) {
			p.held[event.ID.Hex()] = event
			return nil
		}
		return err
	}
	_ = applied
	return p.afterProgress()
}

func (p *roleProc) onMessage(event nostr.Event, verified groupchat.VerifiedIncoming, env groupchat.Envelope) error {
	before, err := p.rowHash()
	if err != nil {
		return err
	}
	inserted, err := p.store.ReceiveMessage(verified)
	if err != nil {
		if errors.Is(err, groupchat.ErrGroupNotActive) && (p.groupID == "" || env.GroupID == p.groupID) {
			p.held[event.ID.Hex()] = event
			return nil
		}
		return p.noteReceiveRefusal(event, classErr(err), err)
	}
	if inserted {
		p.inserts[env.LogicalID]++
		if _, seen := p.logicalAt[env.LogicalID]; !seen {
			p.logicalAt[env.LogicalID] = len(p.order)
			p.order = append(p.order, env.LogicalID)
		}
	} else {
		p.skips[env.LogicalID]++
	}
	if p.deliveries[event.ID.Hex()] >= 3 && p.skips[env.LogicalID] >= 2 {
		p.sawDup = true
	}
	if len(p.order) >= 2 && p.order[0] != "" {
		var man manifest
		if readJSON(filepath.Join(p.pub, "manifest.json"), &man) == nil && len(man.Order) == 2 {
			if p.order[0] == man.Order[1] && p.order[1] == man.Order[0] {
				p.sawReorder = true
			}
		}
	}
	p.upsertMessage(event, verified, env)
	after, err := p.rowHash()
	if err != nil {
		return err
	}
	if p.baseline != "" && after != before && inserted {
		return errors.New("message insert changed rows after the accept-once baseline")
	}
	return p.afterProgress()
}

func (p *roleProc) afterProgress() error {
	var kept []nostr.Event
	for _, event := range p.held {
		verified, err := groupchat.VerifyIncoming(event, p.sk)
		if err != nil {
			continue
		}
		env, err := groupchat.Decode(verified.Plaintext())
		if err != nil {
			continue
		}
		if env.Type == groupchat.EnvelopeActivate {
			if _, err := p.store.ReceiveActivation(verified); err != nil {
				kept = append(kept, event)
			}
			continue
		}
		if env.Type == groupchat.EnvelopeMessage {
			inserted, err := p.store.ReceiveMessage(verified)
			if err != nil {
				kept = append(kept, event)
				continue
			}
			if inserted {
				p.inserts[env.LogicalID]++
				if _, seen := p.logicalAt[env.LogicalID]; !seen {
					p.order = append(p.order, env.LogicalID)
				}
			} else {
				p.skips[env.LogicalID]++
			}
			p.upsertMessage(event, verified, env)
		}
	}
	p.held = map[string]nostr.Event{}
	for _, event := range kept {
		p.held[event.ID.Hex()] = event
	}
	if p.role != roleAlice && !p.activeSent && p.groupID != "" {
		group, err := p.store.GetGroup(p.card.Npub, p.groupID)
		if err == nil && group.State == groupchat.StateActive {
			p.activeSent = true
			if err := os.WriteFile(filepath.Join(p.pub, "active-"+p.role), []byte("ok\n"), 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *roleProc) upsertMessage(event nostr.Event, verified groupchat.VerifiedIncoming, env groupchat.Envelope) {
	item := msgReceipt{
		LogicalID: env.LogicalID, EventID: event.ID.Hex(), Signature: hex.EncodeToString(event.Sig[:]),
		CipherSHA256: sha256hex(event.Content), PlainSHA256: sha256hex(verified.Plaintext()),
		SenderNpub: verified.SenderNpub(), RecipientNpub: verified.RecipientNpub(),
		Inserts: p.inserts[env.LogicalID], DuplicateSkips: p.skips[env.LogicalID], Deliveries: p.deliveries[event.ID.Hex()],
	}
	// Plaintext() is the full envelope. Hash the decoded body for the parent check.
	item.PlainSHA256 = sha256hex(env.Body)
	for i := range p.messages {
		if p.messages[i].LogicalID == item.LogicalID {
			p.messages[i] = item
			return
		}
	}
	p.messages = append(p.messages, item)
}

func (p *roleProc) noteVerifyRefusal(event nostr.Event, cause error) error {
	return p.recordNegative(event, "verify-refuse", classErr(cause), cause)
}

func (p *roleProc) noteReceiveRefusal(event nostr.Event, class string, cause error) error {
	return p.recordNegative(event, "receive-refuse", class, cause)
}

func (p *roleProc) recordNegative(event nostr.Event, class, errClass string, cause error) error {
	if p.baseline == "" {
		return nil
	}
	after, err := p.rowHash()
	if err != nil {
		return err
	}
	p.rememberNeg(event, class, errClass, after == p.baseline)
	_ = cause
	return nil
}

func (p *roleProc) rememberNeg(event nostr.Event, class, errClass string, unchanged bool) {
	name := p.negativeName(event.ID.Hex())
	if name == "" {
		name = class + "-" + event.ID.Hex()[:8]
	}
	item := negReceipt{Name: name, EventID: event.ID.Hex(), Refused: true, DBUnchanged: unchanged, RelayStored: true, ErrorClass: errClass}
	for i := range p.negatives {
		if p.negatives[i].EventID == item.EventID {
			p.negatives[i] = item
			return
		}
	}
	p.negatives = append(p.negatives, item)
}

func (p *roleProc) negativeName(eventID string) string {
	if len(p.catalog.Cases) == 0 && fileExists(filepath.Join(p.pub, "negatives.json")) {
		_ = readJSON(filepath.Join(p.pub, "negatives.json"), &p.catalog)
	}
	for _, item := range p.catalog.Cases {
		if item.EventID == eventID {
			return item.Name
		}
	}
	return ""
}

func (p *roleProc) observeCatalog() error {
	if len(p.catalog.Cases) == 0 {
		if err := readJSON(filepath.Join(p.pub, "negatives.json"), &p.catalog); err != nil {
			return err
		}
	}
	for _, item := range p.catalog.Cases {
		if p.hasNegative(item.EventID) {
			continue
		}
		if item.Expect == "verify-other" {
			if item.TargetNpub != p.card.Npub {
				continue
			}
			if err := p.fetchAndRefuse(item); err != nil {
				return err
			}
			continue
		}
		if item.TargetNpub != "" && item.TargetNpub != p.card.Npub {
			continue
		}
		if item.Event != nil {
			var event nostr.Event
			if err := json.Unmarshal(item.Event, &event); err != nil {
				return err
			}
			before := p.baseline
			_, verr := groupchat.VerifyIncoming(event, p.sk)
			after, err := p.rowHash()
			if err != nil {
				return err
			}
			if verr == nil {
				return fmt.Errorf("%s verified", item.Name)
			}
			p.negatives = append(p.negatives, negReceipt{Name: item.Name, EventID: item.EventID, Refused: true, DBUnchanged: after == before, RelayStored: false, ErrorClass: classErr(verr)})
		}
	}
	return nil
}

func (p *roleProc) fetchAndRefuse(item negativeCase) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	relay, err := nostr.RelayConnect(ctx, p.relayURL, nostr.RelayOptions{})
	if err != nil {
		return err
	}
	defer relay.Close()
	id, err := nostr.IDFromHex(item.EventID)
	if err != nil {
		return err
	}
	sub, err := relay.Subscribe(ctx, nostr.Filter{IDs: []nostr.ID{id}}, nostr.SubscriptionOptions{Label: "neg-" + p.role})
	if err != nil {
		return err
	}
	defer sub.Unsub()
	select {
	case event := <-sub.Events:
		before := p.baseline
		_, verr := groupchat.VerifyIncoming(event, p.sk)
		after, err := p.rowHash()
		if err != nil {
			return err
		}
		if verr == nil || after != before {
			return fmt.Errorf("%s was accepted or mutated the db", item.Name)
		}
		p.negatives = append(p.negatives, negReceipt{Name: item.Name, EventID: item.EventID, Refused: true, DBUnchanged: true, RelayStored: true, ErrorClass: classErr(verr)})
		return nil
	case <-time.After(3 * time.Second):
		return fmt.Errorf("did not fetch %s", item.Name)
	}
}

func (p *roleProc) writeReceipt() error {
	dm, groupRows, err := p.counts()
	if err != nil {
		return err
	}
	receipt := peerReceipt{
		Role: p.role, PID: os.Getpid(), PPID: os.Getppid(), Npub: p.card.Npub, Hex: p.card.Hex,
		GroupID: p.groupID, Messages: p.messages, Negatives: p.namedNegatives(), DMInboxRows: dm,
		GroupRows: groupRows, KeyFileMode: p.keyMode, SawDuplicate: p.sawDup, SawReorder: p.sawReorder,
		FirstLogical: p.order, ReservedRefused: p.reserved,
	}
	if p.role == roleAlice {
		receipt.ReservedRefused = true
		receipt.SawDuplicate = true
		receipt.SawReorder = true
	}
	return writeJSON(filepath.Join(p.pub, "receipt-"+p.role+".json"), receipt)
}

func (p *roleProc) hasNegative(eventID string) bool {
	for _, neg := range p.negatives {
		if neg.EventID == eventID {
			return true
		}
	}
	return false
}

func (p *roleProc) namedNegatives() []negReceipt {
	out := make([]negReceipt, 0, len(p.negatives))
	seen := map[string]bool{}
	for _, neg := range p.negatives {
		if neg.EventID == "" || seen[neg.EventID] {
			continue
		}
		if name := p.negativeName(neg.EventID); name != "" {
			neg.Name = name
		}
		seen[neg.EventID] = true
		out = append(out, neg)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].EventID < out[j].EventID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (p *roleProc) readPeerCards() (map[string]identityCard, error) {
	out := map[string]identityCard{}
	for _, role := range []string{roleAlice, roleBob, roleCharlie} {
		var card identityCard
		if err := readJSON(filepath.Join(p.pub, role+".json"), &card); err != nil {
			return nil, err
		}
		out[role] = card
	}
	return out, nil
}

func (p *roleProc) counts() (int, int, error) {
	var dm, groupRows int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&dm); err != nil {
		return 0, 0, err
	}
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM groupchat_messages`).Scan(&groupRows); err != nil {
		return 0, 0, err
	}
	return dm, groupRows, nil
}

func (p *roleProc) rowHash() (string, error) {
	rows, err := p.db.Query(`SELECT logical_id, COALESCE(event_id,''), sender_npub, body FROM groupchat_messages ORDER BY logical_id, event_id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var logical, eventID, sender, body string
		if err := rows.Scan(&logical, &eventID, &sender, &body); err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s|%s|%s|%s\n", logical, eventID, sender, sha256hex(body))
	}
	var dm int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&dm); err != nil {
		return "", err
	}
	fmt.Fprintf(h, "dm=%d\n", dm)
	return hex.EncodeToString(h.Sum(nil)), rows.Err()
}

func publishSigned(sender nostr.SecretKey, recipient nostr.PubKey, recipientNpub string, env groupchat.Envelope, relayURL string) (nostr.Event, error) {
	event, err := buildSigned(sender, recipient, env)
	if err != nil {
		return nostr.Event{}, err
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return nostr.Event{}, err
	}
	entry, _, err := messaging.EnqueueGroupOutboxEntry(string(raw), recipientNpub, []string{relayURL}, 3)
	if err != nil {
		return nostr.Event{}, err
	}
	id, senderNpub, err := messaging.VerifiedGroupEventIdentity([]byte(entry.EventJSON), entry)
	if err != nil {
		return nostr.Event{}, err
	}
	if id != event.ID.Hex() || senderNpub != common.EncodeNpub(sender.Public()) {
		return nostr.Event{}, errors.New("outbox sender binding does not match the signed event")
	}
	decoded, err := messaging.DecodeVerifiedGroupEvent([]byte(entry.EventJSON), entry)
	if err != nil {
		return nostr.Event{}, err
	}
	if err := publishRaw(relayURL, *decoded); err != nil {
		return nostr.Event{}, err
	}
	return *decoded, nil
}

func buildSigned(sender nostr.SecretKey, recipient nostr.PubKey, env groupchat.Envelope) (nostr.Event, error) {
	encoded, err := groupchat.Encode(env)
	if err != nil {
		return nostr.Event{}, err
	}
	event, err := messaging.BuildAgentMessageEvent(sender, recipient, encoded, nostr.Now())
	if err != nil {
		return nostr.Event{}, err
	}
	return *event, nil
}

func publishRaw(relayURL string, event nostr.Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	results := common.PublishToRelays(ctx, &event, []string{relayURL})
	if err := results[relayURL]; err != nil {
		return err
	}
	return nil
}

func npubKey(peers map[string]identityCard, npub string) nostr.PubKey {
	for _, card := range peers {
		if card.Npub == npub {
			return nostr.MustPubKeyFromHex(card.Hex)
		}
	}
	return nostr.PubKey{}
}

func mustKey(npub string) nostr.PubKey {
	pk, err := common.ParsePublicKey(npub)
	if err != nil {
		panic(err)
	}
	return pk
}

func rawContent(event nostr.Event) string { return event.Content }

func classErr(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, groupchat.ErrGroupNotFound):
		return "group-not-found"
	case errors.Is(err, groupchat.ErrProtocolMismatch):
		return "protocol-mismatch"
	case errors.Is(err, groupchat.ErrGroupNotActive):
		return "group-not-active"
	default:
		if strings.Contains(err.Error(), "signature") || strings.Contains(err.Error(), "recipient") || strings.Contains(err.Error(), "decrypt") || strings.Contains(err.Error(), "invalid") {
			return "verify"
		}
		return "refused"
	}
}

func sha256hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func readJSON(path string, dest any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

func waitJSON(path string, dest any, timeout time.Duration) error {
	if err := waitFile(path, timeout); err != nil {
		return err
	}
	return readJSON(path, dest)
}

func waitFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if fileExists(path) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s", filepath.Base(path))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func waitUntil(timeout time.Duration, ready func() bool) error {
	deadline := time.Now().Add(timeout)
	for {
		if ready() {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("condition timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

func scrub(value string) string {
	value = strings.ReplaceAll(value, bodyOne, "[plaintext]")
	value = strings.ReplaceAll(value, bodyTwo, "[plaintext]")
	value = strings.ReplaceAll(value, foreignBody, "[plaintext]")
	value = strings.ReplaceAll(value, wrongBody, "[plaintext]")
	return value
}

type relayRecord struct {
	Accepted   bool
	Deliveries int
	PubKey     string
	Signature  string
	CipherSHA  string
}

type misRelay struct {
	URL      string
	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
	records  map[string]*relayRecord
	stored   []nostr.Event
	dropHex  string
	conns    map[*websocket.Conn]*relayConn
}

type relayConn struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	subs    map[string]*relaySub
}

type relaySub struct {
	id      string
	filters []nostr.Filter
	pending []nostr.Event
	timer   *time.Timer
	owner   *relayConn
}

func newMisRelay(dropHex string) *misRelay {
	relay := &misRelay{
		records: map[string]*relayRecord{},
		dropHex: dropHex,
		conns:   map[*websocket.Conn]*relayConn{},
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	relay.listener = ln
	relay.URL = "ws://" + ln.Addr().String()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		relay.serve(conn)
	})
	relay.server = &http.Server{Handler: mux}
	go relay.server.Serve(ln)
	return relay
}

func (r *misRelay) close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = r.server.Shutdown(ctx)
	_ = r.listener.Close()
}

func (r *misRelay) setDropHex(hexKey string) {
	r.mu.Lock()
	r.dropHex = hexKey
	r.mu.Unlock()
}

func (r *misRelay) acceptedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, rec := range r.records {
		if rec.Accepted {
			n++
		}
	}
	return n
}

func (r *misRelay) accepted(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.records[id]
	return rec != nil && rec.Accepted
}

func (r *misRelay) deliveryCount(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec := r.records[id]; rec != nil {
		return rec.Deliveries
	}
	return 0
}

func (r *misRelay) serve(conn *websocket.Conn) {
	defer conn.Close()
	state := &relayConn{conn: conn, subs: map[string]*relaySub{}}
	r.mu.Lock()
	r.conns[conn] = state
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.conns, conn)
		r.mu.Unlock()
	}()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		env, err := nostr.ParseMessage(string(data))
		if err != nil {
			continue
		}
		switch msg := env.(type) {
		case *nostr.EventEnvelope:
			r.acceptEvent(state, msg.Event)
		case *nostr.ReqEnvelope:
			r.addSub(state, msg)
		case *nostr.CloseEnvelope:
			r.mu.Lock()
			delete(state.subs, string(*msg))
			r.mu.Unlock()
		}
	}
}

func (r *misRelay) acceptEvent(origin *relayConn, event nostr.Event) {
	ok := event.CheckID() && event.VerifySignature()
	reason := ""
	if !ok {
		reason = "invalid: id or signature"
	}
	frame, _ := nostr.OKEnvelope{EventID: event.ID, OK: ok, Reason: reason}.MarshalJSON()
	origin.writeMu.Lock()
	_ = origin.conn.WriteMessage(websocket.TextMessage, frame)
	origin.writeMu.Unlock()
	if !ok {
		return
	}
	r.mu.Lock()
	rec := r.records[event.ID.Hex()]
	if rec == nil {
		rec = &relayRecord{}
		r.records[event.ID.Hex()] = rec
	}
	rec.Accepted = true
	rec.PubKey = hex.EncodeToString(event.PubKey[:])
	rec.Signature = hex.EncodeToString(event.Sig[:])
	rec.CipherSHA = sha256hex(event.Content)
	if rec.Deliveries == 0 && !eventStored(r.stored, event.ID.Hex()) {
		r.stored = append(r.stored, event)
	}
	drop := r.dropped(event)
	var subs []*relaySub
	for _, state := range r.conns {
		for _, sub := range state.subs {
			if !drop && filtersMatch(sub.filters, event) {
				subs = append(subs, sub)
			}
		}
	}
	r.mu.Unlock()
	for _, sub := range subs {
		r.queue(sub, event)
	}
}

func (r *misRelay) dropped(event nostr.Event) bool {
	if r.dropHex == "" {
		return false
	}
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "p" && tag[1] == r.dropHex {
			return true
		}
	}
	return false
}

func filtersMatch(filters []nostr.Filter, event nostr.Event) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		if filter.Matches(event) {
			return true
		}
	}
	return false
}

func eventStored(events []nostr.Event, id string) bool {
	for _, event := range events {
		if event.ID.Hex() == id {
			return true
		}
	}
	return false
}

func (r *misRelay) addSub(state *relayConn, req *nostr.ReqEnvelope) {
	sub := &relaySub{id: req.SubscriptionID, filters: append([]nostr.Filter(nil), req.Filters...), owner: state}
	r.mu.Lock()
	state.subs[sub.id] = sub
	var backlog []nostr.Event
	for _, event := range r.stored {
		if !r.dropped(event) && filtersMatch(sub.filters, event) {
			backlog = append(backlog, event)
		}
	}
	r.mu.Unlock()
	for _, event := range backlog {
		r.queue(sub, event)
	}
	frame, _ := nostr.EOSEEnvelope{SubscriptionID: sub.id}.MarshalJSON()
	state.writeMu.Lock()
	_ = state.conn.WriteMessage(websocket.TextMessage, frame)
	state.writeMu.Unlock()
}

func (r *misRelay) queue(sub *relaySub, event nostr.Event) {
	r.mu.Lock()
	sub.pending = append(sub.pending, event)
	if sub.timer != nil {
		sub.timer.Stop()
	}
	sub.timer = time.AfterFunc(120*time.Millisecond, func() {
		r.flush(sub)
	})
	r.mu.Unlock()
}

func (r *misRelay) flush(sub *relaySub) {
	r.mu.Lock()
	batch := append([]nostr.Event(nil), sub.pending...)
	sub.pending = nil
	owner := sub.owner
	r.mu.Unlock()
	if len(batch) == 0 || owner == nil {
		return
	}
	reversed := make([]nostr.Event, len(batch))
	for i := range batch {
		reversed[len(batch)-1-i] = batch[i]
	}
	send := func(event nostr.Event) {
		id := sub.id
		frame, err := nostr.EventEnvelope{SubscriptionID: &id, Event: event}.MarshalJSON()
		if err != nil {
			return
		}
		owner.writeMu.Lock()
		err = owner.conn.WriteMessage(websocket.TextMessage, frame)
		owner.writeMu.Unlock()
		if err != nil {
			return
		}
		r.mu.Lock()
		if rec := r.records[event.ID.Hex()]; rec != nil {
			rec.Deliveries++
		}
		r.mu.Unlock()
	}
	for _, event := range reversed {
		send(event)
		send(event)
	}
	for _, event := range reversed {
		send(event)
	}
}

func writeRelayReport(t *testing.T, summaries []runSummary) {
	head, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	status, _ := exec.Command("git", "status", "--porcelain").Output()
	diff, _ := exec.Command("git", "diff", "--stat", "HEAD").Output()
	var b strings.Builder
	fmt.Fprintf(&b, "# Hyphae three-process group relay proof\n\n")
	fmt.Fprintf(&b, "Freeze HEAD: %s\n\n", strings.TrimSpace(string(head)))
	fmt.Fprintf(&b, "This harness is a test helper under tests/e2e. It is not a released group CLI, TUI, daemon, or deployment.\n\n")
	fmt.Fprintf(&b, "Gap: AttemptSendRouted refuses a group outbox entry with ErrGroupRouteHandlerMissing unless the caller supplies a handler. No exported production API creates prepared fanout rows. The harness does not invent that caller. It publishes with common.PublishToRelays only after messaging.VerifiedGroupEventIdentity binds the signed event.\n\n")
	fmt.Fprintf(&b, "## Runs\n\n")
	for _, summary := range summaries {
		pub := summary.PublicReceipts
		summary.PublicReceipts = nil
		raw, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Fprintf(&b, "### run %d %s\n\n```json\n%s\n```\n\n", summary.Run, summary.Mode, raw)
		if len(pub) > 0 {
			fmt.Fprintf(&b, "Public receipts (event ids, signatures, ciphertext hashes, pids; no keys, no plaintext):\n\n```json\n%s\n```\n\n", pub)
		}
	}
	fmt.Fprintf(&b, "\n## Tree\n\n```\n%s```\n\n```\n%s```\n", status, diff)
	fmt.Fprintf(&b, "\nTest result: %v\n", !t.Failed())
	fmt.Fprintf(&b, "\nSELFTEST is the re-exec of this harness with HYPHAE_M1_HARNESS=selftest. The relay drops Charlie-addressed events. Charlie must exit %d. A zero exit fails the parent.\n", dropExitCode)
	_ = os.WriteFile("/tmp/hyphae-grok-three-process-relay-report.md", []byte(b.String()), 0o600)
}
