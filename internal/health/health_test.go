package health

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/urfave/cli/v3"
)

const refused = "ws://127.0.0.1:1"

func okRelay(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		_, _, _ = c.Read(context.Background())
	}))
	t.Cleanup(s.Close)
	return "ws" + strings.TrimPrefix(s.URL, "http")
}

// hangRelay accepts TCP and never answers the WebSocket handshake.
func hangRelay(t *testing.T) string {
	t.Helper()
	done := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-done }))
	t.Cleanup(func() { close(done); s.Close() })
	return "ws" + strings.TrimPrefix(s.URL, "http")
}

func home(t *testing.T) string {
	h := t.TempDir()
	t.Setenv("HOME", h)
	return h
}

func writeFile(t *testing.T, h, name, body string) {
	t.Helper()
	dir := filepath.Join(h, ".hyphae")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func js(t *testing.T, d healthData) string {
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const plainKS = `{"default_identity":"alice","identities":{"alice":{"nickname":"alice","npub":"npub1secretnpubvalue","nsec":"nsec1xxxxxxxxxxxxxxxxxxxxxxxxxxxx"}},"contacts":{},"encrypted":false}`
const encKS = `{"default_identity":"bob","identities":{"bob":{"nickname":"bob","npub":"npub1bobnpub","nsec":"ENC"},"carol":{"nickname":"carol","npub":"npub1carol","nsec":"ENC"}},"contacts":{},"encrypted":true,"salt":"c2FsdA==","verification":"v"}`

func TestHealthOK(t *testing.T) {
	h := home(t)
	writeFile(t, h, "keystore.json", plainKS)
	d := buildHealth(context.Background(), []string{okRelay(t)})
	if d.Status != "ok" || !d.Checks.Relays.OK || !d.Checks.Keystore.OK {
		t.Fatalf("%s", js(t, d))
	}
	k := d.Checks.Keystore
	if !k.Exists || *k.Encrypted || *k.Identities != 1 {
		t.Fatalf("keystore: %s", js(t, d))
	}
	if it := d.Checks.Relays.Items[0]; !it.Reachable || it.LatencyMS == nil || it.Error != "" {
		t.Fatalf("relay: %+v", it)
	}
}

func TestHealthDegradedAndFailed(t *testing.T) {
	home(t)
	up := okRelay(t)
	d := buildHealth(context.Background(), []string{refused, up})
	if d.Status != "degraded" || d.Checks.Relays.OK {
		t.Fatalf("%s", js(t, d))
	}
	for _, it := range d.Checks.Relays.Items {
		if it.URL == refused && (it.Reachable || it.Error != "refused" || it.LatencyMS != nil) {
			t.Fatalf("refused item: %+v", it)
		}
	}
	d = buildHealth(context.Background(), []string{refused})
	if d.Status != "failed" {
		t.Fatalf("%s", js(t, d))
	}
	if d = buildHealth(context.Background(), nil); d.Status != "failed" || d.Checks.Relays.OK {
		t.Fatalf("no relays: %s", js(t, d))
	}
}

func TestHealthCorruptKeystoreFails(t *testing.T) {
	h := home(t)
	writeFile(t, h, "keystore.json", "{not json")
	d := buildHealth(context.Background(), []string{okRelay(t)})
	if d.Status != "failed" {
		t.Fatalf("%s", js(t, d))
	}
	b, _ := json.Marshal(d.Checks.Keystore)
	if string(b) != `{"ok":false,"exists":true}` {
		t.Fatalf("keystore = %s", b)
	}
}

func TestHealthFreshHomeCreatesNothing(t *testing.T) {
	h := home(t)
	d := buildHealth(context.Background(), []string{okRelay(t)})
	b, _ := json.Marshal(d.Checks.Keystore)
	if d.Status != "ok" || string(b) != `{"ok":true,"exists":false,"encrypted":false,"identities":0}` {
		t.Fatalf("%s %s", d.Status, b)
	}
	if _, err := os.Stat(filepath.Join(h, ".hyphae")); !os.IsNotExist(err) {
		t.Fatalf(".hyphae must not exist: %v", err)
	}
}

func TestHealthEncryptedWithoutPasswordNoLeak(t *testing.T) {
	h := home(t)
	writeFile(t, h, "keystore.json", encKS)
	before, _ := os.ReadFile(filepath.Join(h, ".hyphae", "keystore.json"))
	d := buildHealth(context.Background(), []string{okRelay(t)})
	k := d.Checks.Keystore
	if !k.OK || !*k.Encrypted || *k.Identities != 2 {
		t.Fatalf("%s", js(t, d))
	}
	out := js(t, d)
	for _, bad := range []string{"bob", "carol", "npub", "nsec", "ENC", "salt"} {
		if strings.Contains(out, bad) {
			t.Fatalf("output leaks %q: %s", bad, out)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(h, ".hyphae"))
	after, _ := os.ReadFile(filepath.Join(h, ".hyphae", "keystore.json"))
	if len(entries) != 1 || !bytes.Equal(before, after) {
		t.Fatalf("health modified ~/.hyphae: %v", entries)
	}
}

func TestHealthTimeoutBounds(t *testing.T) {
	home(t)
	oldR, oldT := relayTimeout, totalTimeout
	t.Cleanup(func() { relayTimeout, totalTimeout = oldR, oldT })
	hang := hangRelay(t)

	relayTimeout, totalTimeout = 300*time.Millisecond, 3*time.Second
	start := time.Now()
	d := buildHealth(context.Background(), []string{hang, hang + "/a", hang + "/b"})
	if el := time.Since(start); el > 900*time.Millisecond {
		t.Fatalf("per-relay timeout not parallel/bounded: %v", el)
	}
	if d.Checks.Relays.Items[0].Error != "timeout" {
		t.Fatalf("%s", js(t, d))
	}

	relayTimeout, totalTimeout = 5*time.Second, 300*time.Millisecond
	start = time.Now()
	buildHealth(context.Background(), []string{hang})
	if el := time.Since(start); el > 900*time.Millisecond {
		t.Fatalf("total cap not enforced: %v", el)
	}
}

func TestHealthSortedAndTruncated(t *testing.T) {
	home(t)
	var urls []string
	for i := 20; i > 0; i-- {
		urls = append(urls, "ws://127.0.0.1:1/"+string(rune('a'+i)))
	}
	d := buildHealth(context.Background(), urls)
	items := d.Checks.Relays.Items
	if len(items) != 16 || !d.Checks.Relays.Truncated {
		t.Fatalf("len=%d truncated=%v", len(items), d.Checks.Relays.Truncated)
	}
	for i := 1; i < len(items); i++ {
		if items[i-1].URL >= items[i].URL {
			t.Fatal("not sorted")
		}
	}
	d = buildHealth(context.Background(), urls[:16])
	if b, _ := json.Marshal(d.Checks.Relays); strings.Contains(string(b), "truncated") {
		t.Fatalf("truncated must be absent at 16: %s", b)
	}
}

func TestClassifyVocabulary(t *testing.T) {
	home(t)
	d := buildHealth(context.Background(), []string{"ws://nonexistent.invalid:80"})
	if e := d.Checks.Relays.Items[0].Error; e != "dns" && e != "other" {
		t.Fatalf("error = %q", e)
	}
	if e := d.Checks.Relays.Items[0].Error; strings.ContainsAny(e, ".: ") {
		t.Fatalf("raw text leaked: %q", e)
	}
}

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	root := &cli.Command{Name: "hyphae", Flags: []cli.Flag{&cli.BoolFlag{Name: "json"}}, Commands: []*cli.Command{Cmd}}
	err := root.Run(context.Background(), append([]string{"hyphae"}, args...))
	w.Close()
	os.Stdout = old
	var b bytes.Buffer
	_, _ = b.ReadFrom(r)
	return b.String(), err
}

func TestHealthCmdUsesConfiguredRelaysAndEnvJSON(t *testing.T) {
	h := home(t)
	up := okRelay(t)
	writeFile(t, h, "relays.json", `{"version":1,"relays":["`+up+`","`+refused+`"]}`)
	t.Setenv("HYPHAE_OUTPUT", "json")
	out, err := runCmd(t, "health")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK   bool       `json:"ok"`
		Data healthData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || !env.OK {
		t.Fatalf("%v %s", err, out)
	}
	if env.Data.Status != "degraded" || len(env.Data.Checks.Relays.Items) != 2 {
		t.Fatalf("%s", out)
	}
	t.Setenv("HYPHAE_OUTPUT", "")
	if out, err = runCmd(t, "health"); err != nil || !strings.HasPrefix(out, "status: degraded") {
		t.Fatalf("human: %v %q", err, out)
	}
}

func TestHealthCmdBadRelayConfigIsUserError(t *testing.T) {
	h := home(t)
	writeFile(t, h, "relays.json", "garbage")
	_, err := runCmd(t, "health")
	var ee *common.ExitError
	if !errors.As(err, &ee) || ee.Code != common.ErrCodeUser || !strings.Contains(err.Error(), "relay configuration") {
		t.Fatalf("err = %v", err)
	}
}

func TestHealthBinaryContract(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "hyphae")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/hyphae").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	h := home(t)
	writeFile(t, h, "relays.json", `{"version":1,"relays":["`+refused+`"]}`)
	cmd := exec.Command(bin, "health", "--json")
	cmd.Env = append(os.Environ(), "HOME="+h)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("exit must be 0 for failed status: %v", err)
	}
	var env struct {
		OK   bool       `json:"ok"`
		Data healthData `json:"data"`
	}
	dec := json.NewDecoder(&stdout)
	if err := dec.Decode(&env); err != nil || dec.More() || !env.OK || env.Data.Status != "failed" {
		t.Fatalf("stdout: %v %+v", err, env)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}
}
