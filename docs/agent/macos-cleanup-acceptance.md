# macOS owned-process-group cleanup regression

Issue #130 tracks recurring macOS failures in `test_timeout_does_not_leak_descendants_holding_output_pipe`. The contract remains strict: timeout stays a failure, cleanup succeeds only after the entire original owned PGID is gone, and probe/signal errors fail closed. This document describes the deterministic fixture and the 50-run command; local Python results do not establish the CI root cause.

## Fixture lifecycle

`scripts/agent24_joint_cleanup_fixture.py` creates one fresh session/process group with three roles:

1. The `run_child` leader starts a same-PGID supervisor/reaper.
2. The supervisor starts a same-PGID pipe-holder. A dedicated ready pipe confirms that the holder started, is still in the original PGID, and sees the same inherited stdout-pipe identity as the leader.
3. The leader writes a test-only readiness record and exits. The test's Popen instrumentation waits up to one second for both that record and `leader.poll() != None` before it returns the process to `run_child`; only then does the runner's 0.2-second communication deadline begin. The instrumentation also confirms the PGID is still present and records exact helper PIDs/start markers for final leak checks.

On timeout, TERM is sent to the original PGID. `stop_owned_group` waits up to `OWNED_GROUP_TERM_GRACE_SECONDS` (0.5 seconds) for the **whole PGID**, not just the exited leader. The supervisor receives TERM, waits for and reaps its pipe-holder, and then exits. The runner continues probing until the original PGID is confirmed empty; only a still-present group is eligible for SIGKILL. The KILL disappearance bound is 2.5 seconds, and the final pipe-close wait is 0.5 seconds, keeping the regression's existing `<5s` assertion. `force=True` requests escalation after the grace; it never signals an already-empty PGID.

The test's `finally` verifies the original PGID is empty and the recorded supervisor/holder PIDs are gone. If cleanup failed, it may signal only when a recorded helper's start marker and PGID still match the test-owned group, then it must confirm the group disappears; an unverified residual group fails the test. Separate tests cover a real SIGKILL escalation using a TERM-ignoring process, PGID probe failure, leader wait failure, bounded disappearance failure, and signal errors. They do not mock away the leader-exited/descendant-holds-pipe acceptance case.

## Failure evidence and redaction

Each command execution keeps the original `primary_failure` independently from a structured `cleanup_failure`. For example, a timed-out child whose TERM signal fails retains `primary_failure: "child-timeout"` and may record:

```json
{"stage":"term-signal","category":"EPERM","errno":1}
```

Cleanup detail contains only a fixed phase, errno category, and optional integer errno. It never stores exception text, argv values, stdout/stderr content, message body, password, or bearer token. The stable top-level failure remains `child-cleanup-failed` when cleanup cannot be confirmed, so the runner still fails closed.

## Repeated-test command

From the repository root, run the same test method 50 times in one unittest invocation. No binary input or evidence output path is required; test data and helper processes are created in temporary directories. The process exits nonzero if any iteration fails:

```sh
python3 -c 'import unittest; from scripts.test_agent24_joint_test import JointRunnerTests; suite=unittest.TestSuite(JointRunnerTests("test_timeout_does_not_leak_descendants_holding_output_pipe") for _ in range(50)); result=unittest.TextTestRunner(verbosity=1).run(suite); raise SystemExit(not result.wasSuccessful())'
```

CI acceptance must run this command on the macOS runner's Python 3.14.7, report 50/50 tests and exit 0, then run the regular Python discovery suite. The 50-run command's standard output is the unittest summary; it does not write a report file. Do not claim CI success from local runs.

## Local run record

Local implementation run on macOS Python `3.14.6`: the repeated command above completed **50/50** in 20.359 seconds (aggregate, approximately 0.407 seconds per iteration). `python3.14 -m unittest discover -s scripts -p '*_test.py' -v` completed **40 tests, PASS** in 9.912 seconds. These are local results only; they do not establish the CI root cause or substitute for CI. The required follow-up CI check remains a macOS Python 3.14.7 `workflow_dispatch` run; this document does not claim it has run.
