# Kind 30078 wire contract

Hyphae keeps Nostr kind `30078` for both agent profiles and agent messages.
The schema discriminator is carried by tags; changing the kind would break
existing profile replacement coordinates and is not part of this contract.

## Classification

The shared `internal/wireevent` classifier is the authority used by profile
parsing, message inbox validation, daemon receive handling, and the messaging
storage boundary. Consumers still perform their own signature, recipient,
and content checks after classification.

| Event | Required discriminator | `d` handling |
| --- | --- | --- |
| Profile | exactly one `c=profile` and one `d=agent-profile`; no `v` or `p` | fixed profile coordinate |
| Agent message | exactly one `c=agent`, one `v=v1`, and one 32-byte hex `p` | new writes use `agent-message:` plus a unique 16-, 32-, or 64-hex token |

For compatibility, a message may have no `d`, a legacy 16-hex `d`, or a legacy
32-hex `d` (the historical daemon auto-reply form). These forms are accepted
only when the unique `c`, `v`, and `p` tags classify the event as a message.
They do not make an event a message by themselves. The CLI keeps its
established random-plus-content 16-hex uniqueness token; the TUI now uses that
same generator instead of writing an untagged message. The daemon auto-reply
keeps its established random 32-hex token. All three pass through the same
namespace formatter; the auto-reply's randomness and retry/signature
semantics are unchanged. Unknown `c` values,
unknown/conflicting `d` namespaces, malformed discriminator tags, duplicate
`c`/`d`/`v`/`p` tags, profile/message tag combinations, and non-hex or
wrong-length recipient keys fail closed. In particular, profile events are
never decoded or stored as messages, and messages are never decoded as
profiles.

## Queries and compatibility

Message relay filters include kind `30078` and `#c=agent`, `#v=v1`, and
`#p=<recipient>`, but deliberately omit `#d`. This allows relays to return
historical messages that have no `d`; every returned event is still checked by
the local classifier and signature/recipient validation. Profile queries use
`#c=profile` and `#d=agent-profile`, and profile parsing independently checks
both tags.

Existing signed events are read in place. This change does not rewrite,
re-sign, republish, or assign new event IDs to historical profiles or
messages. New CLI, daemon auto-reply, and TUI message writes share one d-tag
namespace formatter, so new message coordinates cannot collide with the fixed
profile coordinate or the legacy forms. Retried outbox events retain their
original signed payload and event ID.

## Outbox concurrency note

`SaveOutbox` is an atomic, locked full-replacement operation, not a safe
read-modify-write primitive for a stale snapshot. Production mutations use
`UpdateOutbox`, which obtains the cross-process lock, reloads the current
snapshot, applies the mutation, and atomically persists it. This change leaves
that implementation intact and adds a process-interleaving regression for a
clear operation confirming an earlier failed snapshot while another process
adds an entry: only the confirmed old entry may be cleared, and the newer
entry must remain valid on disk.
