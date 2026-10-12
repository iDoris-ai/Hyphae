package messaging

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

// GroupRecoveryRecord is the D6 input derived from the locked failed SQLite
// row. Witness's typed members are serialized (Raw/Fields ignored).
// FrozenEventJSON is required for reason=missing and must be empty otherwise.
// LastResultDigest/LastRecoveryDigest attest the old entry's records exactly
// as in GroupFailureRecord.
type GroupRecoveryRecord struct {
	Witness                              RawRecoveryWitness
	FrozenEventJSON                      string
	LastResultDigest, LastRecoveryDigest string
}

// CommitGroupRecoveryLocked performs D6 for a caller holding
// WithGroupOutboxLock and returns the digest of the written
// recovery_witness value fragment, which T2' records as
// last_recovery_digest. On error nothing was written unless
// IsOutboxCommitUncertain(err).
func CommitGroupRecoveryLocked(path string, rec GroupRecoveryRecord) (string, error) {
	var digest string
	edit := func(file *rawOutboxFile) ([]byte, error) {
		next, err := planOutboxD6(file, outboxD6{
			Witness: rec.Witness, EventJSON: rec.FrozenEventJSON,
			ConsumedResultDigest: rec.LastResultDigest, ConsumedWitnessDigest: rec.LastRecoveryDigest,
		})
		if err != nil {
			return nil, err
		}
		written, err := scanRawOutboxFile(next)
		if err != nil {
			return nil, err
		}
		entry, err := written.entryByQueueID(rec.Witness.NewQueueID)
		if err != nil {
			return nil, err
		}
		if entry.Parsed.RecoveryWitness == nil {
			return nil, fmt.Errorf("D6 entry lacks its recovery_witness")
		}
		digest = OutboxWitnessDigest(entry.Parsed)
		return next, nil
	}
	if rec.Witness.Reason == witnessReasonMissing {
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			raw = []byte("{\n  \"entries\": []\n}\n")
			file, err := scanRawOutboxFile(raw)
			if err != nil {
				return "", err
			}
			next, err := edit(file)
			if err != nil {
				return "", err
			}
			if bytes.Equal(next, raw) {
				return "", nil
			}
			if _, err := scanRawOutboxFile(next); err != nil {
				return "", fmt.Errorf("edited outbox fails strict scan: %w", err)
			}
			if err := writeRawOutboxAtomic(path, next); err != nil {
				return "", err
			}
			return digest, nil
		}
	}
	if err := commitRawOutboxEditLocked(path, edit); err != nil {
		return "", err
	}
	return digest, nil
}

// OutboxEntryDigest is the SHA-256 of the entry's raw object bytes
// (old_entry_digest in an exhausted recovery_witness).
func OutboxEntryDigest(e *RawGroupEntry) string { return sha256Hex(e.Raw) }

// OutboxWitnessDigest is the SHA-256 of the entry's recovery_witness value
// fragment, or "" when the entry carries none.
func OutboxWitnessDigest(e *RawGroupEntry) string {
	if e == nil || e.RecoveryWitness == nil {
		return ""
	}
	return sha256Hex(e.Fields["recovery_witness"])
}

// OutboxEventDigest is the SHA-256 of a frozen event_json string
// (event_json_digest in a recovery_witness).
func OutboxEventDigest(eventJSON string) string { return sha256Hex([]byte(eventJSON)) }
