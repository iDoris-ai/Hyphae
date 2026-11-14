package social_test

// Conformance fixtures: runs parse-common*.json (and every parse file through one
// dispatcher) against the social packages, and checks the fixture directory.
// Format: testdata/fixtures/README.md.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/social"
	"github.com/iDoris-ai/hyphae/internal/social/intent"
	"github.com/iDoris-ai/hyphae/internal/social/referral"
	"github.com/iDoris-ai/hyphae/internal/social/trust"
)

const fxDir = "testdata/fixtures"

// --- shared fixture loader (the same in all four fixtures_test.go files; a copy may add helpers) ---

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

// fxParseAny is the full-message parser a second implementation would expose:
// route by the header's type to the owning package.
func fxParseAny(e social.Envelope) (string, error) {
	h, err := social.ParseHeader(e)
	if err != nil {
		return "", err
	}
	switch h.Type {
	case "intent", "respond", "disclose", "accept", "decline", "withdraw":
		m, err := intent.Parse(e)
		return m.Type, err
	case "referral_request":
		_, err = referral.ParseRequest(e)
	case "referral_forward":
		_, err = referral.ParseForward(e)
	case "referral_accept", "referral_decline":
		_, err = referral.ParseReply(e, h.Type)
	case "receipt":
		_, err = referral.ParseReceipt(e)
	case "invite":
		_, err = trust.ParseInvite(e)
	case "invite_accept":
		_, err = trust.ParseAccept(e)
	case "rating":
		_, err = trust.ParseRating(e)
	case "attest":
		_, err = trust.ParseAttest(e)
	default:
		err = fmt.Errorf("unknown type %q", h.Type)
	}
	return h.Type, err
}

func TestFixturesParseCommon(t *testing.T) {
	fxRunParse(t, fxLoad[fxParseCase](t, "parse-common*.json", "parse"), fxParseAny, func(e social.Envelope) (string, error) {
		h, err := social.ParseHeader(e)
		return h.Type, err
	})
}

// TestFixturesAllParseCasesThroughOneDispatcher runs every parse file through
// the single dispatcher too, so a case filed under the wrong package still
// has to agree with whichever package owns its type.
func TestFixturesAllParseCasesThroughOneDispatcher(t *testing.T) {
	var cases []fxParseCase
	for _, c := range fxLoad[fxParseCase](t, "parse-*.json", "parse") {
		if c.Scope != "shape" {
			cases = append(cases, c)
		}
	}
	fxRunParse(t, cases, fxParseAny, nil)
}

// TestFixtureFiles checks the directory: every file is a fixture of the kind
// its name says, and case names are unique per kind.
func TestFixtureFiles(t *testing.T) {
	kinds := map[string]string{"parse-": "parse", "seq-intent": "intent_session", "seq-referral": "referral_flow", "resolve-": "invite_chain"}
	files, err := filepath.Glob(filepath.Join(fxDir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, f := range files {
		base := filepath.Base(f)
		if base == "README.md" {
			continue
		}
		want := ""
		for prefix, k := range kinds {
			if strings.HasPrefix(base, prefix) && strings.HasSuffix(base, ".json") {
				want = k
			}
		}
		if want == "" {
			t.Errorf("%s: unexpected file in the fixture directory", base)
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 60<<10 {
			t.Errorf("%s: %d bytes, keep fixture files under 60 KiB", base, len(data))
		}
		var file struct {
			Kind        string `json:"kind"`
			Description string `json:"description"`
			Cases       []struct {
				Name string `json:"name"`
			} `json:"cases"`
		}
		if err := json.Unmarshal(data, &file); err != nil || file.Kind != want {
			t.Errorf("%s: kind %q (want %q), err %v", base, file.Kind, want, err)
		}
		for _, c := range file.Cases {
			key := file.Kind + "/" + c.Name
			if c.Name == "" {
				t.Errorf("%s: case without a name", base)
			} else if prev, dup := names[key]; dup {
				t.Errorf("%s: case %q also in %s", base, c.Name, prev)
			}
			names[key] = base
		}
	}
}
