# Social protocol conformance fixtures

Language-independent test cases for the Hyphae social protocol (`docs/search/protocol-draft.md`,
in Chinese). The Go packages under `internal/social/` pass them, and a second implementation can
check itself against the same files. This README is the full specification of the format, so you
can write a runner without reading the Go code.

When the spec and the Go code disagree, the fixture follows the spec. The case is then marked
`known_gap` (see [Known gaps](#known-gaps)). When the spec is silent, the fixture pins the Go
behaviour and its `note` starts with `Ambiguity:`.

## Files

Every file is a UTF-8 JSON object:

```json
{ "kind": "<kind>", "description": "<free text>", "cases": [ ... ] }
```

| File name pattern  | `kind`           | What it tests                                         |
|--------------------|------------------|-------------------------------------------------------|
| `parse-*.json`     | `parse`          | Accepting or rejecting one message body               |
| `seq-intent*.json` | `intent_session` | The intent session state machine (direction 1)        |
| `seq-referral*.json` | `referral_flow` | The referral flow state machine (direction 3)        |
| `resolve-*.json`   | `invite_chain`   | Invite-chain resolution (direction 2)                 |

Large files are split into `-1`, `-2`, ... parts. Load every file that matches the pattern.
Within one kind, case `name`s are unique and identify the case. Unknown keys never appear.
A runner should reject unknown keys, so that a typo in a fixture surfaces as an error.

Common optional keys on any case:

- `note`: an explanation for humans. It never affects the result.
- `known_gap`: see [Known gaps](#known-gaps).

## Envelopes

An envelope is the projection of a Nostr event that has already been verified:

```json
{ "id": "<64 hex>", "author": "<64 hex>", "created_at": 1700000000, "content": "<body text>" }
```

- `id` and `author` are given as-is and may be deliberately malformed. **Do not recompute or
  verify the event ID or a signature.** The fixtures use synthetic IDs such as
  `000…0101` and keys such as `aaaa…aaaa`, which are not hashes of anything.
- `created_at` is a JSON integer (Unix seconds, chosen by the sender).
- `content` is a JSON **string**. The message body is the UTF-8 encoding of that string's
  value, byte for byte. The body is deliberately not a nested JSON object. Many cases
  (duplicate keys, escaped keys, `null`, trailing data, deep nesting, oversized bodies, byte
  order marks) cannot survive being parsed and re-serialised. Hand the bytes to your parser
  unchanged.

Instead of `content`, an envelope may carry `content_gen`. Exactly one of the two is present.
This is the **generator form**, used for bodies that would bloat the file:

```json
"content_gen": [ "literal text", { "repeat": "x", "count": 512 }, "more literal text" ]
```

The body is the concatenation of the parts, in order. A part that is a JSON string is
literal text. A part of the form `{"repeat": s, "count": n}` is the string `s` repeated `n`
times (`s` is non-empty, `n >= 0`). Repetition counts the whole string `s`, not bytes.

## Parse fixtures (`kind: "parse"`)

```json
{ "name": "...", "envelope": {...}, "expect": "accept" | "reject",
  "type": "<message type>", "scope": "message" | "shape", "note": "...", "known_gap": "..." }
```

- `expect` is `accept` or `reject`.
- `type` is required when `expect` is `accept`: the message type the parser must report.
  On `reject` cases it is informational: the type the body claims, if any. A runner must not
  need it.
- `scope` is optional. It defaults to `message`.
  - `message`: parse a complete message. **Accept** means that the envelope is valid, the
    body passes every shared rule, the body's `type` is one of the 14 implemented types, and
    the body satisfies that type's schema and field constraints. Anything else is **reject**.
  - `shape`: apply only the envelope checks and the shared body rules below, without a type
    schema. The `type` only has to be a non-empty string. These cases (all in
    `parse-common.json`) test rules such as depth and key charset, which no real message type
    can reach. Example: an accepted body 16 levels deep. Expose this layer as your generic
    strict decoder, or skip the `shape` cases if you cannot.

Implemented types: `intent`, `respond`, `disclose`, `accept`, `decline`, `withdraw` (intent);
`invite`, `invite_accept`, `rating` (trust); `referral_request`, `referral_forward`,
`referral_accept`, `referral_decline`, `receipt` (referral). `attest` appears in the spec but
is not implemented; it is rejected as an unknown type.

### Rules exercised

Envelope:
- `id` and `author` are exactly 64 lowercase hex characters.
- `created_at > 0`. This is an ambiguity: the spec is silent.

Shared body rules (spec section 共同约定):
- The body is at most 16384 bytes (inclusive). This is checked before parsing.
- The body is exactly one JSON object. JSON whitespace may surround it. Anything else is
  rejected: other values, an empty body, trailing data (even a second object), a byte order
  mark, comments, `NaN`, leading zeros or single quotes.
- Nesting is at most 16 levels, and the body object itself is level 1. An object plus 15
  nested arrays is accepted; one more level is rejected.
- Every object key, at any depth and including keys inside `fields`, matches
  `[a-z0-9_]{1,32}` *after* JSON unescaping. So `"v"` is the key `v`, and `"V"`
  (`V`) is invalid.
- Duplicate keys in any one object are rejected, comparing unescaped keys. Keys only need to
  be unique within their own object.
- `null` never appears anywhere, not even as an array element.
- `v` is the JSON integer `1`. `1.0`, `1e0`, `"1"` and other versions are rejected.
- `type` is a non-empty string, and type names are exact (`Accept` is unknown).
- Unknown fields are rejected, and so are fields that every type lacks (e.g. `author`).
- All numbers are integer tokens. A float or exponent token for an integer field is
  rejected, even when its value is a whole number (`2.0`).
- "Characters" means Unicode code points: one emoji is one character, and an escaped
  surrogate pair is one character.
- Every field without a default is required. A missing required field is rejected, and a
  missing `hops_left` is not 0.

Per-type field limits are listed with each case in the files. Each limit has one case at the
limit (accepted) and one just past it (rejected). The constants:

| Field | Limit |
|---|---|
| `category` | 1–32 chars of `a-z0-9-` |
| `summary` | 1–280 code points |
| `area` | 1–64 code points |
| `window` | `{from, to}`, `from < to` |
| `party` | `{min, max}`, `1 <= min <= max <= 20` |
| intent `expires_at` | `created_at < expires_at <= created_at + 2592000` |
| `fields` | 1–16 entries, keys per the key rule, string values of 0–512 code points |
| `disclose.round` | 1–4 |
| `decline.reason` | optional, 0–280 code points (ambiguity: the spec has no bound) |
| `offer`, `amount` | 1–1,000,000 (minimum 1 is an ambiguity) |
| `hops_left` | 0–3, required |
| request `expires_at` | `created_at < expires_at <= created_at + 2592000` (ambiguity: horizon) |
| `rating.score` | 1–5 |
| `rating.match` | exactly two distinct event IDs, in any order |
| `rating.tags` | optional, at most 8, each 1–64 (ambiguity; ASCII-only cases, see below) |
| `invite.invitee` | not the author |
| `rating.subject` | not the author |

### Worked example (parse)

```json
{
  "name": "receipt amount 0",
  "envelope": {
    "id": "0000000000000000000000000000000000000000000000000000000000001105",
    "author": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "created_at": 1700000000,
    "content": "{\"v\":1,\"type\":\"receipt\",\"to\":\"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",\"amount\":0,\"ref\":\"0000000000000000000000000000000000000000000000000000000000000300\"}"
  },
  "expect": "reject",
  "type": "receipt"
}
```

The runner builds the envelope with the body bytes `{"v":1,"type":"receipt",...,"amount":0,...}`
and calls its full-message parser. The parser must fail, because `amount` must be at least 1.

### Worked example (generator form)

```json
{
  "name": "body exactly 16384 bytes",
  "envelope": {
    "id": "0000000000000000000000000000000000000000000000000000000000001006",
    "author": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    "created_at": 1700000000,
    "content_gen": [
      "{\"v\":1,\"type\":\"accept\",\"intent\":\"0000000000000000000000000000000000000000000000000000000000000100\"",
      { "repeat": " ", "count": 16285 },
      "}"
    ]
  },
  "expect": "accept",
  "type": "accept"
}
```

The body is the 98-byte prefix, then 16285 spaces, then `}`: exactly 16384 bytes, so it is
accepted.

## Sequence fixtures (`kind: "intent_session"` / `"referral_flow"`)

```json
{ "name": "...", "note": "...", "known_gap": "...",
  "setup": { ... },
  "steps": [ { "envelope": {...}, "to": "<64 hex>", "now": 1700000010,
               "expect": "ok" | "reject" | "defer", "state_after": "<State>", "note": "..." } ] }
```

Setup:
- `intent_session`: `{"intent": <envelope>, "responder": "<pubkey>", "expect": "ok"|"reject"}`.
  Parse the intent envelope as a message (it is an `intent`). Then open the session between
  the intent's author A and the responder B. The session starts in `Open`. The responder must
  be 64 lowercase hex and must not be A.
- `referral_flow`: `{"request": <envelope>, "first_holder": "<pubkey>", "expect": "ok"|"reject"}`.
  Parse the `referral_request`. Its author is the requester, and `first_holder` is the
  recipient (routing is not in any body). The flow starts in `Requested`. The first holder
  must be 64 lowercase hex and must not be the requester.
- `setup.expect` defaults to `ok`. When it is `reject`, creating the session or flow must
  fail, and `steps` is empty.

The setup event itself counts as already applied. Redelivering it as a step returns `ok`.

Steps are applied in order to one machine. For each step:
1. Parse `envelope` as a message. If parsing fails, the outcome is `reject` and the state is
   unchanged.
2. Apply it with **`now`**, the trusted local time at which the event was first observed.
   Expiry is decided by `now`, never by the sender's `created_at`. `now` is not required to
   increase across steps (cases test that a recorded `Expired` survives a smaller `now`).
3. `to` is present only on `referral_forward` steps. It is the recipient of the forward,
   because the body does not carry it. Treat an absent `to` as the empty string.
4. Compare the outcome with `expect`:
   - `ok`: applied. Redelivering an already-applied event ID also gives `ok`, with no change.
   - `reject`: not applied, for any reason other than being early.
   - `defer`: `created_at > now + 300`. The event is neither applied nor remembered, so the
     same envelope may come back in a later step and be applied.
5. Compare `state_after` with the state **as observed at `now`**. When the stored state is
   not terminal and `now` is past the expiry, this is `Expired`, whether or not an event
   caused it. Intent: any non-terminal state, once `now > expires_at` of the intent. Referral:
   `Requested` or `Forwarded`, once `now > expires_at` of the request; `Accepted` never
   expires.

A `reject` is not remembered. Some rejections are permanent because the event is wrong in
itself (malformed, wrong reference, outsider, dated after `expires_at`); others only mean
"not allowed in the current state", for example a receipt that arrives before the accept it
cites. A relay does not guarantee order, so the same envelope may come back in a later step
and be applied once its prerequisite has arrived. The fixtures use `reject` for both; they do
not yet tell the two apart.

State names (strings, exact case):

| `intent_session` | `referral_flow` |
|---|---|
| `Open` | `Requested` |
| `Responded` | `Forwarded` |
| `Disclosing` | `Accepted` |
| `Matched` (terminal) | `Settled` (terminal) |
| `Declined` (terminal) | `Declined` (terminal) |
| `Withdrawn` (terminal) | `Expired` (terminal) |
| `Expired` (terminal) | |

Ordering decisions the fixtures pin:
- Intent:
  1. A redelivered, already-applied ID returns `ok`.
  2. A terminal state rejects.
  3. `now > expires_at` records `Expired` and rejects.
  4. A wrong intent reference, or `created_at` before the intent, rejects.
  5. An author who is neither A nor B rejects. This check comes before 6 and 7, so an
     outsider is rejected rather than deferred, and cannot expire the session.
  6. `created_at > now + 300` defers.
  7. `created_at > expires_at` rejects that message without expiring the session: only
     the trusted clock expires a session.
  8. Then the handshake rules apply.
- Referral:
  1. A redelivered, already-applied ID returns `ok`.
  2. A terminal state rejects.
  3. `now > expires_at` while not yet `Accepted` records `Expired`.
  4. Deferral.
  5. An event dated before the request itself rejects. Events of different authors are not
     ordered by `created_at`. While not yet `Accepted`, `created_at > expires_at` also
     rejects, without expiring the flow.
  6. Then the forward, reply and receipt rules apply.

### Worked example (sequence)

Abbreviations in this example only: `A`, `B` and `C` stand for 64 copies of that letter
(lowercase), and `R`, `I`, `F` and `K` stand for the event IDs `…0200`, `…0100`, `…119a` and
`…119b`. The files always spell everything out.

```json
{
  "name": "happy path to Settled",
  "setup": {
    "request": { "id": "R", "author": "A", "created_at": 1700000000,
      "content": "{\"v\":1,\"type\":\"referral_request\",\"intent\":\"I\",\"offer\":10,\"hops_left\":3,\"expires_at\":1700003600}" },
    "first_holder": "B"
  },
  "steps": [
    { "envelope": { "id": "F", "author": "B", "created_at": 1700000010,
        "content": "{\"v\":1,\"type\":\"referral_forward\",\"request\":\"R\",\"intent\":\"I\",\"hops_left\":2}" },
      "to": "C", "now": 1700000010, "expect": "ok", "state_after": "Forwarded" },
    { "envelope": { "id": "K", "author": "C", "created_at": 1700000020,
        "content": "{\"v\":1,\"type\":\"referral_accept\",\"forward\":\"F\"}" },
      "now": 1700000020, "expect": "ok", "state_after": "Accepted" },
    { "envelope": { "id": "…119c", "author": "A", "created_at": 1700000030,
        "content": "{\"v\":1,\"type\":\"receipt\",\"to\":\"B\",\"amount\":10,\"ref\":\"K\"}" },
      "now": 1700000030, "expect": "ok", "state_after": "Settled" }
  ]
}
```

B forwards to C (`hops_left` 3 → 2), and C accepts the latest forward. The requester A then
pays the last forwarder, B: `amount` equals `offer`, and `ref` is C's accept. This settles
the flow.

## Invite-chain fixtures (`kind: "invite_chain"`)

```json
{ "name": "...", "note": "...", "known_gap": "...",
  "roots": ["<pubkey>", ...], "quota": 6, "max_depth": 8,
  "events": [<envelope>, ...],
  "expect": { "<pubkey>": {"valid": true, "inviter": "<pubkey or empty>", "depth": 1, "invite": "<event id>"},
              "<pubkey>": {"valid": false} },
  "exhaustive": true,
  "expect_error": true }
```

- `quota` and `max_depth` are optional. When absent, or 0 or less, the defaults are 6 and 8.
- `events` is the set of received envelopes, in no meaningful order. Envelopes that are not a
  valid `invite` or `invite_accept` are ignored, and so are repeated IDs.
- For each pubkey in `expect`:
  - `valid: false` means it is not a member.
  - `valid: true` requires `inviter` and `depth`. A root has `inviter: ""` and `depth: 0`.
  - `invite`, when present, is the ID of the invite event that admitted it.
- `exhaustive: true` means that no pubkey outside `expect` is valid. Membership is only
  possible for roots and for authors of `invite_accept` events, so those are the candidates.
- `expect_error: true` means resolution fails outright (e.g. a malformed root). `expect` is
  then absent.
- The result must not depend on the order of `events` or on duplicates. Runners should also
  resolve the reversed list and the doubled list, and compare.

Resolution rules (spec section 邀请链):
- **Quota:** sort each inviter's well-formed invites by `(created_at, id)`. Only the first
  `quota` count, and each one uses a slot whether or not it is accepted, even if the invitee
  is a root. Self-invites are invalid at parse time and use no slot (an ambiguity).
- **Parent:** look at a non-root identity's own `invite_accept`s that name a known invite
  addressed to it. The one with the lowest `(created_at, id)` wins. If that invite is past its
  inviter's quota, the identity has no parent, with **no fallback** to another accept. A root's
  accepts are ignored. Accepts of unknown invites, malformed invites or other people's invites
  are ignored and do not shadow a real accept.
- **Validity:** a root is valid at depth 0. Any other identity is valid if and only if its
  parent is valid at a depth below `max_depth`; it then sits one level deeper. Cycles never
  reach a root, so they are invalid.

### Worked example (invite chain)

```json
{
  "name": "basic chain",
  "roots": ["0000000000000000000000000000000000000000000000000000000000000001"],
  "events": [
    {"id": "00000000000000000000000000000000000000000000000000000000000011f5", "author": "0000000000000000000000000000000000000000000000000000000000000001", "created_at": 10, "content": "{\"v\":1,\"type\":\"invite\",\"invitee\":\"0000000000000000000000000000000000000000000000000000000000000002\"}"},
    {"id": "00000000000000000000000000000000000000000000000000000000000011f6", "author": "0000000000000000000000000000000000000000000000000000000000000002", "created_at": 10, "content": "{\"v\":1,\"type\":\"invite_accept\",\"invite\":\"00000000000000000000000000000000000000000000000000000000000011f5\"}"},
    {"id": "00000000000000000000000000000000000000000000000000000000000011f7", "author": "0000000000000000000000000000000000000000000000000000000000000002", "created_at": 20, "content": "{\"v\":1,\"type\":\"invite\",\"invitee\":\"0000000000000000000000000000000000000000000000000000000000000003\"}"},
    {"id": "00000000000000000000000000000000000000000000000000000000000011f8", "author": "0000000000000000000000000000000000000000000000000000000000000003", "created_at": 20, "content": "{\"v\":1,\"type\":\"invite_accept\",\"invite\":\"00000000000000000000000000000000000000000000000000000000000011f7\"}"}
  ],
  "expect": {
    "0000000000000000000000000000000000000000000000000000000000000001": {"valid": true, "inviter": "", "depth": 0},
    "0000000000000000000000000000000000000000000000000000000000000002": {"valid": true, "inviter": "0000000000000000000000000000000000000000000000000000000000000001", "depth": 1, "invite": "00000000000000000000000000000000000000000000000000000000000011f5"},
    "0000000000000000000000000000000000000000000000000000000000000003": {"valid": true, "inviter": "0000000000000000000000000000000000000000000000000000000000000002", "depth": 2, "invite": "00000000000000000000000000000000000000000000000000000000000011f7"}
  },
  "exhaustive": true
}
```

Root 1 invites 2 and 2 accepts; 2 invites 3 and 3 accepts. The members are 1 (root), 2 at
depth 1 and 3 at depth 2, and nobody else.

## Known gaps

A case with `known_gap` is one where the spec (as read by these fixtures) and the reference
Go code disagree. The fixture keeps the spec's answer, `known_gap` explains the Go behaviour,
and a runner skips and logs the case while its implementation still disagrees. The Go
runners fail a known-gap case that starts to pass, so the marker gets removed. `known_gap` is
set on the whole case; for sequences, the whole sequence is skipped.

There are currently no known gaps. The seven found while writing the fixtures were settled
in the spec or the code: blank `summary` / `area` and a non-positive `window.from` are
rejected; strings containing U+FFFD (which is what an unpaired surrogate escape decodes to
in some JSON libraries) are rejected; and deferral needs `created_at` strictly more than
300 s ahead of `now`.

## Ambiguities pinned to the Go behaviour

These cases follow Go where the spec is silent. Their notes start with `Ambiguity:`.

- Positive `created_at`.
- `offer` and `amount` of at least 1.
- The 30-day referral horizon.
- The 280-code-point bound on `decline.reason`, with an explicit `""` allowed.
- Tags: at most 8, each 1–64. Go counts **bytes** for tags but code points everywhere else.
  The fixtures only use ASCII tags, so this point is not pinned.
- A second `accept` from the same side is rejected.
- Messages dated before the intent are rejected.
- Referral events dated before the request are rejected.
- A redelivered, previously applied ID returns `ok` even in a terminal state.
- Setup takes an optional `now` (trusted clock when the intent or request was observed,
  default: its `created_at`); a setup event dated more than 300 s ahead of it is deferred.
  Resolve cases take an optional `now` (default: later than every event); events dated more
  than 300 s ahead of it are left out.
- Self-invites use no quota.

## Not covered

- Reputation scoring and the points ledger (`Balances`, `Exposure`, `CanRequest`).
- `attest`, which has no implementation.
- Bodies that are not valid UTF-8 at the byte level. `content` is a JSON string, so the
  fixtures cannot express such bodies.
