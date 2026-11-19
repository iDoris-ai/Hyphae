package messaging

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"fiatjaf.com/nostr"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/iDoris-ai/hyphae/internal/social"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/internal/wireevent"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
)

// MaxControlPlaintext is the largest control message accepted; anything
// longer is dropped (docs/search/discover-friends.md, control messages).
const MaxControlPlaintext = 1024

// FriendAcceptType is the only control message type in v1.
const FriendAcceptType = "friend_accept"

type controlBody struct {
	V       int    `json:"v"`
	Type    string `json:"type"`
	Answers string `json:"answers"`
}

// BuildFriendAccept returns the signed friend_accept to peer answering the
// event answers (the peer's message to us). The plaintext is NIP-44
// encrypted, never compressed, and carries only p, c, v and d tags.
func BuildFriendAccept(sk nostr.SecretKey, peer nostr.PubKey, answers string, now nostr.Timestamp) (*nostr.Event, error) {
	if !isEventIDHex(answers) {
		return nil, errors.New("answers must be a 64-hex event id")
	}
	body, err := json.Marshal(controlBody{V: 1, Type: FriendAcceptType, Answers: answers})
	if err != nil {
		return nil, err
	}
	content, err := crypto.EncryptMessage(string(body), sk, peer)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	d, err := NewAgentMessageDTag(content, now)
	if err != nil {
		return nil, err
	}
	ev := &nostr.Event{Kind: AgentKind, CreatedAt: now, PubKey: sk.Public(), Content: content, Tags: nostr.Tags{
		{"p", common.PubKeyToHex(peer)},
		{"c", wireevent.ControlCategory},
		{"v", wireevent.ControlVersion},
		{"d", d},
	}}
	if err := ev.Sign(sk); err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	return ev, nil
}

// ParseFriendAccept checks that ev is a well-formed friend_accept signed by
// its author and addressed to sk, and returns the event ID it answers.
func ParseFriendAccept(ev *nostr.Event, sk nostr.SecretKey) (string, error) {
	if ev == nil || ev.Kind != AgentKind || !ev.VerifySignature() {
		return "", errors.New("not a signed kind 30078 event")
	}
	class, err := wireevent.Classify30078(ev.Tags)
	if err != nil {
		return "", err
	}
	if class != wireevent.ClassControl {
		return "", errors.New("not a control event")
	}
	if p := ev.Tags.Find("p"); p[1] != common.PubKeyToHex(sk.Public()) {
		return "", errors.New("control event is for someone else")
	}
	plain, err := crypto.DecryptMessage(ev.Content, sk, ev.PubKey)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	if len(plain) > MaxControlPlaintext {
		return "", errors.New("control message too long")
	}
	var b controlBody
	if err := social.StrictUnmarshal(json.RawMessage(plain), &b); err != nil {
		return "", err
	}
	if b.V != 1 || b.Type != FriendAcceptType || !isEventIDHex(b.Answers) {
		return "", fmt.Errorf("unsupported control message v=%d type=%q", b.V, b.Type)
	}
	return b.Answers, nil
}

// ApplyFriendAccept makes the sender of ev a friend of the identity owning
// sk, if ev answers a message that identity sent to the sender after the last
// unblock and the sender is a stranger (not blocked). It reports whether the
// relation changed to friend. Anything else is ignored with an error.
func ApplyFriendAccept(ev *nostr.Event, sk nostr.SecretKey) (bool, error) {
	answers, err := ParseFriendAccept(ev, sk)
	if err != nil {
		return false, err
	}
	if err := InitStorage(); err != nil {
		return false, err
	}
	me, peer := common.EncodeNpub(sk.Public()), common.EncodeNpub(ev.PubKey)
	sentAt, found, err := storage.SentAt(storage.DB, me, peer, answers)
	if err != nil {
		return false, err
	}
	changed := false
	_, err = storage.NewRelationStore(storage.DB).Update(me, peer, time.Now().Unix(),
		func(p storage.PeerRelation) (storage.PeerRelation, error) {
			was := p.Relation.State
			r, err := contact.AcceptedByPeer(p.Relation, found, sentAt)
			if err != nil {
				return p, err
			}
			changed = was != contact.Friend && r.State == contact.Friend
			p.Relation = r
			return p, nil
		})
	return changed, err
}

func isEventIDHex(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

// BuildControlFilter selects control messages addressed to recipientHex.
func BuildControlFilter(recipientHex string, limit int) nostr.Filter {
	return nostr.Filter{Kinds: []nostr.Kind{AgentKind}, Limit: limit, Tags: nostr.TagMap{
		"c": {wireevent.ControlCategory}, "v": {wireevent.ControlVersion}, "p": {recipientHex}}}
}

// SyncControl fetches the latest control messages for sk from every relay and
// applies each friend_accept. Applying is idempotent and invalid or stale
// accepts are refused, so re-reading the same events on every scan is safe.
// It returns the npubs that became friends and the number of events dropped.
func SyncControl(ctx context.Context, relays []string, sk nostr.SecretKey, timeout time.Duration) ([]string, int) {
	filter := BuildControlFilter(common.PubKeyToHex(sk.Public()), 100)
	seen := map[nostr.ID]bool{}
	var friends []string
	dropped := 0
	for _, url := range relays {
		page, err := relayquery.FetchWithTimeout(ctx, url, filter, timeout)
		if err != nil {
			continue
		}
		for i := range page.Events {
			ev := &page.Events[i]
			if seen[ev.ID] {
				continue
			}
			seen[ev.ID] = true
			changed, err := ApplyFriendAccept(ev, sk)
			switch {
			case err != nil:
				dropped++
			case changed:
				friends = append(friends, common.EncodeNpub(ev.PubKey))
			}
		}
	}
	return friends, dropped
}
