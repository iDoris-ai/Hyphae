package contracts

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"
)

const receiptStateFixtureProfile = "hyphae-receipt-state-fixtures/1"

const receiptStateMaxSafeInteger uint64 = 9007199254740991

var receiptStateTransitions = map[string][]string{
	"received":         {"approval_pending", "ready", "rejected", "expired"},
	"approval_pending": {"ready", "rejected", "expired"},
	"ready":            {"run_registered", "rejected", "expired"},
	"run_registered":   {"running", "failed", "expired", "unknown"},
	"running":          {"succeeded", "failed", "unknown"},
	"unknown":          {"running", "succeeded", "failed"},
}

var receiptStateNames = map[string]bool{
	"received": true, "approval_pending": true, "ready": true,
	"run_registered": true, "running": true, "unknown": true,
	"succeeded": true, "failed": true, "rejected": true, "expired": true,
}

type receiptStateRecord struct {
	State      string `json:"state"`
	Seq        uint64 `json:"seq"`
	RunID      string `json:"run_id"`
	SemanticID string `json:"semantic_id"`
}

type receiptStateIncoming struct {
	receiptStateRecord
	CorrelationOK    *bool `json:"correlation_ok"`
	CompleteSnapshot *bool `json:"complete_snapshot"`
}

type receiptStateDirectCase struct {
	ID                string `json:"id"`
	From              string `json:"from"`
	To                string `json:"to"`
	ExpectedDirect    bool   `json:"expected_direct"`
	ExpectedReachable bool   `json:"expected_reachable"`
}

type receiptStateRevisionCase struct {
	ID               string               `json:"id"`
	Prior            receiptStateRecord   `json:"prior"`
	Incoming         receiptStateIncoming `json:"incoming"`
	ExpectedDecision string               `json:"expected_decision"`
	ExpectedRecord   receiptStateRecord   `json:"expected_record"`
}

type receiptStateFixtures struct {
	Profile       string                     `json:"profile"`
	DirectCases   []receiptStateDirectCase   `json:"direct_cases"`
	RevisionCases []receiptStateRevisionCase `json:"revision_cases"`
}

func receiptStateDirectAllowed(from, to string) bool {
	for _, next := range receiptStateTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

func receiptStateReachable(from, to string) bool {
	if !receiptStateNames[from] || !receiptStateNames[to] {
		return false
	}
	if from == to {
		return true
	}
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range receiptStateTransitions[current] {
			if next == to {
				return true
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

func receiptStateIsTerminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "rejected" || state == "expired"
}

func receiptStateRequiresRun(state string) bool {
	return state == "run_registered" || state == "running" || state == "unknown" || state == "succeeded" || state == "failed"
}

func receiptStateForbidsRun(state string) bool {
	return state == "received" || state == "approval_pending" || state == "ready" || state == "rejected"
}

func receiptStateRecordValid(record receiptStateRecord, incoming bool) bool {
	if !receiptStateNames[record.State] || record.Seq > receiptStateMaxSafeInteger {
		return false
	}
	if incoming {
		if record.Seq == 0 || record.SemanticID == "" {
			return false
		}
	} else if record.Seq == 0 {
		return record.State == "received" && record.RunID == "" && record.SemanticID == ""
	} else if record.SemanticID == "" {
		return false
	}
	if receiptStateRequiresRun(record.State) && record.RunID == "" {
		return false
	}
	if receiptStateForbidsRun(record.State) && record.RunID != "" {
		return false
	}
	return true
}

func receiptStateApply(prior receiptStateRecord, incoming receiptStateIncoming) (string, receiptStateRecord) {
	if !receiptStateRecordValid(prior, false) || incoming.CorrelationOK == nil || incoming.CompleteSnapshot == nil || !receiptStateRecordValid(incoming.receiptStateRecord, true) {
		return "invalid_structure", prior
	}
	if !*incoming.CorrelationOK {
		return "correlation_rejected", prior
	}
	if !*incoming.CompleteSnapshot {
		return "incomplete_snapshot", prior
	}
	if prior.RunID != "" && incoming.RunID != "" && incoming.RunID != prior.RunID {
		return "run_mismatch", prior
	}
	if incoming.Seq < prior.Seq {
		return "stale", prior
	}
	if incoming.Seq == prior.Seq {
		if prior.State == incoming.State && prior.RunID == incoming.RunID && prior.SemanticID == incoming.SemanticID {
			return "duplicate", prior
		}
		return "revision_conflict", prior
	}
	if receiptStateIsTerminal(prior.State) {
		return "terminal_locked", prior
	}
	if prior.RunID != "" && incoming.RunID == "" {
		return "run_mismatch", prior
	}
	if !receiptStateReachable(prior.State, incoming.State) {
		return "unreachable", prior
	}
	return "applied", incoming.receiptStateRecord
}

func receiptStateReadFixtures(t *testing.T) receiptStateFixtures {
	t.Helper()
	data, err := os.ReadFile("testdata/receipt-state-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var fixtures receiptStateFixtures
	if err := dec.Decode(&fixtures); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatal("fixture file must contain exactly one JSON value")
	}
	return fixtures
}

func TestReceiptStateSharedFixtures(t *testing.T) {
	fixtures := receiptStateReadFixtures(t)
	if fixtures.Profile != receiptStateFixtureProfile || len(fixtures.DirectCases) < 25 || len(fixtures.RevisionCases) < 32 {
		t.Fatalf("unexpected receipt-state fixture profile/count: %q/%d direct/%d revision", fixtures.Profile, len(fixtures.DirectCases), len(fixtures.RevisionCases))
	}

	ids := map[string]bool{}
	seenEdges := map[string]bool{}
	for _, tc := range fixtures.DirectCases {
		tc := tc
		if tc.ID == "" || ids[tc.ID] {
			t.Fatalf("empty or duplicate fixture id %q", tc.ID)
		}
		ids[tc.ID] = true
		t.Run("edge_"+tc.ID, func(t *testing.T) {
			if got := receiptStateDirectAllowed(tc.From, tc.To); got != tc.ExpectedDirect {
				t.Errorf("directAllowed(%s,%s)=%t, want %t", tc.From, tc.To, got, tc.ExpectedDirect)
			}
			if got := receiptStateReachable(tc.From, tc.To); got != tc.ExpectedReachable {
				t.Errorf("reachable(%s,%s)=%t, want %t", tc.From, tc.To, got, tc.ExpectedReachable)
			}
			if tc.ExpectedDirect {
				key := tc.From + "\x00" + tc.To
				if seenEdges[key] {
					t.Errorf("duplicate direct edge %s -> %s", tc.From, tc.To)
				}
				seenEdges[key] = true
			}
		})
	}
	expectedEdgeCount := 0
	for from, nextStates := range receiptStateTransitions {
		expectedEdgeCount += len(nextStates)
		for _, to := range nextStates {
			if !seenEdges[from+"\x00"+to] {
				t.Errorf("fixture suite omits direct edge %s -> %s", from, to)
			}
		}
	}
	if len(seenEdges) != expectedEdgeCount {
		t.Fatalf("fixture suite covered %d direct edges, want %d", len(seenEdges), expectedEdgeCount)
	}

	decisionCounts := map[string]int{}
	for _, tc := range fixtures.RevisionCases {
		tc := tc
		if tc.ID == "" || ids[tc.ID] {
			t.Fatalf("empty or duplicate fixture id %q", tc.ID)
		}
		ids[tc.ID] = true
		t.Run(tc.ID, func(t *testing.T) {
			decision, got := receiptStateApply(tc.Prior, tc.Incoming)
			decisionCounts[decision]++
			if decision != tc.ExpectedDecision {
				t.Errorf("decision %q, want %q", decision, tc.ExpectedDecision)
			}
			if got != tc.ExpectedRecord {
				t.Errorf("record %+v, want %+v", got, tc.ExpectedRecord)
			}
		})
	}
	for _, decision := range []string{"applied", "duplicate", "stale", "revision_conflict", "invalid_structure", "correlation_rejected", "incomplete_snapshot", "run_mismatch", "terminal_locked", "unreachable"} {
		if decisionCounts[decision] == 0 {
			t.Errorf("fixture suite did not exercise decision %q", decision)
		}
	}
	if len(ids) != len(fixtures.DirectCases)+len(fixtures.RevisionCases) {
		t.Fatalf("not all unique fixtures executed; found %d", len(ids))
	}
}
