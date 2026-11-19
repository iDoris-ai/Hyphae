# Agent24 handoff: friend discovery (contract 1.2)

Design: `docs/search/discover-friends.md`. This page is what Agent24 calls.

## Detect it

`hyphae contract --json` → `contract_version` is `1.2` (or `1.1` for v0.31.0) and `provides` contains `nostr-friend`.
1.1 added discovery and the stranger limits; 1.2 adds the commands to handle strangers (below). Both only add; every 1.0 call keeps working.

## Commands (all take `--json`)

| Call | What it does | `data` on success |
|---|---|---|
| `friend visibility --as <nick>` | Show the card setting; publishes nothing | `visibility` (`public`/`hidden`), `name`, `tags`, `about`, `expires_at`, `published` |
| `friend visibility public --as <nick> [--name] [--tags a,b] [--about]` | Publish the card | the above plus `relays` (per relay `url`/`ok`/`error`), `published_to` |
| `friend visibility hidden --as <nick>` | Hide: tombstone + NIP-09 delete to every relay the card reached | same as public |
| `friend tags --as <nick> --set a,b` | Save tags; republish only if the card is live now | same, plus `published: false` when only saved |
| `friend find --as <nick> [--tag t]... [--limit 1..100] [--relay]` | Find public people by tag (any tag matches; default `友好`) | `results[]`: `npub`, `name`, `tags`, `about`, `matched`, `relation` (`friend`/`stranger`), `card_updated_at`; `failed_relays[]` |

### Handling strangers (1.2)

| Call | What it does | `data` on success |
|---|---|---|
| `friend pending --as <nick>` | Strangers who wrote and were not ignored, blocked or added | `pending[]`: `npub`, `first_message`, `received`, `suppressed` (past the 2nd, not shown), `first_seen_at` |
| `friend accept --as <nick> --npub <p> [--nickname n] [--relay]` | Make them a friend: adds a contact and sends a `friend_accept`; their client lifts the limit too | `npub`, `relation: friend`, `nickname`, `relays`, `published_to` |
| `friend ignore --as <nick> --npub <p>` | Clear the request; still a stranger | `npub`, `relation` |
| `friend block --as <nick> --npub <p>` | Drop everything from them and refuse sends to them; removes them from contacts | `npub`, `relation: blocked`, `removed_contacts[]` |
| `friend unblock --as <nick> --npub <p>` | Back to stranger with the old counts | `npub`, `relation` |
| `friend list --as <nick>` | Contacts and every stored peer | `peers[]`: `npub`, `nickname`, `relation`, `sent`, `received`, `can_send` (-1 no limit); `quota`; `card_expires_at` |
| `friend quota --as <nick>` | Daily new-stranger quota | `daily_limit`, `used`, `remaining`, `resets_at`, `per_stranger_limit` |

`--npub` takes an npub or hex key (`invalid_key` otherwise). `accept` needs a stranger who wrote to you (`user_error` otherwise) and is safe to repeat: on a friend it only resends, so retry after a `network_error`. The other side applies the `friend_accept` in its daemon (every watch interval) or on `agent inbox --decrypt`. Per the design, `accept` and `block` should ask the person first (policy `ask` by default).

A block wins over the keystore: re-adding a blocked peer with `contact add` does not let messages through either way, and messages they sent before the block are not listed any more.

`visibility public|hidden` and `tags` sign events, so they need the secret key (`--password-stdin` for an encrypted keystore).

Default is **public**: the daemon publishes a fresh identity's card with the nickname and tag `友好` on start and refreshes it every 7 days. Call `visibility hidden` if the person does not want to be found.

## Stranger rules

- A **friend** is a keystore contact (`contact add`) or someone accepted with `friend accept` (by either side).
- To a stranger, each side may send **2 messages**; a reply does not count as a new stranger.
- At most **10 new strangers per rolling 24 h** per identity.
- `agent msg`, TUI sends and `groupchat create --invitee` refuse with:

| Code | Exit | `data` |
|---|---|---|
| `stranger_message_limit` | 1 | `peer`, `sent`, `limit` |
| `stranger_daily_limit` | 1 | `used`, `limit`, `resets_at` (unix) |
| `peer_blocked` | 1 | `peer` |

- `agent inbox --json` items carry `relation` (`friend`/`stranger`) and `stranger_seq: 1` on a stranger's first message (show it as a request). A stranger's messages past the second, and anything from a blocked peer, are not returned or notified. The daemon never auto-replies to strangers.

## Other codes

- `invalid_tag` (exit 1, `data.reason`): tag empty, too long, or contains spaces, `,`, `#` or invisible characters.
- `network_error` (exit 2): no relay accepted, or a hide missed a relay that holds the card. The daemon retries an unfinished hide hourly.

## Not in this release

TUI buttons for accept / ignore / block (the TUI chat opens contacts only), and relay-side limits.
