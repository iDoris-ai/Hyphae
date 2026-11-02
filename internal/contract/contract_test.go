package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/urfave/cli/v3"
)

func testRoot(extra ...*cli.Command) *cli.Command {
	return &cli.Command{
		Name: "hyphae", Version: "test-1",
		Flags: []cli.Flag{&cli.BoolFlag{Name: "json"}},
		Commands: append([]*cli.Command{
			{Name: "identity", Commands: []*cli.Command{
				{Name: "export", Flags: []cli.Flag{
					&cli.StringFlag{Name: "out", Aliases: []string{"o"}},
					&cli.BoolFlag{Name: "password-stdin"},
					&cli.StringFlag{Name: "format"},
					&cli.StringFlag{Name: "secret-hidden", Hidden: true},
				}},
			}},
			{Name: "secret", Hidden: true},
			Cmd,
		}, extra...),
	}
}

func run(t *testing.T, root *cli.Command, args ...string) (out []byte) {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	err := root.Run(context.Background(), append([]string{"hyphae"}, args...))
	w.Close()
	os.Stdout = old
	var b bytes.Buffer
	_, _ = b.ReadFrom(r)
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type envelope struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
}

func decode(t *testing.T, out []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(out))
	var env envelope
	if err := dec.Decode(&env); err != nil || !env.OK {
		t.Fatalf("bad envelope: %v %s", err, out)
	}
	if dec.More() {
		t.Fatalf("stdout holds more than one JSON document: %s", out)
	}
	var d map[string]any
	if err := json.Unmarshal(env.Data, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestContractShape(t *testing.T) {
	out := run(t, testRoot(), "contract", "--json")
	d := decode(t, out)
	keys := []string{"binary_version", "commands", "contract_version", "error_codes", "name", "provides"}
	var got []string
	for k := range d {
		got = append(got, k)
	}
	if len(got) != len(keys) {
		t.Fatalf("keys = %v", got)
	}
	if d["name"] != "hyphae-cli" || d["contract_version"] != "1.0" || d["binary_version"] != "test-1" {
		t.Fatalf("scalars: %v", d)
	}
	if !reflect.DeepEqual(d["provides"], []any{"nostr-dm", "nostr-group", "nostr-identity", "nostr-relay"}) {
		t.Fatalf("provides = %v", d["provides"])
	}
}

func TestContractCommandsWalkRealTree(t *testing.T) {
	var d contractData
	var env struct{ Data contractData }
	if err := json.Unmarshal(run(t, testRoot(), "contract", "--json"), &env); err != nil {
		t.Fatal(err)
	}
	d = env.Data
	paths := map[string][]string{}
	var order []string
	for _, c := range d.Commands {
		paths[c.Path] = c.Flags
		order = append(order, c.Path)
	}
	if !reflect.DeepEqual(paths["identity export"], []string{"--format", "--out", "--password-stdin"}) {
		t.Fatalf("identity export flags = %v", paths["identity export"])
	}
	if _, ok := paths["contract"]; !ok {
		t.Fatal("contract missing")
	}
	for _, bad := range []string{"", "secret", "help", "identity help"} {
		if _, ok := paths[bad]; ok {
			t.Fatalf("%q must not be listed", bad)
		}
	}
	if !sortedStrings(order) {
		t.Fatalf("not sorted: %v", order)
	}
	for _, c := range d.Commands {
		for _, f := range c.Flags {
			if f == "--json" || f == "--help" || f == "--version" {
				t.Fatalf("%s lists root/auto flag %s", c.Path, f)
			}
		}
	}
}

func sortedStrings(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}

func TestContractPicksUpNewSubcommand(t *testing.T) {
	extra := &cli.Command{Name: "brand-new", Flags: []cli.Flag{&cli.IntFlag{Name: "zzz"}}}
	var env struct{ Data contractData }
	if err := json.Unmarshal(run(t, testRoot(extra), "contract", "--json"), &env); err != nil {
		t.Fatal(err)
	}
	for _, c := range env.Data.Commands {
		if c.Path == "brand-new" && reflect.DeepEqual(c.Flags, []string{"--zzz"}) {
			return
		}
	}
	t.Fatal("new subcommand not reported")
}

func TestContractErrorCodesMatchCommon(t *testing.T) {
	var env struct{ Data contractData }
	if err := json.Unmarshal(run(t, testRoot(), "contract", "--json"), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.ErrorCodes) != len(common.StableErrorCodes) {
		t.Fatalf("len = %d", len(env.Data.ErrorCodes))
	}
	want := map[string]int{"user_error": 1, "network_error": 2, "auth_error": 3, "other_error": 4, "write_conflict": 5, "invalid_key": 1, "keystore_locked": 3}
	prev := ""
	for _, e := range env.Data.ErrorCodes {
		if e.Code <= prev {
			t.Fatalf("not sorted at %s", e.Code)
		}
		prev = e.Code
		if e.Exit != common.ExitCodeFor(e.Code) {
			t.Fatalf("%s exit %d", e.Code, e.Exit)
		}
		if w, ok := want[e.Code]; ok && w != e.Exit {
			t.Fatalf("%s exit %d, want %d", e.Code, e.Exit, w)
		}
	}
}

func TestContractDeterministicAndEnvJSON(t *testing.T) {
	a := run(t, testRoot(), "contract", "--json")
	b := run(t, testRoot(), "contract", "--json")
	if !bytes.Equal(a, b) {
		t.Fatal("two runs differ")
	}
	t.Setenv("HYPHAE_OUTPUT", "json")
	if c := run(t, testRoot(), "contract"); !bytes.Equal(a, c) {
		t.Fatalf("env JSON mode differs: %s", c)
	}
	t.Setenv("HYPHAE_OUTPUT", "")
	if h := run(t, testRoot(), "contract"); strings.HasPrefix(string(h), "{") || !strings.Contains(string(h), "hyphae-cli contract 1.0") {
		t.Fatalf("human mode: %s", h)
	}
}

func TestContractBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "hyphae")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/hyphae").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "contract", "--json")
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}
	var env struct{ Data contractData }
	dec := json.NewDecoder(&stdout)
	if err := dec.Decode(&env); err != nil || dec.More() {
		t.Fatalf("not exactly one document: %v", err)
	}
	if env.Data.BinaryVersion != "dev" {
		t.Fatalf("binary_version = %q", env.Data.BinaryVersion)
	}
	seen := map[string]bool{}
	for _, c := range env.Data.Commands {
		seen[c.Path] = true
	}
	for _, p := range []string{"identity export", "identity import", "contract", "agent msg", "daemon"} {
		if !seen[p] {
			t.Errorf("real tree lacks %q", p)
		}
	}
}
