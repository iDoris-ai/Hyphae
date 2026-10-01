# Hyphae COMM round-one artifact-locked test

`scripts/test_comm_round1.py` runs the Hyphae-side encrypted CLI/relay round-one flow against prebuilt artifacts. It is a local integration check, not an Agent24 CLI/UI test and not proof of complete E-M1 acceptance.

The runner fails closed. It requires the production lock JSON, the exact lock recipe, a Hyphae executable, a local relay executable, and the relay's full expected SHA-256. It checks the lock's schema/source/Go version/binary map, chooses the artifact hash from the actual host platform, copies each artifact from an opened regular-file descriptor into a private temporary directory, hashes the copied bytes, then executes only those copies. It does not download or modify artifacts or lock files. The lock records the upstream build recipe; this harness checks supplied artifact hashes and does not claim that the local operator reproduced that build.

Example invocation (supply the actual reviewed lock and artifact paths):

```sh
python3 scripts/test_comm_round1.py \
  --lock /path/to/production-lock.json \
  --expected-recipe 'the exact recipe value in the lock' \
  --cli /path/to/locked-hyphae \
  --relay /path/to/locked-hyphae-relay \
  --expected-relay-sha256 a012d86e549cbeb564d5a5932c54f9b3511c2434203846537096420c89f36aef
```

For Darwin arm64, the relay hash is pinned to the a4 artifact above. Other supported platforms still require an explicit 64-character relay SHA-256 matching the supplied relay file. A missing lock, unsupported host, null platform binary, mismatch, or unavailable local listener is a failure, never a skip.

The run uses separate temporary HOME directories for Alice and Bob and one persistent temporary relay data directory across relay restarts. Passwords are sent only through child stdin. Child environments are allowlisted; CLI/relay stdout and stderr are captured, and failure output is reduced to safe stage labels. The runner exercises encrypted identity creation, mutual contacts, relay config whose `source` is exactly `config`, empty `history inbox` after publish but before the first network pull, both message directions, offline outbox queueing, relay restart/retry with the same event ID, repeated `agent inbox` pulls followed by `history inbox` de-duplication, and baseline error codes (wrong password 3, malformed contact 4, invalid message target 1). It checks full stored plaintext, event IDs, encryption flags, and incoming direction through the CLI JSON response without printing message bodies.

`scripts/test_comm_round1_test.py` contains standard-library unit tests for the unmodified, hash-pinned production lock fixture, verified copies, environment isolation, stdin handling, timeout cleanup, strict envelope parsing, and safe error assertions. To test another lock, set `HYPHAE_COMM_ROUND1_TEST_LOCK=/path/to/hyphae.lock.json`; otherwise the checked-in source fixture is required and verified by SHA-256. Those tests validate the harness only; they do not substitute for running the real locked CLI and relay.
