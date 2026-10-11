package messaging

import (
	"bytes"
	"fmt"
	"strconv"
)

// outboxO3 is one exact-condition O3 commit (schema supplement §3). Result's
// typed members are serialized; its Raw, Fields and ResultID are ignored and
// the ID is derived from the token. ConsumedResultDigest/ConsumedWitnessDigest
// are the SHA-256 of the value fragments of an existing failure_result /
// recovery_witness that the caller verified as consumed under the F3 lock;
// a present record without a matching digest is refused. This type grants no SQLite authorization.
type outboxO3 struct {
	Result                                      RawFailureResult
	ConsumedResultDigest, ConsumedWitnessDigest string
}

// commitOutboxO3 writes failure_result together with the entry's
// retry_count/status/last_attempt (and fanout_protocol=1) in one atomic
// replace, or writes nothing.
func commitOutboxO3(path string, o3 outboxO3) error {
	return commitRawOutboxEdit(path, func(file *rawOutboxFile) ([]byte, error) {
		return planOutboxO3(file, o3)
	})
}

func planOutboxO3(file *rawOutboxFile, o3 outboxO3) ([]byte, error) {
	encoded := encodeFailureResult(&o3.Result)
	result, err := ParseRawFailureResult(encoded)
	if err != nil {
		return nil, fmt.Errorf("failure_result: %w", err)
	}
	entry, err := file.entryByQueueID(result.Token.QueueID)
	if err != nil {
		return nil, err
	}
	e := entry.Parsed
	sender, err := entrySenderNpub(e)
	if err != nil {
		return nil, err
	}
	key := result.Token.FanoutKey
	if e.Route != OutboxRouteGroup || e.Entry.ID != result.Token.EventID || e.Entry.RecipientNpub != key.RecipientNpub || sender != key.LocalNpub {
		return nil, fmt.Errorf("O3 token does not bind the queued entry")
	}
	maxRetries := int64(e.Entry.MaxRetries)
	if e.Entry.Status != OutboxStatusGroupPending || int64(e.Entry.RetryCount) != result.PriorRetryCount || result.PriorRetryCount >= maxRetries {
		return nil, fmt.Errorf("entry is not an active O3 pre-image")
	}
	wantStatus := OutboxStatusGroupPending
	if result.RetryCount >= maxRetries {
		wantStatus = OutboxStatusGroupFailed
	}
	if result.Status != wantStatus {
		return nil, fmt.Errorf("O3 status %q does not match budget-derived %q", result.Status, wantStatus)
	}
	if err := requireConsumedFragment(entry, "recovery_witness", o3.ConsumedWitnessDigest); err != nil {
		return nil, err
	}
	if err := requireConsumedFragment(entry, "failure_result", o3.ConsumedResultDigest); err != nil {
		return nil, err
	}
	if old := e.FailureResult; old != nil && old.Token.AttemptGeneration >= result.Token.AttemptGeneration {
		return nil, fmt.Errorf("O3 generation %d is not newer than recorded result", result.Token.AttemptGeneration)
	}
	patches := []RawOutboxPatch{
		entry.valuePatch("retry_count", strconv.FormatInt(result.RetryCount, 10)),
		entry.valuePatch("status", `"`+result.Status+`"`),
		entry.valuePatch("last_attempt", strconv.FormatInt(result.LastAttemptAt, 10)),
	}
	var insert bytes.Buffer
	if _, ok := entry.Values["fanout_protocol"]; !ok {
		insert.WriteString(`,"fanout_protocol":1`)
	}
	if _, ok := entry.Values["failure_result"]; ok {
		patches = append(patches, entry.valuePatch("failure_result", string(encoded)))
	} else {
		insert.WriteString(`,"failure_result":`)
		insert.Write(encoded)
	}
	if insert.Len() > 0 {
		// Insert right before the entry's closing brace: the whitespace ahead
		// of it belongs to the last member's fragment, which must stay intact.
		at := entry.Span.End - 1
		patches = append(patches, RawOutboxPatch{Start: at, End: at, Replacement: insert.Bytes()})
	}
	return ApplyRawOutboxPatches(file.Raw, patches)
}

func (f *rawOutboxFile) entryByQueueID(queueID string) (*rawOutboxEntry, error) {
	for i := range f.Entries {
		if f.Entries[i].Parsed.QueueID == queueID {
			return &f.Entries[i], nil
		}
	}
	return nil, fmt.Errorf("queue_missing: no outbox entry has queue_id %q", queueID)
}

// requireConsumedFragment refuses when the member is present and digest does
// not match its exact value fragment. digest is the caller's last consumed
// receipt digest from SQLite, which may outlive the record (a D6 epoch starts
// without one), so an absent member is accepted whatever digest is passed.
func requireConsumedFragment(entry *rawOutboxEntry, member, digest string) error {
	fragment, present := entry.Parsed.Fields[member]
	switch {
	case !present:
		return nil
	case digest == "":
		return fmt.Errorf("%s: existing record is not attested as consumed", member)
	case sha256Hex(fragment) != digest:
		return fmt.Errorf("%s: consumed digest does not match the raw fragment", member)
	}
	return nil
}

func (e *rawOutboxEntry) valuePatch(member, replacement string) RawOutboxPatch {
	span := e.Values[member]
	return RawOutboxPatch{Start: span.Start, End: span.End, Replacement: []byte(replacement)}
}

// encodeFailureResult writes the compact v1 object with a derived result_id;
// strings use the same escaping as the result_id preimage.
func encodeFailureResult(r *RawFailureResult) []byte {
	var token bytes.Buffer
	t := newRawJSONObject(&token)
	t.raw("fanout_key", encodeFanoutKey(r.Token.FanoutKey))
	t.str("event_id", r.Token.EventID)
	t.str("queue_id", r.Token.QueueID)
	t.num("attempt_generation", r.Token.AttemptGeneration)
	t.close()
	var b bytes.Buffer
	o := newRawJSONObject(&b)
	o.num("version", r.Version)
	o.raw("token", token.Bytes())
	o.str("result_id", failureResultID(r.Token))
	o.num("expected_row_revision", r.ExpectedRowRevision)
	o.num("attempts", r.Attempts)
	o.num("last_attempt_at", r.LastAttemptAt)
	o.num("relay_count", r.RelayCount)
	o.num("prior_retry_count", r.PriorRetryCount)
	o.num("retry_count", r.RetryCount)
	o.str("status", r.Status)
	o.str("issue", r.Issue)
	o.close()
	return b.Bytes()
}

func encodeFanoutKey(k OutboxFanoutKey) []byte {
	var b bytes.Buffer
	o := newRawJSONObject(&b)
	o.str("local_npub", k.LocalNpub)
	o.str("recipient_npub", k.RecipientNpub)
	o.str("group_id", k.GroupID)
	o.str("send_key", k.SendKey)
	o.str("envelope_type", k.EnvelopeType)
	o.close()
	return b.Bytes()
}

// rawJSONObject writes one compact JSON object member by member.
type rawJSONObject struct {
	b       *bytes.Buffer
	members int
}

func newRawJSONObject(b *bytes.Buffer) *rawJSONObject {
	b.WriteByte('{')
	return &rawJSONObject{b: b}
}

func (o *rawJSONObject) key(name string) *bytes.Buffer {
	if o.members > 0 {
		o.b.WriteByte(',')
	}
	o.members++
	appendJSONString(o.b, name)
	o.b.WriteByte(':')
	return o.b
}

func (o *rawJSONObject) str(name, value string)    { appendJSONString(o.key(name), value) }
func (o *rawJSONObject) num(name string, v int64)  { o.key(name).WriteString(strconv.FormatInt(v, 10)) }
func (o *rawJSONObject) raw(name string, v []byte) { o.key(name).Write(v) }
func (o *rawJSONObject) close()                    { o.b.WriteByte('}') }
