package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/messaging"
)

func TestReadyGuardDoesNotProbePartialKey(t *testing.T) {
	if role := os.Getenv("HYPHAE_M1_ROLE"); role != "" {
		os.Exit(runRole(role))
	}
	t.Setenv("HYPHAE_M1_KEY_FAULT", "hold")
	root := t.TempDir()
	home := filepath.Join(root, "bob")
	pub := filepath.Join(root, "pub")
	release := filepath.Join(root, "release")
	settle := filepath.Join(root, "settle")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pub, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestReadyGuardDoesNotProbePartialKey$", "-test.count=1", "-test.timeout=30s")
	cmd.Dir = home
	cmd.Env = []string{
		"HOME=" + home,
		"TMPDIR=" + os.TempDir(),
		"PATH=" + os.Getenv("PATH"),
		"LANG=" + os.Getenv("LANG"),
		"HYPHAE_M1_ROLE=" + roleBob,
		"HYPHAE_M1_RELAY=ws://127.0.0.1:1",
		"HYPHAE_M1_PUB=" + pub,
		"HYPHAE_M1_MODE=positive",
		"HYPHAE_M1_KEY_FAULT=hold",
		"HYPHAE_M1_KEY_RELEASE=" + release,
		"HYPHAE_M1_KEY_SETTLE=" + settle,
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.WriteFile(release, []byte("go\n"), 0o600)
		_ = os.WriteFile(settle, []byte("go\n"), 0o600)
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()
	holdPath := filepath.Join(home, ".hyphae", "key-hold.json")
	if err := waitFile(holdPath, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(home, ".hyphae", "role.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("held key size %d mode %o", info.Size(), info.Mode().Perm())
	}
	if fileExists(filepath.Join(home, ".hyphae", "key-ready.json")) {
		t.Fatal("ready signal existed while the key file was empty")
	}
	if fileExists(filepath.Join(home, ".hyphae", "ps-probed.json")) {
		t.Fatal("ps inspection ran before the key was ready")
	}
	done := make(chan error, 1)
	go func() {
		done <- assertNoKeyInProcessEnv([]scenarioChild{{
			role: roleBob, home: home, cmd: cmd,
		}})
	}()
	select {
	case err := <-done:
		t.Fatalf("parent probed before release: %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	if fileExists(filepath.Join(home, ".hyphae", "ps-probed.json")) {
		t.Fatal("ps inspection ran while the key file was empty")
	}
	if err := os.WriteFile(release, []byte("go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ready guard did not finish after the key write completed")
	}
	probeRaw, err := os.ReadFile(filepath.Join(home, ".hyphae", "ps-probed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		NeedleLen int  `json:"needle_len"`
		Leak      bool `json:"leak"`
		PSLen     int  `json:"ps_len"`
	}
	if err := json.Unmarshal(probeRaw, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.NeedleLen != 64 || probe.Leak || probe.PSLen == 0 {
		t.Fatalf("ready probe %+v", probe)
	}
	readyRaw, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readyKeyMaterial(readyRaw); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settle, []byte("go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyKeyReadIsNotAnEnvironmentLeak(t *testing.T) {
	ps := []byte("HOME=/tmp PATH=/usr/bin")
	if !bytes.Contains(ps, bytes.TrimSpace([]byte{})) {
		t.Fatal("baseline bytes.Contains(ps, empty) is no longer true; the empty-read fixture is stale")
	}
	err := keyAbsentFromProcessEnv(ps, nil)
	if !errors.Is(err, errKeyNotReady) {
		t.Fatalf("empty key read: %v", err)
	}
	if errors.Is(err, errKeyInEnv) {
		t.Fatal("empty key read was reported as a process-environment leak")
	}
	if err := keyAbsentFromProcessEnv(ps, []byte("\n")); !errors.Is(err, errKeyNotReady) {
		t.Fatalf("whitespace key read: %v", err)
	}

	sk := nostr.Generate()
	file := []byte(hex.EncodeToString(sk[:]) + "\n")
	if err := keyAbsentFromProcessEnv(ps, file); err != nil {
		t.Fatalf("absent key: %v", err)
	}
	leaked := append(append([]byte{}, ps...), hex.EncodeToString(sk[:])...)
	if err := keyAbsentFromProcessEnv(leaked, file); !errors.Is(err, errKeyInEnv) {
		t.Fatalf("real key in process environment: %v", err)
	}
}

func TestAtomicRoleKeyFinalPathIsNeverEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".hyphae"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".hyphae", "role.key")
	sk := nostr.Generate()
	stop := make(chan struct{})
	var incomplete atomic.Bool
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if _, err := readyKeyMaterial(data); err != nil {
				incomplete.Store(true)
			}
		}
	}()
	for i := 0; i < 40; i++ {
		if err := writeRoleKeyAtomic(path, sk); err != nil {
			close(stop)
			t.Fatal(err)
		}
	}
	close(stop)
	if incomplete.Load() {
		t.Fatal("role.key was readable before the full 32-byte key was published")
	}
	if err := signalKeyReady(dir, "bob", 4242); err != nil {
		t.Fatal(err)
	}
	var sig keyReadySignal
	if err := readJSON(filepath.Join(dir, ".hyphae", "key-ready.json"), &sig); err != nil {
		t.Fatal(err)
	}
	if sig.Role != "bob" || sig.PID != 4242 {
		t.Fatalf("ready signal %+v", sig)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".hyphae", "key-ready.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), hex.EncodeToString(sk[:])) {
		t.Fatal("key-ready signal contains the private key")
	}
}

func TestReorderSplitWindowStaysPublishOrderUntilBarrier(t *testing.T) {
	legacy := legacyTimerOrder([]string{"m1", "m2"}, []time.Duration{0, 200 * time.Millisecond}, 120*time.Millisecond)
	if len(legacy) != 2 || legacy[0] != "m1" || legacy[1] != "m2" {
		t.Fatalf("baseline 120ms split order %v", legacy)
	}
	got, err := barrierReverseIDs([]string{"m1", "m2"}, reorderNeed)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "m2" || got[1] != "m1" {
		t.Fatalf("barrier order %v", got)
	}
	if _, err := barrierReverseIDs([]string{"m1"}, reorderNeed); err == nil || !strings.Contains(err.Error(), "missing actual events") {
		t.Fatalf("missing event error: %v", err)
	}
}

func TestMisRelayReorderBarrierHoldsAcrossTimerWindow(t *testing.T) {
	prev := reorderBarrierTimeout
	reorderBarrierTimeout = 2 * time.Second
	t.Cleanup(func() { reorderBarrierTimeout = prev })

	relay := newMisRelay("", "")
	defer relay.close()
	subConn, frames, errs := dialRelay(t, relay.URL)
	defer subConn.Close()
	req, err := nostr.ReqEnvelope{SubscriptionID: "sub", Filters: []nostr.Filter{{}}}.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := subConn.WriteMessage(websocket.TextMessage, req); err != nil {
		t.Fatal(err)
	}
	if _, err := readFrame(frames, errs, func(data []byte) bool {
		return strings.Contains(string(data), "EOSE")
	}, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	sender, recipient := nostr.Generate(), nostr.Generate()
	recipientPub := recipient.Public()
	recipientHex := hex.EncodeToString(recipientPub[:])
	if err := armReorderBarrier(relay.URL, recipientHex, reorderNeed); err != nil {
		t.Fatal(err)
	}
	first := signedPEvent(t, sender, recipientPub, "logical-one")
	second := signedPEvent(t, sender, recipientPub, "logical-two")
	pubConn := dialRaw(t, relay.URL)
	defer pubConn.Close()
	publishAccepted(t, pubConn, first)

	select {
	case data := <-frames:
		t.Fatalf("relay flushed before both logical messages: %s", data)
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(250 * time.Millisecond):
	}

	publishAccepted(t, pubConn, second)
	var order []string
	for len(order) < 6 {
		data, err := readFrame(frames, errs, func([]byte) bool { return true }, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		env, err := nostr.ParseMessage(string(data))
		if err != nil {
			t.Fatal(err)
		}
		frame, ok := env.(*nostr.EventEnvelope)
		if !ok {
			continue
		}
		order = append(order, frame.Event.ID.Hex())
	}
	if order[0] != second.ID.Hex() {
		t.Fatalf("first delivery %s, want reversed logical %s in %v", order[0], second.ID.Hex(), order)
	}
	if countID(order, second.ID.Hex()) != 3 || countID(order, first.ID.Hex()) != 3 {
		t.Fatalf("duplicate deliveries %v", order)
	}
	if relay.deliveryCount(second.ID.Hex()) != 3 || relay.deliveryCount(first.ID.Hex()) != 3 {
		t.Fatalf("relay delivery counters first=%d second=%d", relay.deliveryCount(first.ID.Hex()), relay.deliveryCount(second.ID.Hex()))
	}
}

func TestMisRelayReorderBarrierWaitsForInsertedAck(t *testing.T) {
	prev := reorderBarrierTimeout
	reorderBarrierTimeout = 2 * time.Second
	t.Cleanup(func() { reorderBarrierTimeout = prev })

	pub := t.TempDir()
	relay := newMisRelay("", pub)
	defer relay.close()
	subConn, frames, errs := dialRelay(t, relay.URL)
	defer subConn.Close()
	req, err := nostr.ReqEnvelope{SubscriptionID: "sub", Filters: []nostr.Filter{{}}}.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := subConn.WriteMessage(websocket.TextMessage, req); err != nil {
		t.Fatal(err)
	}
	if _, err := readFrame(frames, errs, func(data []byte) bool {
		return strings.Contains(string(data), "EOSE")
	}, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	sender, recipient := nostr.Generate(), nostr.Generate()
	recipientPub := recipient.Public()
	recipientHex := hex.EncodeToString(recipientPub[:])
	if err := armReorderBarrier(relay.URL, recipientHex, reorderNeed); err != nil {
		t.Fatal(err)
	}
	first := signedPEvent(t, sender, recipientPub, "logical-one")
	second := signedPEvent(t, sender, recipientPub, "logical-two")
	pubConn := dialRaw(t, relay.URL)
	defer pubConn.Close()
	publishAccepted(t, pubConn, first)
	publishAccepted(t, pubConn, second)

	var firstBatch []string
	for len(firstBatch) < 3 {
		data, err := readFrame(frames, errs, func([]byte) bool { return true }, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		env, err := nostr.ParseMessage(string(data))
		if err != nil {
			t.Fatal(err)
		}
		frame, ok := env.(*nostr.EventEnvelope)
		if !ok {
			continue
		}
		firstBatch = append(firstBatch, frame.Event.ID.Hex())
	}
	if countID(firstBatch, second.ID.Hex()) != 3 {
		t.Fatalf("ack gate released the second logical message early: %v", firstBatch)
	}
	select {
	case data := <-frames:
		t.Fatalf("second logical message arrived before ack: %s", data)
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := os.WriteFile(filepath.Join(pub, "reorder-ack-"+recipientHex), []byte(second.ID.Hex()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var rest []string
	for len(rest) < 3 {
		data, err := readFrame(frames, errs, func([]byte) bool { return true }, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		env, err := nostr.ParseMessage(string(data))
		if err != nil {
			t.Fatal(err)
		}
		frame, ok := env.(*nostr.EventEnvelope)
		if !ok {
			continue
		}
		rest = append(rest, frame.Event.ID.Hex())
	}
	if countID(rest, first.ID.Hex()) != 3 {
		t.Fatalf("post-ack deliveries %v", rest)
	}
}

func TestMisRelayReorderBarrierTimesOutWhenEventMissing(t *testing.T) {
	prev := reorderBarrierTimeout
	reorderBarrierTimeout = 200 * time.Millisecond
	t.Cleanup(func() { reorderBarrierTimeout = prev })

	relay := newMisRelay("", "")
	defer relay.close()
	if err := armReorderBarrier(relay.URL, strings.Repeat("ab", 32), reorderNeed); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		err := relay.barrierError()
		if err != nil && strings.Contains(err.Error(), "missing actual events") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("reorder barrier did not report the missing event")
}

func TestCopiedPubkeyKeepsUniqueIDAndBadSignature(t *testing.T) {
	alice, bob, mallory := nostr.Generate(), nostr.Generate(), nostr.Generate()
	sample, err := messaging.BuildAgentMessageEvent(alice, bob.Public(), "sample-cipher", nostr.Now())
	if err != nil {
		t.Fatal(err)
	}
	claimed := *sample
	shell, err := messaging.BuildAgentMessageEvent(mallory, bob.Public(), "placeholder", nostr.Now())
	if err != nil {
		t.Fatal(err)
	}
	claimed.Content = sample.Content
	claimed.CreatedAt = shell.CreatedAt
	claimed.Kind = shell.Kind
	claimed.Tags = shell.Tags
	if err := claimed.Sign(mallory); err != nil {
		t.Fatal(err)
	}
	if !claimed.VerifySignature() {
		t.Fatal("copied claimed sender must keep a valid foreign signature")
	}
	copied, err := copiedPubkeyEvent(claimed, alice.Public())
	if err != nil {
		t.Fatal(err)
	}
	if copied.ID == claimed.ID {
		t.Fatal("copied pubkey reused the claimed sender event id")
	}
	if copied.Sig != claimed.Sig {
		t.Fatal("copied pubkey replaced the bad signature")
	}
	var zero [64]byte
	if copied.Sig == zero || !copied.CheckID() || copied.VerifySignature() {
		t.Fatal("copied pubkey is not a recomputed id with the retained bad signature")
	}
	raw, err := claimed.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	round, err := nostr.ParseMessage(`["EVENT",` + string(raw) + `]`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := round.(*nostr.EventEnvelope); !ok {
		t.Fatalf("claimed event did not round-trip: %T", round)
	}
	if _, err := groupchat.VerifyIncoming(copied, bob); err == nil {
		t.Fatal("recipient VerifyIncoming accepted the copied pubkey")
	}
	foreign, err := messaging.BuildAgentMessageEvent(mallory, bob.Public(), "foreign-not-member", nostr.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !foreign.VerifySignature() || foreign.Sig == zero {
		t.Fatal("foreign sender case lost its valid signature")
	}
}

func TestNegativeEventIDCollisionDoesNotCollapseCases(t *testing.T) {
	shared := strings.Repeat("ab", 32)
	cases := []negativeCase{
		{Name: "copied-claimed-sender", EventID: shared, TargetNpub: "bob", Expect: "verify-refuse"},
		{Name: "copied-pubkey", EventID: shared, TargetNpub: "bob", Expect: "verify-refuse"},
	}
	one := []negReceipt{{
		Name: "copied-claimed-sender", EventID: shared, Refused: true, DBUnchanged: true,
		RelayStored: true, ErrorClass: "verify", VerifyInvoked: true, SnapshotScope: snapshotScope,
	}}
	if !legacyNegativesCompleteByEventID(one, cases, "bob") {
		t.Fatal("baseline event-id index no longer collapses two cases; the fixture is stale")
	}
	if casesSatisfied(one, cases, "bob") {
		t.Fatal("one shared event id satisfied both copied-claimed-sender and copied-pubkey")
	}
	copiedID := strings.Repeat("cd", 32)
	cases[1].EventID = copiedID
	both := append(one, negReceipt{
		Name: "copied-pubkey", EventID: copiedID, Refused: true, DBUnchanged: true,
		RelayStored: false, ErrorClass: "verify", VerifyInvoked: true, SnapshotScope: snapshotScope,
	})
	if !casesSatisfied(both, cases, "bob") {
		t.Fatal("independent event ids should satisfy both negative cases")
	}
	if casesSatisfied(one, cases, "bob") {
		t.Fatal("claimed-sender receipt alone satisfied copied-pubkey")
	}
}

func TestScopedSnapshotHashesDMContentNotJustCount(t *testing.T) {
	left := snapshotDB(t, "alpha-body")
	right := snapshotDB(t, "beta-body")
	same := snapshotDB(t, "alpha-body")
	h1, err := hashDBFile(left)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := hashDBFile(right)
	if err != nil {
		t.Fatal(err)
	}
	h3, err := hashDBFile(same)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("distinct DM row content with the same row count produced one hash")
	}
	if h1 != h3 {
		t.Fatal("identical scoped rows produced different hashes")
	}
	if strings.Contains(h1, "alpha-body") || strings.Contains(h2, "beta-body") {
		t.Fatal("snapshot hash contains DM plaintext")
	}
}

func legacyTimerOrder(ids []string, at []time.Duration, window time.Duration) []string {
	var pending []string
	var out []string
	var last time.Duration
	flush := func() {
		for i := len(pending) - 1; i >= 0; i-- {
			out = append(out, pending[i])
		}
		pending = nil
	}
	for i, id := range ids {
		if i > 0 && at[i]-last > window && len(pending) > 0 {
			flush()
		}
		pending = append(pending, id)
		last = at[i]
	}
	flush()
	return out
}

func legacyNegativesCompleteByEventID(got []negReceipt, cases []negativeCase, self string) bool {
	byID := map[string]negReceipt{}
	for _, neg := range got {
		byID[neg.EventID] = neg
	}
	matched := 0
	for _, want := range cases {
		if !caseAppliesTo(want, self) {
			continue
		}
		item, ok := byID[want.EventID]
		if !ok || !item.Refused || !item.DBUnchanged {
			return false
		}
		matched++
	}
	return matched > 0
}

func snapshotDB(t *testing.T, dmPlain string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE messages (
		id TEXT PRIMARY KEY, event_id TEXT, sender_npub TEXT, recipient_npub TEXT,
		content TEXT, plaintext TEXT, created_at INTEGER, received_at INTEGER,
		is_encrypted BOOLEAN, is_incoming BOOLEAN, relay TEXT, kind INTEGER)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE groupchat_messages (
		local_npub TEXT, group_id TEXT, logical_id TEXT, sender_npub TEXT,
		body TEXT, created_at INTEGER, event_id TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO groupchat_messages VALUES ('local', 'group', 'logical', 'sender', 'body', 1, 'event')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO messages VALUES ('dm-1', 'ev', 'sender', 'recipient', ?, ?, 1, 1, 1, 1, 'relay', 30078)`, dmPlain, dmPlain)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func signedPEvent(t *testing.T, sender nostr.SecretKey, recipient nostr.PubKey, content string) nostr.Event {
	t.Helper()
	event := nostr.Event{
		CreatedAt: nostr.Now(),
		Kind:      1,
		Tags:      nostr.Tags{{"p", hex.EncodeToString(recipient[:])}},
		Content:   content,
	}
	if err := event.Sign(sender); err != nil {
		t.Fatal(err)
	}
	return event
}

func dialRaw(t *testing.T, relayURL string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func dialRelay(t *testing.T, relayURL string) (*websocket.Conn, <-chan []byte, <-chan error) {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	frames := make(chan []byte, 16)
	errs := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				errs <- err
				return
			}
			frames <- data
		}
	}()
	return conn, frames, errs
}

func publishAccepted(t *testing.T, conn *websocket.Conn, event nostr.Event) {
	t.Helper()
	raw, err := nostr.EventEnvelope{Event: event}.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	env, err := nostr.ParseMessage(string(data))
	if err != nil {
		t.Fatalf("ok frame: %v (%s)", err, data)
	}
	ok, isOK := env.(*nostr.OKEnvelope)
	if !isOK || !ok.OK {
		t.Fatalf("relay rejected event: %s", data)
	}
}

func readFrame(frames <-chan []byte, errs <-chan error, match func([]byte) bool, timeout time.Duration) ([]byte, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case data := <-frames:
			if match(data) {
				return data, nil
			}
		case err := <-errs:
			return nil, err
		case <-timer.C:
			return nil, errors.New("timed out waiting for a relay frame")
		}
	}
}

func countID(ids []string, want string) int {
	n := 0
	for _, id := range ids {
		if id == want {
			n++
		}
	}
	return n
}
