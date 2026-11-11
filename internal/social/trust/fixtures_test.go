package trust_test

// Conformance fixtures: runs parse-trust*.json and resolve-*.json against this package.
// Format: ../testdata/fixtures/README.md.

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/social"
	"github.com/iDoris-ai/hyphae/internal/social/trust"
)

const fxDir = "../testdata/fixtures"

// --- shared fixture loader (kept identical across the four fixtures_test.go files) ---

type fxEnvelope struct {
	ID         string            `json:"id"`
	Author     string            `json:"author"`
	CreatedAt  int64             `json:"created_at"`
	Content    *string           `json:"content"`
	ContentGen []json.RawMessage `json:"content_gen"`
}

func (e fxEnvelope) build(t *testing.T) social.Envelope {
	t.Helper()
	var content string
	switch {
	case e.Content != nil && e.ContentGen == nil:
		content = *e.Content
	case e.Content == nil && e.ContentGen != nil:
		var b strings.Builder
		for _, p := range e.ContentGen {
			var lit string
			if json.Unmarshal(p, &lit) == nil {
				b.WriteString(lit)
				continue
			}
			var r struct {
				Repeat string `json:"repeat"`
				Count  int    `json:"count"`
			}
			if err := fxStrict(p, &r); err != nil || r.Repeat == "" || r.Count < 0 {
				t.Fatalf("bad content_gen part %s: %v", p, err)
			}
			b.WriteString(strings.Repeat(r.Repeat, r.Count))
		}
		content = b.String()
	default:
		t.Fatal("envelope needs exactly one of content and content_gen")
	}
	return social.Envelope{ID: e.ID, Author: e.Author, CreatedAt: e.CreatedAt, Content: json.RawMessage(content)}
}

func fxStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// fxLoad decodes every file matching pattern (strictly: unknown keys fail) and
// returns their cases, checking the file kind.
func fxLoad[T any](t *testing.T, pattern, kind string) []T {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(fxDir, pattern))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixture files match %s: %v", pattern, err)
	}
	var out []T
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			Kind        string `json:"kind"`
			Description string `json:"description"`
			Cases       []T    `json:"cases"`
		}
		if err := fxStrict(data, &file); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if file.Kind != kind || len(file.Cases) == 0 {
			t.Fatalf("%s: kind %q with %d cases, want kind %q", f, file.Kind, len(file.Cases), kind)
		}
		out = append(out, file.Cases...)
	}
	return out
}

// fxGap handles a case marked known_gap: it is skipped and logged while the
// Go result still disagrees, and fails once the gap is fixed so the marker is
// removed.
func fxGap(t *testing.T, gap string, mismatch bool, got string) {
	t.Helper()
	if !mismatch {
		t.Fatalf("known_gap case now matches the fixture; remove the marker (%s)", gap)
	}
	t.Skipf("KNOWN GAP (Go gives %s): %s", got, gap)
}

type fxParseCase struct {
	Name     string     `json:"name"`
	Envelope fxEnvelope `json:"envelope"`
	Expect   string     `json:"expect"`
	Type     string     `json:"type"`
	Scope    string     `json:"scope"`
	Note     string     `json:"note"`
	KnownGap string     `json:"known_gap"`
}

// fxRunParse runs parse cases. message parses a full message; shape applies
// only the shared body rules (nil: the file must have no shape-scope cases).
func fxRunParse(t *testing.T, cases []fxParseCase, message, shape func(social.Envelope) (string, error)) {
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			parse := message
			if c.Scope == "shape" {
				parse = shape
			} else if c.Scope != "" && c.Scope != "message" {
				parse = nil
			}
			if parse == nil || c.Expect != "accept" && c.Expect != "reject" || c.Expect == "accept" && c.Type == "" {
				t.Fatalf("bad case: expect %q type %q scope %q", c.Expect, c.Type, c.Scope)
			}
			typ, err := parse(c.Envelope.build(t))
			got := "reject"
			if err == nil {
				got = "accept"
			}
			mismatch := got != c.Expect || got == "accept" && typ != c.Type
			if c.KnownGap != "" {
				fxGap(t, c.KnownGap, mismatch, got)
			}
			if mismatch {
				t.Errorf("got %s (type %q, err %v), want %s %s; note: %s", got, typ, err, c.Expect, c.Type, c.Note)
			}
		})
	}
}

// --- end of shared loader ---

func TestFixturesParseTrust(t *testing.T) {
	fxRunParse(t, fxLoad[fxParseCase](t, "parse-trust*.json", "parse"), func(e social.Envelope) (string, error) {
		h, err := social.ParseHeader(e)
		if err != nil {
			return "", err
		}
		switch h.Type {
		case "invite":
			_, err = trust.ParseInvite(e)
		case "invite_accept":
			_, err = trust.ParseAccept(e)
		case "rating":
			_, err = trust.ParseRating(e)
		default:
			err = fmt.Errorf("not a trust type: %q", h.Type)
		}
		return h.Type, err
	}, nil)
}

type fxMemberExpect struct {
	Valid   bool    `json:"valid"`
	Inviter *string `json:"inviter"`
	Depth   *int    `json:"depth"`
	Invite  *string `json:"invite"`
}

type fxResolveCase struct {
	Name        string                    `json:"name"`
	Note        string                    `json:"note"`
	KnownGap    string                    `json:"known_gap"`
	Roots       []string                  `json:"roots"`
	Quota       int                       `json:"quota"`
	MaxDepth    int                       `json:"max_depth"`
	Events      []fxEnvelope              `json:"events"`
	Exhaustive  bool                      `json:"exhaustive"`
	Now         int64                     `json:"now"` // trusted clock; defaults to far later than every event
	ExpectError bool                      `json:"expect_error"`
	Expect      map[string]fxMemberExpect `json:"expect"`
}

func TestFixturesResolveInvites(t *testing.T) {
	for _, c := range fxLoad[fxResolveCase](t, "resolve-*.json", "invite_chain") {
		t.Run(c.Name, func(t *testing.T) {
			got, mismatch := fxRunResolve(t, c)
			if c.KnownGap != "" {
				fxGap(t, c.KnownGap, mismatch, got)
			}
			if mismatch {
				t.Error(got)
			}
		})
	}
}

// fxRunResolve returns a description of the first disagreement, if any.
func fxRunResolve(t *testing.T, c fxResolveCase) (string, bool) {
	var envs []social.Envelope
	keys := map[string]bool{} // every pubkey named anywhere: the only candidates for membership
	for _, r := range c.Roots {
		keys[r] = true
	}
	for _, e := range c.Events {
		env := e.build(t)
		envs = append(envs, env)
		keys[env.Author] = true
	}
	for k := range c.Expect {
		keys[k] = true
	}
	cfg := trust.Config{Roots: c.Roots, Quota: c.Quota, MaxDepth: c.MaxDepth}
	snapshot := func(envs []social.Envelope) (map[string]trust.Member, error) {
		ch, err := trust.ResolveInvites(cfg, envs, cmp.Or(c.Now, int64(1)<<40))
		if err != nil {
			return nil, err
		}
		m := map[string]trust.Member{}
		for k := range keys {
			if mem, ok := ch.Member(k); ok {
				m[k] = mem
			}
		}
		return m, nil
	}
	got, err := snapshot(envs)
	if c.ExpectError || err != nil {
		if c.ExpectError != (err != nil) {
			return fmt.Sprintf("error %v, want error: %v", err, c.ExpectError), true
		}
		return "", false
	}
	// The result depends only on the set of events: not their order, not repeats.
	rev := slices.Clone(envs)
	slices.Reverse(rev)
	for name, alt := range map[string][]social.Envelope{"reversed": rev, "doubled": slices.Concat(envs, envs)} {
		if m, _ := snapshot(alt); !maps.Equal(m, got) {
			return fmt.Sprintf("%s order gives %v, given order %v", name, m, got), true
		}
	}
	for k, want := range c.Expect {
		mem, ok := got[k]
		if !want.Valid {
			if ok {
				return fmt.Sprintf("%s: valid %+v, want invalid", k, mem), true
			}
			continue
		}
		if want.Inviter == nil || want.Depth == nil {
			t.Fatalf("%s: a valid expectation needs inviter and depth", k)
		}
		if !ok || mem.Inviter != *want.Inviter || mem.Depth != *want.Depth || want.Invite != nil && mem.Invite != *want.Invite {
			invite := "(any)"
			if want.Invite != nil {
				invite = *want.Invite
			}
			return fmt.Sprintf("%s: got %+v (valid %v), want inviter %q depth %d invite %s",
				k, mem, ok, *want.Inviter, *want.Depth, invite), true
		}
	}
	if c.Exhaustive {
		for k, mem := range got {
			if _, listed := c.Expect[k]; !listed {
				return fmt.Sprintf("exhaustive: %s is valid (%+v) but not expected", k, mem), true
			}
		}
	}
	return "", false
}
