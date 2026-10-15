# Baton v2: DM PTY harness safety patch, round 2

- task_id: dm-safe-r2-20261015
- attempt_id: 84eace8d-5218-488c-b2b3-5d0287ad9549
- base_sha: baa5b9753c807fb83a601478306716fcf5ec2bbb (verified as Git commit object and as HEAD after checkout)
- branch: baton/grokbot-v2-20261015-dm-safe-r2
- commits: tests `7130bd6d6a8756065d05527aaeca1af254361794`, fix `851cbc5df8e296cb84e90be944c2c50dad4828bc`
- status: candidate, pending independent review. No PR, merge, release or acceptance claim.
- Inherited round-1 commits and metadata stay in ancestry unchanged.

## Blockers addressed

1. Post-fork ownership. `PtyProcess` now takes the caller's `owned` list and appends itself immediately after `pty.fork()` returns a positive PID, before `ioctl`/`SIGWINCH`/`setraw` can raise. `SIGTERM` is blocked with `pthread_sigmask` across fork and registration (restored in `finally`, and restored in the child before exec), so a SIGTERM unwind cannot land between acquisition and registration. All three `start_chat` calls (sender, receiver, sender restart) register into the same list and cleanup iterates `[*owned_tuis, relay]`, so a partially constructed or restarted child is still stopped. `stop()` resets `fd` to -1 so a second stop never closes a reused descriptor. `finish_owned_cleanup` marks `cleanup_incomplete` before the loop and clears it only when every stop is confirmed, so an escape during cleanup retains the 0700 private root. Signals still target only the exact owned PID; no group/global kills; deadlines unchanged.
2. Evidence-write failure. New `emit_summary()` wraps `write_evidence`; any exception sets the fixed flag `evidence_write_failed: true`, keeps the first safe `failure` text and nonzero exit, and never emits the raw path/error or a traceback. On the success path a write failure turns `status` into `fail` with fixed text and exit 1, so it is never reported as product success. The SIGTERM handler is still restored in the outer `finally`; root retention is decided before the summary.

## Regression proofs (mocked only)

New tests: `test_pty_setup_failure_after_fork_stops_exact_owned_child`, `test_pty_setup_failure_unconfirmed_stop_retains_private_root`, `test_evidence_write_failure_keeps_first_safe_failure`, `test_evidence_write_failure_on_success_is_not_reported_as_pass`. They mock `pty.fork` (synthetic PID 424242), `ioctl` failure with secret-shaped text, `os.kill`, `os.waitpid`, `os.close`, `os.execvpe`, `os._exit`, `pthread_sigmask`, relay start/stop and identity helpers, and protect `os.environ` with `mock.patch.dict`. No real fork, PTY, relay, build or signal to another process.

- Base source (baa5b975) + new tests: exit 1, 19 tests, errors=5. The PTY proofs fail because the acquired PID is never signalled (`kill` has no calls) and the root is removed; the evidence proof fails because the raw `PermissionError` escapes; the success-path proof has no safe summary helper; the existing exec-failure test errors only on the new `owned` argument. Log sha256 `f5bfdfab13a5f4be292337ace302acd2a6d20e6203f492e80fee0419c331b0f5` (kept privately).
- Fix: `python3 scripts/test_tui_offline_pty_acceptance.py` exit 0, 19 tests OK. Log sha256 `4d1c8bdc240d65726cff04ef792150b5cc71ab6aaa8c63fe2f0e34dfb400e8c7` (kept privately).

## Diff size

Against current `origin/main` (merge base 01294a52), add/del:
```
171	84	scripts/tui_offline_pty_acceptance.py   (production, 255 lines changed, within 300)
228	0	scripts/test_tui_offline_pty_acceptance.py
```
This round only, against base baa5b975: production 43/22, tests 106/1.

## Not done / residual notes

- Harness `--execute`, real PTY, relay and product build were not run.
- A partially written evidence file may remain if writing fails after creation; it only ever contains the redacted summary.
- A SIGTERM during cleanup can still skip later stops, but the root is then retained with `cleanup_incomplete` set (fail closed).
