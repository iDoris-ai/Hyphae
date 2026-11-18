# Agent24 handoff: friend discovery (contract 1.1)

Design: `docs/search/discover-friends.md`. This page is what Agent24 calls.

## Detect it

`hyphae contract --json` → `contract_version` is `1.1` and `provides` contains `nostr-friend`.
1.1 only adds commands and error codes; every 1.0 call keeps working.

## Commands (all take `--json`)

| Call | What it does | `data` on success |
|---|---|---|
| `friend visibility --as <nick>` | Show the card setting; publishes nothing | `visibility` (`public`/`hidden`), `name`, `tags`, `about`, `expires_at`, `published` |
| `friend visibility public --as <nick> [--name] [--tags a,b] [--about]` | Publish the card | the above plus `relays` (per relay `url`/`ok`/`error`), `published_to` |
| `friend visibility hidden --as <nick>` | Hide: tombstone + NIP-09 delete to every relay the card reached | same as public |
| `friend tags --as <nick> --set a,b` | Save tags; republish only if the card is live now | same, plus `published: false` when only saved |
| `friend find --as <nick> [--tag t]... [--limit 1..100] [--relay]` | Find public people by tag (any tag matches; default `友好`) | `results[]`: `npub`, `name`, `tags`, `about`, `matched`, `relation` (`friend`/`stranger`), `card_updated_at`; `failed_relays[]` |

`visibility public|hidden` and `tags` sign events, so they need the secret key (`--password-stdin` for an encrypted keystore).

Default is **public**: the daemon publishes a fresh identity's card with the nickname and tag `友好` on start and refreshes it every 7 days. Call `visibility hidden` if the person does not want to be found.

## Stranger rules

- A **friend** is a keystore contact (`contact add`). There is no accept command yet; adding the contact is how Agent24 makes someone a friend.
- To a stranger, each side may send **2 messages**; a reply does not count as a new stranger.
- At most **10 new strangers per rolling 24 h** per identity.
- `agent msg` refuses with:

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

Accept/ignore/block/list/quota commands, the TUI toggle, and the stranger limit on TUI sends, auto-reply and group invites. They come in the next release as contract 1.2.
