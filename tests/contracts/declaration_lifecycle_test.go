package contracts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

const declarationLifecycleMaxSafeInteger int64 = 9007199254740991
const declarationLifecycleMaxTTL int64 = 86400
const declarationLifecycleFutureAllowance int64 = 300

type declarationLifecycleRecord struct {
	Author    string `json:"author"`
	EventID   string `json:"event_id"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
	Action    string `json:"action"`
}

type declarationLifecycleExpectedSelection struct {
	Author  string `json:"author"`
	EventID string `json:"event_id,omitempty"`
	Status  string `json:"status"`
}

type declarationLifecycleRejectedRecord struct {
	EventID string `json:"event_id"`
	Reason  string `json:"reason"`
}

type declarationLifecycleCase struct {
	ID         string                                  `json:"id"`
	Now        int64                                   `json:"now"`
	PriorKnown []declarationLifecycleRecord            `json:"prior_known"`
	Records    []declarationLifecycleRecord            `json:"records"`
	Expected   []declarationLifecycleExpectedSelection `json:"expected"`
	Rejected   []declarationLifecycleRejectedRecord    `json:"rejected"`
	Error      string                                  `json:"error"`
}

type declarationLifecycleFixtures struct {
	Description string                     `json:"description"`
	Cases       []declarationLifecycleCase `json:"cases"`
}

type declarationLifecycleResult struct {
	Selections []declarationLifecycleExpectedSelection
	Rejected   []declarationLifecycleRejectedRecord
}

func TestDeclarationLifecycleFixtures(t *testing.T) {
	fixturePath := filepath.Join("testdata", "declaration-lifecycle-fixtures.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures declarationLifecycleFixtures
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	if len(fixtures.Cases) == 0 {
		t.Fatal("fixture set is empty")
	}
	seen := make(map[string]bool, len(fixtures.Cases))
	for _, tc := range fixtures.Cases {
		tc := tc
		t.Run(tc.ID, func(t *testing.T) {
			if tc.ID == "" || seen[tc.ID] {
				t.Fatalf("fixture id is empty or duplicated: %q", tc.ID)
			}
			seen[tc.ID] = true
			got, err := selectDeclarationLifecycleLatestKnown(tc.Now, tc.PriorKnown, tc.Records)
			if tc.Error != "" {
				if err == nil || err.Error() != tc.Error {
					t.Fatalf("error = %v, want %q", err, tc.Error)
				}
				if len(got.Selections) != 0 || len(got.Rejected) != 0 {
					t.Fatalf("failed selection returned partial update: %#v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectDeclarationLifecycleLatestKnown: %v", err)
			}
			if !reflect.DeepEqual(got.Selections, tc.Expected) {
				t.Errorf("selections = %#v, want %#v", got.Selections, tc.Expected)
			}
			if !reflect.DeepEqual(got.Rejected, tc.Rejected) {
				t.Errorf("rejected = %#v, want %#v", got.Rejected, tc.Rejected)
			}
		})
	}
}

// selectDeclarationLifecycleLatestKnown is a test-only reference reducer for
// structurally normalized records whose signature and identity checks are
// assumed. It independently checks lifecycle time semantics and deliberately
// models no storage, network completeness, authorization, or run behavior.
func selectDeclarationLifecycleLatestKnown(now int64, priorKnown, records []declarationLifecycleRecord) (declarationLifecycleResult, error) {
	if !isDeclarationLifecycleSafeTimestamp(now) {
		return declarationLifecycleResult{}, fmt.Errorf("invalid_now")
	}

	all := make([]declarationLifecycleRecord, 0, len(priorKnown)+len(records))
	all = append(all, priorKnown...)
	all = append(all, records...)

	// Detect event-id equivocation before applying time filters. Even a
	// malformed or future duplicate must not silently replace a known event.
	byID := make(map[string]declarationLifecycleRecord, len(all))
	unique := make([]declarationLifecycleRecord, 0, len(all))
	for _, record := range all {
		if old, ok := byID[record.EventID]; ok {
			if old != record {
				return declarationLifecycleResult{}, fmt.Errorf("conflict")
			}
			continue
		}
		byID[record.EventID] = record
		unique = append(unique, record)
	}

	latestByAuthor := make(map[string]declarationLifecycleRecord)
	seenAuthors := make(map[string]bool)
	result := declarationLifecycleResult{}
	for _, record := range unique {
		seenAuthors[record.Author] = true
		if !validDeclarationLifecycleRecord(record) {
			result.Rejected = append(result.Rejected, declarationLifecycleRejectedRecord{EventID: record.EventID, Reason: "invalid_lifecycle"})
			continue
		}
		if isDeclarationLifecycleTooFarInFuture(record.IssuedAt, now) {
			result.Rejected = append(result.Rejected, declarationLifecycleRejectedRecord{EventID: record.EventID, Reason: "future_clock"})
			continue
		}
		current, ok := latestByAuthor[record.Author]
		if !ok || record.IssuedAt > current.IssuedAt || (record.IssuedAt == current.IssuedAt && record.EventID < current.EventID) {
			latestByAuthor[record.Author] = record
		}
	}

	authors := make([]string, 0, len(seenAuthors))
	for author := range seenAuthors {
		authors = append(authors, author)
	}
	sort.Strings(authors)
	for _, author := range authors {
		record, ok := latestByAuthor[author]
		if !ok {
			result.Selections = append(result.Selections, declarationLifecycleExpectedSelection{Author: author, Status: "none"})
			continue
		}
		status := "inactive"
		if record.Action == "upsert" && now < record.ExpiresAt {
			status = "active"
		}
		result.Selections = append(result.Selections, declarationLifecycleExpectedSelection{Author: author, EventID: record.EventID, Status: status})
	}
	sort.Slice(result.Rejected, func(i, j int) bool { return result.Rejected[i].EventID < result.Rejected[j].EventID })
	return result, nil
}

func validDeclarationLifecycleRecord(record declarationLifecycleRecord) bool {
	if !isDeclarationLifecycleSafeTimestamp(record.IssuedAt) || !isDeclarationLifecycleSafeTimestamp(record.ExpiresAt) {
		return false
	}
	if record.ExpiresAt <= record.IssuedAt {
		return false
	}
	return record.ExpiresAt-record.IssuedAt <= declarationLifecycleMaxTTL
}

func isDeclarationLifecycleSafeTimestamp(value int64) bool {
	return value >= 0 && value <= declarationLifecycleMaxSafeInteger
}

func isDeclarationLifecycleTooFarInFuture(issuedAt, now int64) bool {
	return issuedAt > now && issuedAt-now > declarationLifecycleFutureAllowance
}
