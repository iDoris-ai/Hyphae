// Package trust implements the invite chain, ratings and reputation score of
// the social protocol. See docs/search/protocol-draft.md, section 二.
//
// Like the rest of internal/social it is pure logic over already-verified
// envelopes: no relay, no kinds, no signing, no storage.
package trust

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/iDoris-ai/hyphae/internal/social"
)

// Defaults for Config.
const DefaultQuota, DefaultMaxDepth = 6, 8

// Invite is a parsed invite message. Inviter is the envelope author.
type Invite struct {
	ID, Inviter, Invitee string
	CreatedAt            int64
}

// Accept is a parsed invite_accept message. Invitee is the envelope author.
type Accept struct {
	ID, Invitee, Invite string
	CreatedAt           int64
}

// parseBody checks the header, the expected type, and decodes the body.
func parseBody(e social.Envelope, typ string, v any) error {
	h, err := social.ParseHeader(e)
	if err != nil {
		return err
	}
	if h.Type != typ {
		return fmt.Errorf("%s: wrong type %q", typ, h.Type)
	}
	return social.StrictUnmarshal(e.Content, v)
}

// ParseInvite parses and validates an invite body. Self-invites are rejected
// here, so they never reach resolution and never consume quota.
func ParseInvite(e social.Envelope) (Invite, error) {
	var b struct {
		V       int    `json:"v"`
		Type    string `json:"type"`
		Invitee string `json:"invitee"`
	}
	if err := parseBody(e, "invite", &b); err != nil {
		return Invite{}, err
	}
	switch {
	case !social.ValidHex32(b.Invitee):
		return Invite{}, errors.New("invite: invitee must be 64 lowercase hex characters")
	case b.Invitee == e.Author:
		return Invite{}, errors.New("invite: self-invite")
	}
	return Invite{ID: e.ID, Inviter: e.Author, Invitee: b.Invitee, CreatedAt: e.CreatedAt}, nil
}

// ParseAccept parses an invite_accept body; ResolveInvites checks its invite.
func ParseAccept(e social.Envelope) (Accept, error) {
	var b struct {
		V      int    `json:"v"`
		Type   string `json:"type"`
		Invite string `json:"invite"`
	}
	if err := parseBody(e, "invite_accept", &b); err != nil {
		return Accept{}, err
	}
	if !social.ValidHex32(b.Invite) {
		return Accept{}, errors.New("invite_accept: invite must be 64 lowercase hex characters")
	}
	return Accept{ID: e.ID, Invitee: e.Author, Invite: b.Invite, CreatedAt: e.CreatedAt}, nil
}

// Config holds the invite chain parameters. A Quota or MaxDepth of zero or
// less means the default.
type Config struct {
	Roots           []string
	Quota, MaxDepth int
}

// Member describes a valid identity: who admitted it, with which invite
// event, at what depth. Roots have Depth 0 and empty Inviter and Invite.
type Member struct {
	Inviter, Invite string
	Depth           int
}

// Chain is the resolved set of valid identities.
type Chain struct{ members map[string]Member }

// Member returns how pubkey was admitted, and whether it is valid at all.
func (c *Chain) Member(pubkey string) (Member, bool) {
	m, ok := c.members[pubkey]
	return m, ok
}

// Valid reports whether pubkey is a root or was admitted by the chain.
func (c *Chain) Valid(pubkey string) bool { _, ok := c.members[pubkey]; return ok }

// order compares events by created_at, then event ID.
func order(atA int64, idA string, atB int64, idB string) int {
	return cmp.Or(cmp.Compare(atA, atB), strings.Compare(idA, idB))
}

// ResolveInvites computes the valid identities from invite and invite_accept
// envelopes in any order, ignoring other envelopes and repeated (verified)
// event IDs. The result depends only on the set of envelopes; created_at
// only orders one author's own events and never decides causality.
//
//   - Quota: of an inviter's well-formed invites, by (created_at, event ID),
//     only the first Quota count, each using its slot whether or not it is
//     ever accepted, so nobody else can burn or free an inviter's slots.
//   - Parent: only an identity picks its parent. Of its accepts that name a
//     known invite addressed to it, the lowest (created_at, event ID) wins.
//     If that invite is past its inviter's quota the identity has no parent;
//     there is deliberately no fallback to another accept. Roots have none.
//   - Validity: a root is valid at depth 0; any other identity is valid iff
//     its parent is valid below MaxDepth, and sits one deeper. Cycles never
//     reach a root, so their members are invalid.
//
// now is the trusted local time: events dated more than social.MaxClockSkew
// ahead of it are left out until the clock reaches them.
func ResolveInvites(cfg Config, envs []social.Envelope, now int64) (*Chain, error) {
	cfg.Quota = cmp.Or(max(cfg.Quota, 0), DefaultQuota)
	cfg.MaxDepth = cmp.Or(max(cfg.MaxDepth, 0), DefaultMaxDepth)
	c := &Chain{members: map[string]Member{}}
	for _, r := range cfg.Roots {
		if !social.ValidHex32(r) {
			return nil, fmt.Errorf("trust: root %q must be 64 lowercase hex characters", r)
		}
		c.members[r] = Member{}
	}
	invites, chosen := map[string]Invite{}, map[string]Accept{} // chosen: by invitee
	var accepts []Accept
	for _, e := range envs {
		if e.CreatedAt > now+social.MaxClockSkew {
			continue // not yet: counted once the trusted clock reaches it
		}
		if inv, err := ParseInvite(e); err == nil {
			invites[inv.ID] = inv
		} else if a, err := ParseAccept(e); err == nil {
			accepts = append(accepts, a)
		}
	}
	for _, a := range accepts {
		_, root := c.members[a.Invitee]
		old, had := chosen[a.Invitee]
		if inv, ok := invites[a.Invite]; ok && !root && inv.Invitee == a.Invitee && (!had || order(a.CreatedAt, a.ID, old.CreatedAt, old.ID) < 0) {
			chosen[a.Invitee] = a
		}
	}
	// Sorted by inviter then order, an invite counts iff < Quota of its inviter's precede it.
	all := slices.SortedFunc(maps.Values(invites), func(a, b Invite) int {
		return cmp.Or(strings.Compare(a.Inviter, b.Inviter), order(a.CreatedAt, a.ID, b.CreatedAt, b.ID))
	})
	counted, parent := map[string]bool{}, map[string]Invite{} // parent: by invitee
	for i, inv := range all {
		counted[inv.ID] = i < cfg.Quota || all[i-cfg.Quota].Inviter != inv.Inviter
	}
	for who, a := range chosen {
		if counted[a.Invite] {
			parent[who] = invites[a.Invite]
		}
	}
	// Walk up to an identity with no parent (root or invalid) or already seen
	// (decided, or on this walk: a cycle), then decide the walk top down.
	seen := map[string]bool{}
	for who := range parent {
		var path []string
		for cur := who; !seen[cur] && parent[cur].ID != ""; cur = parent[cur].Inviter {
			seen[cur], path = true, append(path, cur)
		}
		for _, x := range slices.Backward(path) {
			inv := parent[x]
			if p, ok := c.members[inv.Inviter]; ok && p.Depth < cfg.MaxDepth {
				c.members[x] = Member{Inviter: inv.Inviter, Invite: inv.ID, Depth: p.Depth + 1}
			}
		}
	}
	return c, nil
}
