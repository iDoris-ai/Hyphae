package messaging

import (
	"bytes"
	"os"
	"testing"
)

// p-o3 fixture result: generation 7, prior 0 → retry 1, group_pending/send_failed.
func fixtureO3Result(t *testing.T) RawFailureResult {
	t.Helper()
	e, err := ParseRawGroupEntry(readF2BFixture(t, "p-o3-entry.json"))
	if err != nil || e.FailureResult == nil {
		t.Fatalf("P-O3 fixture: %v", err)
	}
	return *e.FailureResult
}

func scanO3Target(t *testing.T, raw []byte, queueID string) *RawGroupEntry {
	t.Helper()
	file, err := scanRawOutboxFile(raw)
	if err != nil {
		t.Fatalf("scan written outbox: %v", err)
	}
	entry, err := file.entryByQueueID(queueID)
	if err != nil {
		t.Fatal(err)
	}
	return entry.Parsed
}

func TestCommitOutboxO3LegacyEntry(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	before := rawOutboxDoc(dm, group)
	path := writeOutboxFile(t, before)
	result := fixtureO3Result(t)
	if err := commitOutboxO3(path, outboxO3{Result: result}); err != nil {
		t.Fatalf("O3: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Contains(after, dm) {
		t.Fatal("unrelated DM entry bytes changed")
	}
	got := scanO3Target(t, after, result.Token.QueueID)
	if got.Entry.RetryCount != 1 || got.Entry.Status != OutboxStatusGroupPending || got.Entry.LastAttempt != 1791539070 {
		t.Fatalf("O3 post-image = %+v", got.Entry)
	}
	if got.FanoutProtocol == nil || got.FailureResult == nil || got.FailureResult.ResultID != result.ResultID {
		t.Fatalf("O3 metadata = %+v", got)
	}
	old, err := ParseRawGroupEntry(group)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{"queue_id", "route", "id", "event_json", "recipient_npub", "relays", "max_retries", "created_at"} {
		if !bytes.Equal(got.Fields[member], old.Fields[member]) {
			t.Errorf("%s fragment changed: %q → %q", member, old.Fields[member], got.Fields[member])
		}
	}
}

func TestCommitOutboxO3Exhaustion(t *testing.T) {
	group := bytes.Replace(legacyFixtureEntry(t, "legacy-165-group-nil-relays.json"), []byte(`"retry_count": 0`), []byte(`"retry_count": 9`), 1)
	result := fixtureO3Result(t)
	result.PriorRetryCount, result.RetryCount = 9, 10

	pending := writeOutboxFile(t, rawOutboxDoc(group))
	if err := commitOutboxO3(pending, outboxO3{Result: result}); err == nil {
		t.Fatal("exhausting O3 recorded as group_pending")
	}
	requireOutboxUnchanged(t, pending, rawOutboxDoc(group))

	result.Status, result.Issue = OutboxStatusGroupFailed, "retry_exhausted"
	path := writeOutboxFile(t, rawOutboxDoc(group))
	if err := commitOutboxO3(path, outboxO3{Result: result}); err != nil {
		t.Fatalf("exhausting O3: %v", err)
	}
	after, _ := os.ReadFile(path)
	got := scanO3Target(t, after, result.Token.QueueID)
	if got.Entry.Status != OutboxStatusGroupFailed || got.Entry.RetryCount != 10 || got.FailureResult.Issue != "retry_exhausted" {
		t.Fatalf("exhausted post-image = %+v / %+v", got.Entry, got.FailureResult)
	}
}

func TestCommitOutboxO3ReplacesConsumedResult(t *testing.T) {
	o3 := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	before := rawOutboxDoc(o3)
	parsed, err := ParseRawGroupEntry(o3)
	if err != nil {
		t.Fatal(err)
	}
	result := fixtureO3Result(t)
	result.Token.AttemptGeneration, result.PriorRetryCount, result.RetryCount = 8, 1, 2
	consumed := sha256Hex(parsed.Fields["failure_result"])
	sameGeneration := result
	sameGeneration.Token.AttemptGeneration = 7

	for name, o3 := range map[string]outboxO3{
		"unattested":      {Result: result},
		"wrong digest":    {Result: result, ConsumedResultDigest: sha256Hex([]byte("x"))},
		"same generation": {Result: sameGeneration, ConsumedResultDigest: consumed},
	} {
		path := writeOutboxFile(t, before)
		err := commitOutboxO3(path, o3)
		t.Logf("%s: %v", name, err)
		if err == nil {
			t.Errorf("%s: expected refusal", name)
		}
		requireOutboxUnchanged(t, path, before)
	}

	path := writeOutboxFile(t, before)
	if err := commitOutboxO3(path, outboxO3{Result: result, ConsumedResultDigest: consumed}); err != nil {
		t.Fatalf("O3 over consumed result: %v", err)
	}
	after, _ := os.ReadFile(path)
	if bytes.Count(after, []byte(`"fanout_protocol"`)) != 1 || bytes.Count(after, []byte(`"failure_result"`)) != 1 {
		t.Fatalf("metadata duplicated:\n%s", after)
	}
	got := scanO3Target(t, after, result.Token.QueueID)
	if got.FailureResult.Token.AttemptGeneration != 8 || got.Entry.RetryCount != 2 || !bytes.Equal(got.Fields["relays"], parsed.Fields["relays"]) {
		t.Fatalf("post-image = %+v", got.Entry)
	}
}

func TestCommitOutboxO3KeepsConsumedWitness(t *testing.T) {
	missing := bytes.TrimSpace(readF2BFixture(t, "p-missing-entry.json"))
	parsed, err := ParseRawGroupEntry(missing)
	if err != nil {
		t.Fatal(err)
	}
	result := fixtureO3Result(t)
	result.Token.QueueID = parsed.QueueID
	result.Token.AttemptGeneration = 8

	unconsumed := writeOutboxFile(t, rawOutboxDoc(missing))
	if err := commitOutboxO3(unconsumed, outboxO3{Result: result}); err == nil {
		t.Fatal("O3 accepted beside an unattested witness")
	}
	requireOutboxUnchanged(t, unconsumed, rawOutboxDoc(missing))

	path := writeOutboxFile(t, rawOutboxDoc(missing))
	if err := commitOutboxO3(path, outboxO3{Result: result, ConsumedWitnessDigest: sha256Hex(parsed.Fields["recovery_witness"])}); err != nil {
		t.Fatalf("O3 beside consumed witness: %v", err)
	}
	after, _ := os.ReadFile(path)
	got := scanO3Target(t, after, parsed.QueueID)
	if !bytes.Equal(got.Fields["recovery_witness"], parsed.Fields["recovery_witness"]) || got.FailureResult == nil {
		t.Fatal("witness fragment changed or result missing")
	}
}

func TestCommitOutboxO3Refusals(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	before := rawOutboxDoc(dm, group)
	base := fixtureO3Result(t)
	mutate := func(edit func(*RawFailureResult)) RawFailureResult {
		r := base
		edit(&r)
		return r
	}
	for name, result := range map[string]RawFailureResult{
		"queue missing": mutate(func(r *RawFailureResult) { r.Token.QueueID = "ffffffffffffffffffffffffffffffff" }),
		"DM target":     mutate(func(r *RawFailureResult) { r.Token.QueueID = "219bdbf9ed70b96893cc2382f365f24c" }),
		"event mismatch": mutate(func(r *RawFailureResult) {
			r.Token.EventID = "7c3d244aadd48d6fca073dad9cbe061e379a4d5281fe67465185a696790d7bda"
		}),
		"recipient mismatch": mutate(func(r *RawFailureResult) { r.Token.FanoutKey.RecipientNpub = r.Token.FanoutKey.LocalNpub }),
		"sender mismatch":    mutate(func(r *RawFailureResult) { r.Token.FanoutKey.LocalNpub = r.Token.FanoutKey.RecipientNpub }),
		"prior mismatch":     mutate(func(r *RawFailureResult) { r.PriorRetryCount, r.RetryCount = 1, 2 }),
		"invalid result":     mutate(func(r *RawFailureResult) { r.Attempts = 0 }),
		"generation zero":    mutate(func(r *RawFailureResult) { r.Token.AttemptGeneration = 0 }),
	} {
		path := writeOutboxFile(t, before)
		err := commitOutboxO3(path, outboxO3{Result: result})
		t.Logf("%s: %v", name, err)
		if err == nil {
			t.Errorf("%s: expected refusal", name)
		}
		requireOutboxUnchanged(t, path, before)
	}

	path := writeOutboxFile(t, before)
	if err := commitOutboxO3(path, outboxO3{Result: base, ConsumedResultDigest: sha256Hex(nil)}); err != nil {
		t.Errorf("receipt digest of an absent result must not block O3: %v", err)
	}

	failed := bytes.Replace(bytes.Replace(group, []byte(`"group_pending"`), []byte(`"group_failed"`), 1), []byte(`"retry_count": 0`), []byte(`"retry_count": 10`), 1)
	path = writeOutboxFile(t, rawOutboxDoc(failed))
	if err := commitOutboxO3(path, outboxO3{Result: mutate(func(r *RawFailureResult) { r.PriorRetryCount, r.RetryCount = 10, 11 })}); err == nil {
		t.Error("O3 accepted on a group_failed entry")
	}
	requireOutboxUnchanged(t, path, rawOutboxDoc(failed))
}

func TestEncodeFailureResultRoundTrip(t *testing.T) {
	r := fixtureO3Result(t)
	r.Token.QueueID = "legacy\n\"queue\"/é"
	encoded := encodeFailureResult(&r)
	parsed, err := ParseRawFailureResult(encoded)
	if err != nil {
		t.Fatalf("parse encoded: %v\n%s", err, encoded)
	}
	if parsed.Token != r.Token || parsed.ResultID != failureResultID(r.Token) || !bytes.Contains(encoded, []byte(`legacy\u000a\"queue\"/é`)) {
		t.Fatalf("round trip mismatch: %s", encoded)
	}
	fixture := fixtureO3Result(t)
	again, err := ParseRawFailureResult(encodeFailureResult(&fixture))
	if err != nil || again.ResultID != fixture.ResultID || again.Token != fixture.Token || again.Attempts != fixture.Attempts || again.Issue != fixture.Issue {
		t.Fatalf("fixture re-encoding differs: %+v (%v)", again, err)
	}
}
