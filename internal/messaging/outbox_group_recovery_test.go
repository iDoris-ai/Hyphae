package messaging

import (
	"bytes"
	"os"
	"testing"
)

func TestCommitGroupRecoveryLocked(t *testing.T) {
	failed := exhaustedGroupEntry(t, OutboxStatusGroupFailed)
	before := rawOutboxDoc(failed)
	w := exhaustedWitness(t, failed, witnessReasonExhaustedFailed, OutboxStatusGroupFailed, 10)

	path := writeOutboxFile(t, before)
	digest, err := CommitGroupRecoveryLocked(path, GroupRecoveryRecord{Witness: w})
	if err != nil {
		t.Fatalf("D6: %v", err)
	}
	after, _ := os.ReadFile(path)
	got := scanO3Target(t, after, w.NewQueueID)
	if got.RecoveryWitness == nil || OutboxWitnessDigest(got) != digest || digest == "" {
		t.Fatalf("digest %q does not match the written witness", digest)
	}
	if OutboxEntryDigest(got) != sha256Hex(got.Raw) || OutboxWitnessDigest(&RawGroupEntry{}) != "" || OutboxWitnessDigest(nil) != "" {
		t.Fatal("digest helpers disagree with the raw bytes")
	}
	if OutboxEventDigest(got.Entry.EventJSON) != got.RecoveryWitness.EventJSONDigest {
		t.Fatal("event digest helper disagrees with the fixture witness")
	}

	// Refusals write nothing: wrong frozen event for missing, stale digest.
	_, missing := fixtureMissingWitness(t)
	for name, rec := range map[string]GroupRecoveryRecord{
		"missing without event": {Witness: missing},
		"exhausted stale digest": {Witness: func() RawRecoveryWitness {
			x := w
			x.OldEntryDigest = sha256Hex(nil)
			return x
		}()},
	} {
		path := writeOutboxFile(t, before)
		if d, err := CommitGroupRecoveryLocked(path, rec); err == nil || d != "" {
			t.Errorf("%s: digest=%q err=%v", name, d, err)
		}
		requireOutboxUnchanged(t, path, before)
	}
	if !bytes.Contains(after, []byte(`"recovery_witness"`)) {
		t.Fatal("witness missing from the written file")
	}
}
