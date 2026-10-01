# Real macOS artifact round-one result

- Runner worktree: `/tmp/hyphae-artifact-r2b-wt`, HEAD `343520df6470884f37cc962a3105cebbc5905783`, clean.
- Fixed source worktree: `/tmp/hyphae-artifact-source-a4`, HEAD `a4aa606eb81d5c040d94c51cdf94553e646d8674`, clean.
- Toolchain available on host: `go1.26.4 darwin/arm64`.
- Lock: `/Users/jason/Dev/iDoris/Hyphae/build/agent-handoff/20261001/Agent24-production-hyphae.lock.json`; the archived production lock was read directly and not modified.
- CLI artifact SHA256: `f53c29b31d8ca5eb0124ced246bcff6610f048f18bc8dcc2de27f685dad8b221`.
- Relay artifact SHA256: `a012d86e549cbeb564d5a5932c54f9b3511c2434203846537096420c89f36aef`.

## Result

The formal runner completed once with exit code `0`. It accepted the production lock and both expected artifact hashes. All runner stages passed:

- `encrypted-identities`
- `mutual-contacts`
- `relay-configured`
- `empty-history-before-pull`
- `encrypted-a-to-b`
- `encrypted-b-to-a`
- `offline-queued`
- `restart-retry-and-history-dedup`
- `expected-error-codes`
- `complete`

Both directions produced a 64-character event ID, and the receiving inbox/history assertions matched the same event ID and plaintext. Offline send was queued with `published_to=0`, retry published the same event ID, the outbox cleared, and repeated inbox pulls preserved a single history row. The three expected error assertions passed: wrong password -> `auth_error` (exit 3), malformed contact key -> `other_error` (exit 4), and missing message contact -> `user_error` (exit 1). The harness reports assertion stages rather than printing generated event-ID values; the exact IDs are intentionally absent from the raw log.

The runner used its own temporary homes and artifact copies. The external temporary HOME/TMPDIR used to invoke it was removed after completion. Its `finally` cleanup stopped the spawned relay. No runner temp directory remained, and the post-run process check found no relay process. The original `round1-attempt/` failure evidence was left unchanged.

## Evidence files

- Exact command, with no password: `exact-command.txt`, SHA256 `ea17291b21f53675ab30432bf31c5546910afdfb89c23db2610c94918b24eac9`.
- Unmodified runner stdout/stderr: `run.log`, SHA256 `2ce0224d333a543774af7b8b05dbebcfd7a4066c2db6db725762f29ef1327d20`.
- Exit code: `exit-code.txt` contains `0`, SHA256 `9a271f2a916b0b6ee6cecb2426f0b3206ef074578be55d9bc94f6f3fe3ab86aa`.

This verifies the archived macOS artifact against the supplied lock and the round-one CLI/relay scenarios. It does not establish Agent24's formal entrypoint or four-repository acceptance.
