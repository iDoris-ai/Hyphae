package messaging

import "os"

// ReadRawOutboxEvidenceSnapshot captures the outbox file at path while holding
// the `withOutboxLock` sibling lock — the same critical section a writer uses
// for its atomic replace — and then parses those bytes with
// ParseRawOutboxEvidence.
//
// Raw holds the bytes as read. When `withOutboxLock` reports an unsupported
// platform, that error is returned and no read or parse happens.
func ReadRawOutboxEvidenceSnapshot(path string) (*RawOutboxEvidence, error) {
	var raw []byte
	if err := withOutboxLock(path, func() error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		raw = data
		return nil
	}); err != nil {
		return nil, err
	}
	return ParseRawOutboxEvidence(raw)
}
