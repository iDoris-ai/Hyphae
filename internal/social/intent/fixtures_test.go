package intent_test

// Conformance fixtures: runs ../testdata/fixtures/parse-intent*.json and
// seq-intent*.json (format: ../testdata/fixtures/README.md) against this package.

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/social"
	"github.com/iDoris-ai/hyphae/internal/social/intent"
)

const fxDir = "../testdata/fixtures"

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

// fxOutcome maps an error to the fixture vocabulary: ok, defer or reject.
func fxOutcome(err, tooEarly error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, tooEarly):
		return "defer"
	}
	return "reject"
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

func TestFixturesParseIntent(t *testing.T) {
	fxRunParse(t, fxLoad[fxParseCase](t, "parse-intent*.json", "parse"), func(e social.Envelope) (string, error) {
		m, err := intent.Parse(e)
		return m.Type, err
	}, nil)
}

var fxIntentStates = map[intent.State]string{
	intent.Open: "Open", intent.Responded: "Responded", intent.Disclosing: "Disclosing", intent.Matched: "Matched",
	intent.Declined: "Declined", intent.Withdrawn: "Withdrawn", intent.Expired: "Expired",
}

type fxIntentCase struct {
	Name     string `json:"name"`
	Note     string `json:"note"`
	KnownGap string `json:"known_gap"`
	Setup    struct {
		Intent    fxEnvelope `json:"intent"`
		Responder string     `json:"responder"`
		Now       int64      `json:"now"` // trusted clock for setup; defaults to the event's created_at
		Expect    string     `json:"expect"`
	} `json:"setup"`
	Steps []struct {
		Envelope   fxEnvelope `json:"envelope"`
		Now        int64      `json:"now"`
		Expect     string     `json:"expect"`
		StateAfter string     `json:"state_after"`
		Note       string     `json:"note"`
	} `json:"steps"`
}

func TestFixturesIntentSession(t *testing.T) {
	known := map[string]bool{}
	for _, n := range fxIntentStates {
		known[n] = true
	}
	for _, c := range fxLoad[fxIntentCase](t, "seq-intent*.json", "intent_session") {
		t.Run(c.Name, func(t *testing.T) {
			got, mismatch := fxRunIntent(t, c, known)
			if c.KnownGap != "" {
				fxGap(t, c.KnownGap, mismatch, got)
			}
			if mismatch {
				t.Error(got)
			}
		})
	}
}

// fxRunIntent returns a description of the first disagreement, if any.
func fxRunIntent(t *testing.T, c fxIntentCase, known map[string]bool) (string, bool) {
	m, err := intent.Parse(c.Setup.Intent.build(t))
	var s *intent.Session
	if err == nil {
		s, err = intent.NewSession(m, c.Setup.Responder, cmp.Or(c.Setup.Now, m.Envelope.CreatedAt))
	}
	switch want := cmp.Or(c.Setup.Expect, "ok"); {
	case want == "reject" || want == "defer":
		if got := fxOutcome(err, intent.ErrTooEarly); got != want {
			return fmt.Sprintf("setup: got %s (err %v), want %s", got, err, want), true
		}
		return "", false
	case want != "ok":
		t.Fatalf("bad setup expect %q", want)
	case err != nil:
		return "setup: " + err.Error(), true
	}
	for i, st := range c.Steps {
		if !known[st.StateAfter] || st.Expect != "ok" && st.Expect != "reject" && st.Expect != "defer" {
			t.Fatalf("step %d: bad expect %q or state %q", i, st.Expect, st.StateAfter)
		}
		m, err := intent.Parse(st.Envelope.build(t))
		if err == nil {
			err = s.Apply(m, st.Now)
		}
		got, state := fxOutcome(err, intent.ErrTooEarly), fxIntentStates[s.StateAt(st.Now)]
		if got != st.Expect || state != st.StateAfter {
			return fmt.Sprintf("step %d: got %s/%s (err %v), want %s/%s; note: %s",
				i, got, state, err, st.Expect, st.StateAfter, st.Note), true
		}
	}
	return "", false
}
