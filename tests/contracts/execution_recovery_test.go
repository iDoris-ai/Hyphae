package contracts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const executionRecoveryMaxSafeInteger int64 = 9007199254740991
const executionRecoveryMaxTTL int64 = 86400
const executionRecoveryFutureAllowance int64 = 300

type executionRecoveryFixtures struct {
	Metadata struct {
		Format                          string `json:"format"`
		Semantics                       string `json:"semantics"`
		BaseCommit                      string `json:"base_commit"`
		AuthorizationCandidateSourceSHA string `json:"authorization_candidate_source_sha256"`
		ContractGatesSourceSHA          string `json:"contract_gates_source_sha256"`
		CaseCount                       int    `json:"case_count"`
	} `json:"metadata"`
	Cases []executionRecoveryCase `json:"cases"`
}

func assertExecutionRecoveryCandidateEdges(t *testing.T, cases []executionRecoveryCase) {
	t.Helper()
	candidate, err := os.ReadFile("../../docs/agent/t01-authorization-recovery-candidate.md")
	if err != nil {
		t.Fatal(err)
	}
	lineRE := regexp.MustCompile(`^([a-z_]+)\s+-->\s+([a-z_]+)$`)
	candidateEdges := map[string]bool{}
	inDiagram := false
	for _, line := range strings.Split(string(candidate), "\n") {
		line = strings.TrimSpace(line)
		if line == "```mermaid" {
			inDiagram = true
			continue
		}
		if inDiagram && line == "```" {
			break
		}
		if !inDiagram {
			continue
		}
		matches := lineRE.FindStringSubmatch(line)
		if len(matches) == 3 {
			candidateEdges[matches[1]+"->"+matches[2]] = true
		}
	}
	fixtureEdges := map[string]bool{}
	for _, tc := range cases {
		if !strings.HasPrefix(tc.ID, "transition_legal_") {
			continue
		}
		from, to := stringValue(tc.Input["from"]), stringValue(tc.Input["to"])
		key := from + "->" + to
		if fixtureEdges[key] {
			t.Fatalf("duplicate legal transition fixture for %s", key)
		}
		fixtureEdges[key] = true
	}
	if len(candidateEdges) != len(fixtureEdges) {
		t.Fatalf("candidate has %d legal edges, fixtures cover %d", len(candidateEdges), len(fixtureEdges))
	}
	for edge := range candidateEdges {
		if !fixtureEdges[edge] {
			t.Errorf("candidate legal edge %s has no transition_legal fixture", edge)
		}
	}
	for edge := range fixtureEdges {
		if !candidateEdges[edge] {
			t.Errorf("fixture edge %s is absent from candidate graph", edge)
		}
	}
}

type executionRecoveryCase struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"`
	Input    map[string]any `json:"input"`
	Expected struct {
		Action string `json:"action"`
		State  string `json:"state,omitempty"`
		Reason string `json:"reason,omitempty"`
	} `json:"expected"`
}

func TestExecutionRecoveryFixtures(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "execution-recovery-fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures executionRecoveryFixtures
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fixtures); err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	if fixtures.Metadata.Format != "hyphae-execution-recovery-fixtures-v1" || fixtures.Metadata.Semantics != "test-observation-only" {
		t.Fatalf("unexpected fixture manifest format/semantics %q/%q", fixtures.Metadata.Format, fixtures.Metadata.Semantics)
	}
	if fixtures.Metadata.BaseCommit != "a4aa606eb81d5c040d94c51cdf94553e646d8674" {
		t.Fatalf("fixture base commit = %q", fixtures.Metadata.BaseCommit)
	}
	for _, source := range []struct{ path, want string }{
		{"../../docs/agent/t01-authorization-recovery-candidate.md", fixtures.Metadata.AuthorizationCandidateSourceSHA},
		{"../../docs/agent/t01-contract-gates.md", fixtures.Metadata.ContractGatesSourceSHA},
	} {
		sourceBytes, err := os.ReadFile(source.path)
		if err != nil {
			t.Fatalf("read manifest source %s: %v", source.path, err)
		}
		sum := sha256.Sum256(sourceBytes)
		if got := hex.EncodeToString(sum[:]); got != source.want {
			t.Fatalf("source SHA-256 for %s = %s, want %s", source.path, got, source.want)
		}
	}
	assertExecutionRecoveryCandidateEdges(t, fixtures.Cases)
	if fixtures.Metadata.CaseCount != len(fixtures.Cases) || len(fixtures.Cases) < 30 {
		t.Fatalf("case_count = %d, decoded cases = %d; want matching count and at least 30", fixtures.Metadata.CaseCount, len(fixtures.Cases))
	}
	seen := make(map[string]bool, len(fixtures.Cases))
	for _, tc := range fixtures.Cases {
		tc := tc
		t.Run(tc.ID, func(t *testing.T) {
			if tc.ID == "" || seen[tc.ID] {
				t.Fatalf("fixture id is empty or duplicated: %q", tc.ID)
			}
			seen[tc.ID] = true
			got, err := decideExecutionRecovery(tc.Kind, tc.Input)
			if err != nil {
				t.Fatalf("reference decision: %v", err)
			}
			if got.Action != tc.Expected.Action || got.State != tc.Expected.State || got.Reason != tc.Expected.Reason {
				t.Errorf("decision = %#v, want action=%q state=%q reason=%q", got, tc.Expected.Action, tc.Expected.State, tc.Expected.Reason)
			}
		})
	}
	if len(seen) != len(fixtures.Cases) {
		t.Fatalf("checked %d distinct fixture IDs, want %d", len(seen), len(fixtures.Cases))
	}
}

type executionRecoveryDecision struct {
	Action string
	State  string
	Reason string
}

// decideExecutionRecovery is a test-only reference oracle. It maps supplied
// observations to permitted actions and candidate state transitions; it does
// not run requests, persist state, or count executions or side effects.
func decideExecutionRecovery(kind string, in map[string]any) (executionRecoveryDecision, error) {
	switch kind {
	case "clock":
		return checkExecutionRecoveryClock(in), nil
	case "preflight":
		if reason := executionRecoveryPreflightFailure(in); reason != "" {
			state := "ready"
			if reason == "expired" {
				state = "expired"
			}
			return executionRecoveryDecision{Action: "reject", State: state, Reason: reason}, nil
		}
		return executionRecoveryDecision{Action: "register_run", State: "run_registered"}, nil
	case "replay":
		if !boolValue(in["same_author"]) || !boolValue(in["same_target"]) || !boolValue(in["same_request_id"]) {
			return executionRecoveryDecision{Action: "new_request"}, nil
		}
		if boolValue(in["same_immutable_content"]) {
			return executionRecoveryDecision{Action: "reuse_existing"}, nil
		}
		return executionRecoveryDecision{Action: "reject", Reason: "request_conflict"}, nil
	case "transition":
		from, to := stringValue(in["from"]), stringValue(in["to"])
		facts, _ := in["facts"].(map[string]any)
		if !isAllowedExecutionRecoveryEdge(from, to) || !executionRecoveryEdgeFactsHold(from, to, facts) {
			return executionRecoveryDecision{Action: "retain_state", State: from, Reason: "transition_or_fact_not_allowed"}, nil
		}
		return executionRecoveryDecision{Action: "apply_transition", State: to}, nil
	case "recovery":
		return recoverExecutionObservation(in), nil
	default:
		return executionRecoveryDecision{}, fmt.Errorf("unknown fixture kind %q", kind)
	}
}

func checkExecutionRecoveryClock(in map[string]any) executionRecoveryDecision {
	now, nowOK := intValue(in["now"])
	issued, issuedOK := intValue(in["issued_at"])
	expires, expiresOK := intValue(in["expires_at"])
	if !nowOK || !issuedOK || !expiresOK || !isExecutionRecoverySafeInteger(now) || !isExecutionRecoverySafeInteger(issued) || !isExecutionRecoverySafeInteger(expires) {
		return executionRecoveryDecision{Action: "reject", Reason: "schema_integer"}
	}
	if expires <= issued || expires-issued > executionRecoveryMaxTTL {
		return executionRecoveryDecision{Action: "reject", Reason: "invalid_lifecycle"}
	}
	if issued > now && issued-now > executionRecoveryFutureAllowance {
		return executionRecoveryDecision{Action: "reject", Reason: "future_clock"}
	}
	if now >= expires {
		return executionRecoveryDecision{Action: "deny_expired", Reason: "expired"}
	}
	return executionRecoveryDecision{Action: "allow_start"}
}

// executionRecoveryPreflightFailure is shared by initial registration,
// ready-to-run_registered, and recovery of an existing not-yet-started run.
// Model observations are test context only: only model-dependent capabilities
// require a selected model, while privacy_ok covers the selected route's policy.
func executionRecoveryPreflightFailure(in map[string]any) string {
	if d := checkExecutionRecoveryClock(in); d.Action != "allow_start" {
		return d.Reason
	}
	for _, gate := range []struct{ key, reason string }{{"approval_persisted", "unauthorized"}, {"scope_digest_match", "scope_or_digest_mismatch"}, {"budget_ok", "budget_rejected"}, {"privacy_ok", "privacy_requirement_unmet"}, {"module_allowed", "module_forbidden"}} {
		if !boolValue(in[gate.key]) {
			return gate.reason
		}
	}
	modelRequired, present := boolObservation(in, "model_required")
	if !present {
		return "model_requirement_unknown"
	}
	if modelRequired {
		modelAvailable, available := boolObservation(in, "selected_model_available")
		if !available {
			return "model_availability_unknown"
		}
		if !modelAvailable {
			return "selected_model_unavailable"
		}
	}
	return ""
}

func boolObservation(in map[string]any, key string) (bool, bool) {
	value, ok := in[key].(bool)
	return value, ok
}

func isExecutionRecoverySafeInteger(value int64) bool {
	return value >= -executionRecoveryMaxSafeInteger && value <= executionRecoveryMaxSafeInteger
}

func isAllowedExecutionRecoveryEdge(from, to string) bool {
	edges := map[string]map[string]bool{
		"received":         {"approval_pending": true, "ready": true, "rejected": true, "expired": true},
		"approval_pending": {"ready": true, "rejected": true, "expired": true},
		"ready":            {"run_registered": true, "rejected": true, "expired": true},
		"run_registered":   {"running": true, "failed": true, "expired": true, "unknown": true},
		"running":          {"succeeded": true, "failed": true, "unknown": true},
		"unknown":          {"running": true, "succeeded": true, "failed": true},
		"succeeded":        {},
		"failed":           {},
		"rejected":         {},
		"expired":          {},
	}
	return edges[from][to]
}

func executionRecoveryEdgeFactsHold(from, to string, facts map[string]any) bool {
	switch {
	case to == "ready":
		return boolValue(facts["approval_persisted"]) && boolValue(facts["scope_digest_match"])
	case from == "ready" && to == "run_registered":
		return executionRecoveryPreflightFailure(facts) == ""
	case from == "run_registered" && to == "expired":
		now, nowOK := intValue(facts["now"])
		expires, expiresOK := intValue(facts["expires_at"])
		return stringValue(facts["start_call"]) == "confirmed_not_sent_no_start" && nowOK && expiresOK && now >= expires
	case to == "expired":
		now, nowOK := intValue(facts["now"])
		expires, expiresOK := intValue(facts["expires_at"])
		return nowOK && expiresOK && now >= expires
	case to == "running" && from == "run_registered":
		return stringValue(facts["start_call"]) == "confirmed_started"
	case to == "running" && from == "unknown":
		return boolValue(facts["same_run"]) && boolValue(facts["same_run_confirmed_started"])
	case to == "failed":
		return stringValue(facts["executor_outcome"]) == "explicit_failure" && boolValue(facts["result_saved"]) && boolValue(facts["same_run"])
	case to == "succeeded":
		return stringValue(facts["executor_outcome"]) == "explicit_success" && boolValue(facts["result_saved"]) && boolValue(facts["same_run"])
	case to == "unknown" && from == "run_registered":
		return stringValue(facts["start_call"]) == "uncertain"
	case to == "unknown" && from == "running":
		return stringValue(facts["executor_outcome"]) == "uncertain"
	default:
		return true
	}
}

func recoverExecutionObservation(in map[string]any) executionRecoveryDecision {
	state := stringValue(in["state"])
	switch state {
	case "run_registered":
		if stringValue(in["start_call"]) == "confirmed_not_sent_no_start" {
			now, nowOK := intValue(in["now"])
			expires, expiresOK := intValue(in["expires_at"])
			if nowOK && expiresOK && now >= expires {
				return executionRecoveryDecision{Action: "expire", State: "expired"}
			}
			if !boolValue(in["same_run_id_present"]) {
				return executionRecoveryDecision{Action: "hold_for_recheck", State: state, Reason: "run_id_missing"}
			}
			if executionRecoveryPreflightFailure(in) != "" {
				return executionRecoveryDecision{Action: "hold_for_recheck", State: state, Reason: "preflight_incomplete"}
			}
			return executionRecoveryDecision{Action: "start_existing_run", State: state}
		}
		return executionRecoveryDecision{Action: "query_same_run", State: "unknown"}
	case "unknown":
		if !boolValue(in["same_run_id_present"]) {
			return executionRecoveryDecision{Action: "query_same_run", State: state, Reason: "run_id_missing"}
		}
		observation := stringValue(in["executor_observation"])
		if observation != "not_found" && !boolValue(in["same_run"]) {
			return executionRecoveryDecision{Action: "query_same_run", State: state, Reason: "observation_mismatch"}
		}
		if (observation == "same_run_succeeded" || observation == "same_run_failed") && !boolValue(in["result_saved"]) {
			return executionRecoveryDecision{Action: "query_same_run", State: state, Reason: "result_not_saved_no_completion_receipt"}
		}
		resolved := map[string]string{"same_run_running": "running", "same_run_succeeded": "succeeded", "same_run_failed": "failed"}[observation]
		if resolved == "" {
			resolved = "unknown"
		}
		return executionRecoveryDecision{Action: "query_same_run", State: resolved}
	case "running":
		if stringValue(in["result_transaction"]) == "failed" {
			return executionRecoveryDecision{Action: "query_same_run", State: "running", Reason: "no_completion_receipt"}
		}
		return executionRecoveryDecision{Action: "query_same_run", State: state}
	case "succeeded", "failed":
		if boolValue(in["result_saved"]) && boolValue(in["receipt_pending"]) {
			return executionRecoveryDecision{Action: "resend_receipt", State: state}
		}
		return executionRecoveryDecision{Action: "no_action", State: state}
	default:
		return executionRecoveryDecision{Action: "retain_state", State: state, Reason: "unknown_recovery_state"}
	}
}

func boolValue(value any) bool     { b, _ := value.(bool); return b }
func stringValue(value any) string { s, _ := value.(string); return s }
func intValue(value any) (int64, bool) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := n.Int64()
	if err != nil || !isExecutionRecoverySafeInteger(parsed) {
		return 0, false
	}
	return parsed, true
}
