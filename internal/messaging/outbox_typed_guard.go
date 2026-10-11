package messaging

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// ErrOutboxTypedWriteHeld reports that outbox.json carries members the typed
// types.Outbox round trip would drop -- the group protocol records
// fanout_protocol/failure_result/recovery_witness or any other unknown member.
// Typed whole-file rewrites must not erase them (result/witness schema
// supplement §5), so UpdateOutbox and SaveOutbox refuse and leave the file
// byte-identical until each caller is migrated to the raw writer (F2e).
var ErrOutboxTypedWriteHeld = errors.New("outbox holds records a typed rewrite would drop; refusing to modify it")

var typedOutboxEntryMembers = map[string]bool{
	"queue_id": true, "route": true, "id": true, "event_json": true, "recipient_npub": true, "relays": true,
	"retry_count": true, "max_retries": true, "last_attempt": true, "created_at": true, "status": true,
}

// requireTypedOutboxRoundTrip runs under the outbox lock before any typed
// transaction. Member names are compared after JSON decoding, so escaped
// spellings such as "fanout_protocol" are caught too.
func requireTypedOutboxRoundTrip(file string) error {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("failed to parse outbox: %w", err)
	}
	var entries []map[string]json.RawMessage
	for key, value := range doc {
		if key != "entries" {
			return fmt.Errorf("%w: top-level member %q", ErrOutboxTypedWriteHeld, key)
		}
		if err := json.Unmarshal(value, &entries); err != nil {
			return fmt.Errorf("failed to parse outbox: %w", err)
		}
	}
	for i, entry := range entries {
		for key := range entry {
			if !typedOutboxEntryMembers[key] {
				return fmt.Errorf("%w: entry %d member %q", ErrOutboxTypedWriteHeld, i, key)
			}
		}
	}
	return nil
}
