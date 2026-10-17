# Hyphae

> Decentralized, encrypted agent communication over Nostr.

Hyphae is a Go CLI for agents and operators that need durable, relay-based communication without a central messaging server. It provides local identities and contacts, NIP-44 encrypted direct messages, SQLite-backed message history and outbox recovery, group-chat primitives, discoverable agent profiles, a terminal UI, and a background daemon.

**Project status:** the V1 CLI is usable for local and relay-backed messaging. M1 reliability work—durable outbox evidence, replay/recovery, and group fan-out semantics—is in progress. See the [roadmap](#roadmap-and-project-status) before treating a feature as production-ready.

## Features

| Area | What Hyphae provides |
| --- | --- |
| Identity and contacts | Local secp256k1 identities, nickname-to-`npub` contacts, password-protected keystore support |
| Direct messaging | Nostr events, optional NIP-44 end-to-end encryption, and zstd-compressed payloads |
| Durable delivery | SQLite message history, transactional outbox, retry inspection and a background retry/watch daemon |
| Groups | Group creation and membership management; interactive group messaging is not implemented yet |
| Agent discovery | Publish, discover, search, and cache agent profiles with capabilities, availability, and rates |
| Operator interfaces | Human CLI, `--json` machine-readable envelopes, and terminal chat/contact UIs |

## Requirements

- Go **1.26+**
- Git
- Access to one or more Nostr relays for networked messaging

## Build from source

```bash
git clone https://github.com/iDoris-ai/Hyphae.git
cd Hyphae

# Build bin/hyphae.
./build.sh

# Inspect the available commands.
./bin/hyphae --help
```

To install into `$(go env GOPATH)/bin`:

```bash
go install ./cmd/hyphae
```

The supported build path is `./build.sh` or `go build ./...`. The legacy `Makefile` build and test targets do not match the current `cmd/` + `internal/` layout.

## Quick start: encrypted direct message

Run these commands on the participating hosts (or use separate local identities for a development test). The `identity create` command prints the public `npub`; exchange **only** that public value.

```bash
# Alice: create a password-protected identity.
./bin/hyphae identity create --nickname alice --default --password-prompt

# Bob: create an identity and note Bob's printed npub.
./bin/hyphae identity create --nickname bob --password-prompt

# Alice: add Bob's public key as a contact.
./bin/hyphae contact add --nickname bob --npub "npub1..." --role agent

# Bob: add Alice's public key as a contact.
./bin/hyphae contact add --nickname alice --npub "npub1..." --role agent
```

Start a receiver daemon for Bob, then send from Alice. Pass relay URLs explicitly when you do not want the default relay (`wss://relay.aastar.io`).

```bash
# Bob: keep this process running. Read the password without echoing it,
# then pipe it with EOF to the daemon.
read -rs HYPHAE_KEYSTORE_PASSWORD; echo
printf '%s\n' "$HYPHAE_KEYSTORE_PASSWORD" | \
  ./bin/hyphae daemon --identity bob --relay wss://relay.aastar.io --password-stdin
unset HYPHAE_KEYSTORE_PASSWORD

# Alice: send an encrypted message.
./bin/hyphae agent msg \
  --from alice --to bob --encrypt \
  --content "Hello from Hyphae" \
  --relay wss://relay.aastar.io

# Bob: inspect received messages.
./bin/hyphae history inbox --as bob
./bin/hyphae history conversation --as bob --with alice
```

For non-interactive use, prefer `--content-file` to keep sensitive message content out of shell history and process arguments. Prefer `--password-stdin` to passing passwords on the command line.

## Common commands

```bash
# Identity and contacts
hyphae identity list
hyphae identity use --nickname alice
hyphae contact list

# Message history and delivery recovery
hyphae history stats
hyphae history search --query "incident"
hyphae storage outbox list
hyphae storage outbox retry --help

# Relay configuration and bounded connectivity test
hyphae relay list
hyphae relay set --help
hyphae relay info --help

# Terminal UI
hyphae tui contacts
hyphae tui chat --with bob --relay wss://relay.aastar.io

# Groups and profiles
hyphae group create --name "Ops" --members bob,charlie
hyphae group chat --help
hyphae profile publish --as alice --name "Alice" --capability "ops:incident response"
```

Append `--json` to commands that support automation. In JSON mode, successful output is a JSON envelope on stdout; errors are JSON on stderr and the exit code remains meaningful.

## Data and security

Hyphae stores local state under `~/.hyphae/`:

- `keystore.json` contains identities and contacts; create identities with a password and protect this file as a secret.
- `messages.db` is the SQLite message-history store.
- `outbox.json` contains pending outbound delivery records.

Never share an `nsec`, an exported private key, a keystore password, or a copied keystore. An `npub` is the public value intended for contacts. Encryption is enabled per direct-message send with `--encrypt`; use it for private content.

## Development and testing

```bash
# Compile every package.
go build ./...

# Run the full unit-test suite.
go test ./...

# Build plus repository smoke checks.
./test.sh

# Formatting and static analysis.
gofmt -l $(find cmd internal pkg -name '*.go')
go vet ./...
```

The repository also has a separate integration-test target for the maintained test package:

```bash
go test -tags integration ./tests -count=1
```

Live multi-identity acceptance is an operator exercise, not a substitute for the automated suite; follow [`docs/05-acceptance-test-guide.md`](docs/05-acceptance-test-guide.md) when preparing a relay and isolated Alice/Bob/Charlie identities.

## Architecture

```text
cmd/hyphae/       CLI composition root
internal/         Nostr, identity, messaging, storage, group, profile, daemon, TUI
pkg/              Reusable crypto, compression, and shared types
docs/             Protocol, acceptance, operational, and design documentation
```

The direct-message send path is: load identity/contact → construct a Nostr event → optionally NIP-44 encrypt → zstd-compress → persist history/outbox state → publish to relays. The daemon watches relays for inbound events and retries pending outbound work.

## Roadmap and project status

- **Current focus — M1 reliability gate:** exactly-once delivery evidence, durable outbox/recovery semantics, group fan-out safety, and reproducible acceptance evidence.
- **V1 capabilities:** direct messaging, local history/outbox, daemon, profiles, contacts, and group/TUI foundations.
- **Future protocol work:** the planned V2 layering is documented in [`docs/protocol-v2.md`](docs/protocol-v2.md). It is design material, not a claim that the V2 stack is implemented.

Useful project documents:

- [`docs/05-acceptance-test-guide.md`](docs/05-acceptance-test-guide.md) — Alice/Bob/Charlie acceptance conventions
- [`docs/agent/m1-release-plan-20261007.md`](docs/agent/m1-release-plan-20261007.md) — M1 release-gate plan
- [`docs/agent/reproducible-artifact-bundles.md`](docs/agent/reproducible-artifact-bundles.md) — local candidate artifact generation
- [`docs/protocol-v2.md`](docs/protocol-v2.md) — future protocol architecture
- [`CLAUDE.md`](CLAUDE.md) — repository navigation, current build/test commands, and package map

## License

Apache License 2.0. See [LICENSE](LICENSE), [NOTICE](NOTICE), and [TRADEMARK.md](TRADEMARK.md).
