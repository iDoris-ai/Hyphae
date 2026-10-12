package messaging

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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

func TestCommitGroupRecoveryLockedMissingFile(t *testing.T) {
	want, missing := fixtureMissingWitness(t)
	failed := exhaustedGroupEntry(t, OutboxStatusGroupFailed)
	exhausted := exhaustedWitness(t, failed, witnessReasonExhaustedFailed, OutboxStatusGroupFailed, 10)

	t.Run("missing reason creates file and writes D6", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "outbox.json")
		digest, err := CommitGroupRecoveryLocked(path, GroupRecoveryRecord{Witness: missing, FrozenEventJSON: want.Entry.EventJSON})
		if err != nil {
			t.Fatalf("missing recovery on absent file: %v", err)
		}
		if digest == "" {
			t.Fatal("empty digest")
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading created outbox: %v", err)
		}
		got := scanO3Target(t, after, missing.NewQueueID)
		if got == nil || got.RecoveryWitness == nil || OutboxWitnessDigest(got) != digest {
			t.Fatalf("witness mismatch: got=%+v", got)
		}
		if got.RecoveryWitness.Reason != "missing" || !got.RecoveryWitness.ObservedAbsent {
			t.Fatalf("witness reason/absent mismatch: %+v", got.RecoveryWitness)
		}
	})

	t.Run("exhausted reason on absent file fails closed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "outbox.json")
		if _, err := CommitGroupRecoveryLocked(path, GroupRecoveryRecord{Witness: exhausted}); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("exhausted on absent file must return ErrNotExist, got: %v", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("outbox file must remain absent")
		}
	})

	t.Run("injected failure on absent file rolls back cleanly", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "outbox.json")
		badDigestWitness := missing
		badDigestWitness.EventJSONDigest = sha256Hex([]byte("mismatched-event"))
		if _, err := CommitGroupRecoveryLocked(path, GroupRecoveryRecord{Witness: badDigestWitness, FrozenEventJSON: want.Entry.EventJSON}); err == nil {
			t.Fatal("mismatched event digest must fail")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("outbox file must remain absent after injected failure")
		}

		if _, err := CommitGroupRecoveryLocked(path, GroupRecoveryRecord{Witness: missing, FrozenEventJSON: ""}); err == nil {
			t.Fatal("missing reason with empty event must fail")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("outbox file must remain absent after empty event failure")
		}
	})
}
