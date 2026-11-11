package referral_test

// Conformance fixtures: runs parse-referral*.json and seq-referral*.json against this package.
// Format: ../testdata/fixtures/README.md.

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
	"github.com/iDoris-ai/hyphae/internal/social/referral"
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

func TestFixturesParseReferral(t *testing.T) {
	fxRunParse(t, fxLoad[fxParseCase](t, "parse-referral*.json", "parse"), func(e social.Envelope) (string, error) {
		h, err := social.ParseHeader(e)
		if err != nil {
			return "", err
		}
		switch h.Type {
		case "referral_request":
			_, err = referral.ParseRequest(e)
		case "referral_forward":
			_, err = referral.ParseForward(e)
		case "referral_accept", "referral_decline":
			_, err = referral.ParseReply(e, h.Type)
		case "receipt":
			_, err = referral.ParseReceipt(e)
		default:
			err = fmt.Errorf("not a referral type: %q", h.Type)
		}
		return h.Type, err
	}, nil)
}

var fxFlowStates = map[referral.State]string{
	referral.Requested: "Requested", referral.Forwarded: "Forwarded", referral.Accepted: "Accepted",
	referral.Settled: "Settled", referral.Declined: "Declined", referral.Expired: "Expired",
}

type fxFlowCase struct {
	Name     string `json:"name"`
	Note     string `json:"note"`
	KnownGap string `json:"known_gap"`
	Setup    struct {
		Request     fxEnvelope `json:"request"`
		FirstHolder string     `json:"first_holder"`
		Now         int64      `json:"now"` // trusted clock for setup; defaults to the event's created_at
		Expect      string     `json:"expect"`
	} `json:"setup"`
	Steps []struct {
		Envelope   fxEnvelope `json:"envelope"`
		To         string     `json:"to"`
		Now        int64      `json:"now"`
		Expect     string     `json:"expect"`
		StateAfter string     `json:"state_after"`
		Note       string     `json:"note"`
	} `json:"steps"`
}

func TestFixturesReferralFlow(t *testing.T) {
	known := map[string]bool{}
	for _, n := range fxFlowStates {
		known[n] = true
	}
	for _, c := range fxLoad[fxFlowCase](t, "seq-referral*.json", "referral_flow") {
		t.Run(c.Name, func(t *testing.T) {
			got, mismatch := fxRunFlow(t, c, known)
			if c.KnownGap != "" {
				fxGap(t, c.KnownGap, mismatch, got)
			}
			if mismatch {
				t.Error(got)
			}
		})
	}
}

// fxRunFlow returns a description of the first disagreement, if any.
func fxRunFlow(t *testing.T, c fxFlowCase, known map[string]bool) (string, bool) {
	req := c.Setup.Request.build(t)
	f, err := referral.NewFlow(req, c.Setup.FirstHolder, cmp.Or(c.Setup.Now, req.CreatedAt))
	switch want := cmp.Or(c.Setup.Expect, "ok"); {
	case want != "ok" && want != "reject":
		t.Fatalf("bad setup expect %q", want)
	case want == "reject":
		if err == nil {
			return "setup: got ok, want reject", true
		}
		return "", false
	case err != nil:
		return "setup: " + err.Error(), true
	}
	for i, st := range c.Steps {
		if !known[st.StateAfter] || st.Expect != "ok" && st.Expect != "reject" && st.Expect != "defer" {
			t.Fatalf("step %d: bad expect %q or state %q", i, st.Expect, st.StateAfter)
		}
		err := f.Apply(st.Envelope.build(t), st.To, st.Now)
		got, state := fxOutcome(err, referral.ErrTooEarly), fxFlowStates[f.State(st.Now)]
		if got != st.Expect || state != st.StateAfter {
			return fmt.Sprintf("step %d: got %s/%s (err %v), want %s/%s; note: %s",
				i, got, state, err, st.Expect, st.StateAfter, st.Note), true
		}
	}
	return "", false
}
