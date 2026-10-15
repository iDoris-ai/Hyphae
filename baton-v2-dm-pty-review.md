# Baton v2 Wave2: DM PTY harness safety patch

- task_id: v2-wave2-dm-safe-fix-20261015
- attempt_id: c468bf7b-2538-48d0-b7eb-5dfb038e3225
- base_sha: 01294a52ec597eec1c213b97a2ca8de7fc5b9795 (git HEAD verified after checkout)
- branch: baton/grokbot-v2-20261015-dm-safe-fix
- commits: tests `c3bd01d79f8b4870f89b24903dd1d49791804aa1`, fix `dd64e6a2c1bed251e525e15be7043d2c306dc2e1`
- status: candidate, pending independent review

## Defects confirmed on base (mocked unit tests only)

Command: `python3 scripts/test_tui_offline_pty_acceptance.py` on base source + new tests, exit 1
(15 tests, failures=1, errors=8; log sha256 `5e80d5c0536a9db20b476614ab7a7d4aac88bd8906e824e93ceead752f0e5a77`, log kept privately, not published).

1. Redaction: `str(exc)[:240]` published arbitrary exception text; a mocked `TimeoutExpired` leaked argv (path, npub) into failure JSON.
2. Inbox probe: `independent_inbox_probe` only caught RuntimeError/JSON/AttributeError; a `TimeoutExpired` escaped and would replace the safe primary visibility diagnostic.
3. Cleanup: `finally` stopped sender, receiver, relay sequentially; one failing stop skipped the rest and masked the primary failure. `Relay.stop` could raise raw `TimeoutExpired` (argv/paths) after SIGKILL.
4. Fork child: if `execvpe` failed in the `pty.fork` child, the exception unwound into the parent's code path (cleanup, evidence write, temp removal) inside the child.
5. SIGTERM: no handler, so SIGTERM skipped owned cleanup entirely.
6. `TemporaryDirectory` removed private roots even when owned child shutdown was unconfirmed.

## Fix (production file only)

- `HarnessError(RuntimeError)` for runner-authored messages; all 35 runner raises use it. `safe_failure()` publishes only HarnessError text; other exceptions become `unexpected <Type> (details redacted)` / `subprocess timed out (details redacted)`. Plain RuntimeError strings are NOT whitelisted.
- Inbox probe catches any Exception and returns the fixed false/zero dict; receiver diagnostics are wrapped so a diagnostic failure sets `receiver_diagnostics_incomplete` and the primary safe diagnostic is still raised.
- `finish_owned_cleanup()` attempts every owned child stop, counts failures, raises a safe HarnessError only when there was no primary failure (primary never masked). Relay post-SIGKILL timeout becomes a safe HarnessError.
- `private_temp_root()` (mkdtemp 0700) keeps the root when cleanup is incomplete; failure evidence gets `residual` flags (no paths).
- Forked child wraps env/exec in `try/finally: os._exit(127)`.
- SIGTERM handler raises `HarnessTerminated` (one shot) so owned cleanup unwinds; previous handler restored in `finally`.
- Unchanged: opt-in `--execute` gate, hash pins, exact-owned-PID-only signaling (no process-group/global kills), evidence O_EXCL/0600 and HOME refusal.

Diff vs base (numstat, add/del/file):
```
123	0	scripts/test_tui_offline_pty_acceptance.py
136	70	scripts/tui_offline_pty_acceptance.py
```

## Verification

`python3 scripts/test_tui_offline_pty_acceptance.py` exit 0 (15 tests OK; log sha256 `08268afbe1aa85cc830db20686575d3968be077dd5272b5078b3b5ea7c8ce2c3`).
All fork/exec/signal/process calls in new tests are mocked; the test-process environment is protected with `mock.patch.dict` and `os._exit` is mocked. Harness `--execute`, real PTY, relay, product build and binaries were NOT run.
