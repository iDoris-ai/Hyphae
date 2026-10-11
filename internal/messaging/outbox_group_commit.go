package messaging

import (
	"errors"
	"fmt"
)

// WithGroupOutboxLock holds the outbox sibling lock for one F3 cross-store
// operation. rev7 §3.3 lock order: this lock first, then SQLite
// BEGIN IMMEDIATE; never wait for this lock while holding SQLite.
func WithGroupOutboxLock(fn func(path string) error) error {
	path, err := GetOutboxPath()
	if err != nil {
		return err
	}
	return withOutboxLock(path, func() error { return fn(path) })
}

// GroupFailureRecord is the O3 input that the F3 caller derives from the
// locked SQLite row. LastResultDigest/LastRecoveryDigest are the digests of
// the row's last consumed failure receipt and recovery witness ("" if none);
// FrozenEventJSON and MaxRetries must equal the JSON entry's values.
type GroupFailureRecord struct {
	Result                               RawFailureResult
	LastResultDigest, LastRecoveryDigest string
	FrozenEventJSON                      string
	MaxRetries                           int64
}

// CommitGroupFailureLocked performs O3 for a caller holding
// WithGroupOutboxLock and returns the T4 receipt for the written record:
// "<result_id>:<sha256 of the failure_result value fragment>". On error
// nothing was written unless IsOutboxCommitUncertain(err) reports that the
// replacement may already be visible.
func CommitGroupFailureLocked(path string, rec GroupFailureRecord) (string, error) {
	var receipt string
	err := commitRawOutboxEditLocked(path, func(file *rawOutboxFile) ([]byte, error) {
		current, err := file.entryByQueueID(rec.Result.Token.QueueID)
		if err != nil {
			return nil, err
		}
		if current.Parsed.Entry.EventJSON != rec.FrozenEventJSON || int64(current.Parsed.Entry.MaxRetries) != rec.MaxRetries {
			return nil, fmt.Errorf("outbox entry does not carry the frozen event and max_retries")
		}
		next, err := planOutboxO3(file, outboxO3{Result: rec.Result, ConsumedResultDigest: rec.LastResultDigest, ConsumedWitnessDigest: rec.LastRecoveryDigest})
		if err != nil {
			return nil, err
		}
		written, err := scanRawOutboxFile(next)
		if err != nil {
			return nil, err
		}
		entry, err := written.entryByQueueID(rec.Result.Token.QueueID)
		if err != nil {
			return nil, err
		}
		receipt = FailureReceipt(entry.Parsed.FailureResult.ResultID, sha256Hex(entry.Parsed.Fields["failure_result"]))
		return next, nil
	})
	if err != nil {
		return "", err
	}
	return receipt, nil
}

// FailureReceipt formats the SQLite last_failure_receipt value. result_id is
// derived from the complete AttemptToken, so the pair names the exact token
// and the exact raw fragment that T4 consumed.
func FailureReceipt(resultID, fragmentDigest string) string {
	return resultID + ":" + fragmentDigest
}

// IsOutboxCommitUncertain reports a write that failed after its rename, so the
// new outbox may or may not be durable; callers must not retry the write.
func IsOutboxCommitUncertain(err error) bool {
	var uncertain *outboxCommitUncertainError
	return errors.As(err, &uncertain)
}
