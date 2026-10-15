import argparse
import contextlib
import io
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import tui_offline_pty_acceptance as runner


class AcceptanceRunnerTests(unittest.TestCase):
    def test_screen_keeps_visible_text_not_raw_redraw_count(self):
        screen = runner.Screen(rows=4, cols=40)
        screen.feed(b"\x1b[2J\x1b[HInbox: queued\r\nMessage once\x1b[0m")
        self.assertIn("Inbox: queued", screen.text())
        self.assertIn("Message once", screen.text())
        self.assertEqual(screen.text().count("Message once"), 1)

    def test_outbox_cues_distinguish_queued_from_relay_ack(self):
        queued = "Outbox: queued for retry • 123456789abc (not delivered; awaiting relay ACK)"
        accepted = "Outbox: relay accepted • 123456789abc (recipient delivery/read not confirmed)"
        self.assertIsNotNone(runner.QUEUED_RE.search(queued))
        self.assertIsNone(runner.ACCEPTED_RE.search(queued))
        self.assertIsNotNone(runner.ACCEPTED_RE.search(accepted))
        self.assertIsNone(runner.QUEUED_RE.search(accepted))

    def test_screen_ignores_terminal_osc_queries(self):
        screen = runner.Screen(rows=3, cols=40)
        screen.feed(b"\x1b]11;?\x07\x1b[2J\x1b[HChat open")
        self.assertIn("Chat open", screen.text())
        self.assertNotIn("11;?", screen.text())

    def test_owned_tui_child_surviving_sigkill_fails_closed(self):
        process = object.__new__(runner.PtyProcess)
        process.pid = 4242
        process.fd = -1
        with mock.patch.object(process, "wait", side_effect=[False, False]), \
             mock.patch.object(runner.os, "kill") as kill, \
             mock.patch.object(runner.os, "close") as close:
            with self.assertRaisesRegex(RuntimeError, "owned TUI child did not stop after SIGKILL"):
                process.stop()
        self.assertEqual(kill.call_args_list, [
            mock.call(4242, signal.SIGTERM),
            mock.call(4242, signal.SIGKILL),
        ])
        close.assert_not_called()

    def test_gap_is_opt_in_and_never_passes_by_default(self):
        script = Path(runner.__file__).resolve()
        result = subprocess.run([sys.executable, str(script)], capture_output=True, text=True, check=False)
        self.assertEqual(result.returncode, runner.EXIT_GAP)
        self.assertEqual(json.loads(result.stdout)["status"], "expected_gap")

    def test_group_skeleton_is_an_expected_gap(self):
        script = Path(runner.__file__).resolve()
        result = subprocess.run([sys.executable, str(script), "--group-skeleton"],
                                capture_output=True, text=True, check=False)
        self.assertEqual(result.returncode, runner.EXIT_GAP)
        payload = json.loads(result.stdout)
        self.assertEqual(payload["status"], "expected_gap")
        self.assertGreaterEqual(len(payload["checks"]), 6)

    # --- DM harness safety invariants (all process/signal/fork/exec mocked) ---
    SECRET_CMD = ["/private/secret-root/bin/hyphae", "agent", "inbox", "npub1secretvalue"]

    def _args(self):
        return argparse.Namespace(source_commit=None, bin_dir=None, expected_cli_sha256=None,
                                  expected_relay_sha256=None, evidence=None)

    def _run_quiet(self):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = runner.run_offline_acceptance(self._args())
        return code, out.getvalue()

    def test_arbitrary_exception_content_is_redacted(self):
        boom = subprocess.TimeoutExpired(self.SECRET_CMD, 20, output=b"event-content-secret")
        with mock.patch.object(runner, "isolated_env", side_effect=boom):
            code, output = self._run_quiet()
        self.assertEqual(code, 1)
        payload = json.loads(output)
        self.assertEqual(payload["status"], "fail")
        for leaked in ("private", "secret", "npub1", "event-content"):
            self.assertNotIn(leaked, output)
        with mock.patch.object(runner, "isolated_env", side_effect=RuntimeError("token=abc /private/x")):
            _, output = self._run_quiet()
        self.assertNotIn("token=abc", output)
        self.assertNotIn("/private/x", output)

    def test_harness_authored_failures_remain_readable(self):
        self.assertEqual(runner.safe_failure(runner.HarnessError("local relay did not start listening")),
                         "local relay did not start listening")
        self.assertNotIn("secret", runner.safe_failure(OSError(2, "missing", "/private/secret")))

    def test_inbox_probe_failure_never_replaces_primary_diagnostics(self):
        with mock.patch.object(runner, "run_cli", side_effect=subprocess.TimeoutExpired(self.SECRET_CMD, 20)):
            result = runner.independent_inbox_probe(Path("/x"), Path("/h"), Path("/c"),
                                                    "ws://127.0.0.1:1", "e" * 64, "body")
        self.assertFalse(result["command_succeeded"])
        self.assertNotIn("secret", json.dumps(result))

    def test_relay_stop_timeout_after_kill_is_safe_error(self):
        relay = runner.Relay(Path("/private/relay"), Path("/private/data"), 1, {})
        relay.proc = mock.Mock()
        relay.proc.poll.return_value = None
        relay.proc.wait.side_effect = [subprocess.TimeoutExpired(self.SECRET_CMD, 5),
                                       subprocess.TimeoutExpired(self.SECRET_CMD, 2)]
        with self.assertRaises(runner.HarnessError) as ctx:
            relay.stop()
        self.assertNotIn("private", str(ctx.exception))
        relay.proc.kill.assert_called_once_with()

    def test_cleanup_attempts_every_owned_child_even_when_one_fails(self):
        first, second, third = mock.Mock(), mock.Mock(), mock.Mock()
        first.stop.side_effect = RuntimeError("secret /private/path")
        state = {}
        runner.finish_owned_cleanup([first, None, second, third], state, primary_ok=False)
        first.stop.assert_called_once_with()
        second.stop.assert_called_once_with()
        third.stop.assert_called_once_with()
        self.assertTrue(state["cleanup_incomplete"])
        self.assertEqual(state["failed_stop_count"], 1)

    def test_cleanup_error_never_masks_primary_but_fails_closed_alone(self):
        bad = mock.Mock()
        bad.stop.side_effect = RuntimeError("secret /private/path")
        runner.finish_owned_cleanup([bad], {}, primary_ok=False)  # primary propagates instead
        with self.assertRaises(runner.HarnessError) as ctx:
            runner.finish_owned_cleanup([bad], {}, primary_ok=True)
        self.assertIn("owned child cleanup incomplete", str(ctx.exception))
        self.assertNotIn("secret", str(ctx.exception))

    def test_private_root_retained_while_owned_shutdown_unconfirmed(self):
        state = {}
        with runner.private_temp_root(state) as name:
            kept = Path(name)
            state["cleanup_incomplete"] = True
        self.assertTrue(kept.exists())
        shutil.rmtree(kept)
        with runner.private_temp_root({}) as name:
            removed = Path(name)
            self.assertEqual(os.stat(removed).st_mode & 0o777, 0o700)
        self.assertFalse(removed.exists())

    def test_exec_failure_in_forked_child_exits_127_without_unwinding(self):
        class ChildExit(BaseException):
            pass
        with mock.patch.dict(runner.os.environ, {"TEST_SENTINEL": "1"}), \
             mock.patch.object(runner.pty, "fork", return_value=(0, -1)), \
             mock.patch.object(runner.os, "execvpe", side_effect=FileNotFoundError(2, "no", "/private/x")), \
             mock.patch.object(runner.os, "_exit", side_effect=ChildExit) as exit_, \
             mock.patch.object(runner.fcntl, "ioctl") as ioctl, \
             mock.patch.object(runner.os, "kill") as kill:
            with self.assertRaises(ChildExit):
                runner.PtyProcess(["/nonexistent"], {"HOME": "/nonexistent-home"}, [])
        self.assertEqual(os.environ.get("TEST_SENTINEL"), None)
        exit_.assert_called_once_with(127)
        ioctl.assert_not_called()
        kill.assert_not_called()

    def test_sigterm_unwinds_owned_cleanup_and_restores_handler(self):
        installed = {}
        previous = object()

        def fake_signal(signum, handler):
            old = installed.get(signum, previous)
            installed[signum] = handler
            return old

        def deliver_sigterm(*_args, **_kwargs):
            installed[signal.SIGTERM](signal.SIGTERM, None)

        with mock.patch.object(runner.signal, "signal", side_effect=fake_signal) as sig, \
             mock.patch.object(runner, "isolated_env", side_effect=deliver_sigterm):
            code, output = self._run_quiet()
        self.assertEqual(code, 1)
        self.assertIn("SIGTERM", json.loads(output)["failure"])
        self.assertIs(installed[signal.SIGTERM], previous)
        self.assertGreaterEqual(sig.call_count, 2)

    # --- regression proofs for review blockers (fork/exec/kill/waitpid all mocked) ---
    def _fake_bins(self):
        bin_dir = tempfile.mkdtemp(prefix="pty-test-bins-")
        self.addCleanup(shutil.rmtree, bin_dir, True)
        for name in ("hyphae", "hyphae-relay"):
            Path(bin_dir, name).write_bytes(b"")
        args = self._args()
        args.bin_dir = bin_dir
        return args

    def _run_setup_failure(self, stop_confirmed):
        roots = []
        real_mkdtemp = tempfile.mkdtemp

        def record_root(*a, **k):
            roots.append(real_mkdtemp(*a, **k))
            return roots[-1]

        args = self._fake_bins()
        out = io.StringIO()
        err = io.StringIO()
        relay_stop = mock.Mock()
        with contextlib.ExitStack() as stack:
            enter = stack.enter_context
            enter(mock.patch.dict(runner.os.environ, {}))
            enter(mock.patch.object(runner.tempfile, "mkdtemp", side_effect=record_root))
            for name, value in (("create_identity", "npub1fake"), ("set_contact", None),
                                ("decode_npub", "a" * 64), ("contact_matches", True), ("free_port", 1)):
                enter(mock.patch.object(runner, name, return_value=value))
            enter(mock.patch.object(runner.Relay, "start"))
            enter(mock.patch.object(runner.Relay, "stop", relay_stop))
            enter(mock.patch.object(runner.signal, "pthread_sigmask", return_value=set(), create=True))
            enter(mock.patch.object(runner.pty, "fork", return_value=(424242, 97)))
            enter(mock.patch.object(runner.os, "execvpe", side_effect=AssertionError("no exec")))
            enter(mock.patch.object(runner.os, "_exit", side_effect=AssertionError("no _exit")))
            enter(mock.patch.object(runner.fcntl, "ioctl",
                                    side_effect=OSError(5, "ioctl npub1secret", "/private/secret-tty")))
            kill = enter(mock.patch.object(runner.os, "kill"))
            enter(mock.patch.object(runner.os, "waitpid", return_value=(424242, 0)))
            enter(mock.patch.object(runner.os, "close"))
            if not stop_confirmed:
                enter(mock.patch.object(runner.PtyProcess, "wait", return_value=False))
            enter(contextlib.redirect_stdout(out))
            enter(contextlib.redirect_stderr(err))
            code = runner.run_offline_acceptance(args)
        for root in roots:
            self.addCleanup(shutil.rmtree, root, True)
        self.assertEqual(code, 1)
        payload = json.loads(out.getvalue())
        self.assertEqual(payload["status"], "fail")
        for leaked in ("/private", "secret", "npub1", "Traceback"):
            self.assertNotIn(leaked, out.getvalue() + err.getvalue())
        self.assertEqual(kill.call_args_list[0], mock.call(424242, signal.SIGTERM))
        self.assertTrue(all(c.args[0] == 424242 for c in kill.call_args_list))
        relay_stop.assert_called_once_with()
        self.assertEqual(len(roots), 1)
        return payload, Path(roots[0]), kill

    def test_pty_setup_failure_after_fork_stops_exact_owned_child(self):
        payload, root, _ = self._run_setup_failure(stop_confirmed=True)
        self.assertNotIn("residual", payload)
        self.assertFalse(root.exists())

    def test_pty_setup_failure_unconfirmed_stop_retains_private_root(self):
        payload, root, kill = self._run_setup_failure(stop_confirmed=False)
        self.assertEqual(kill.call_args_list, [mock.call(424242, signal.SIGTERM),
                                               mock.call(424242, signal.SIGKILL)])
        self.assertTrue(payload["residual"]["private_temp_root_retained"])
        self.assertTrue(root.exists())
        self.assertEqual(os.stat(root).st_mode & 0o777, 0o700)

    def test_evidence_write_failure_keeps_first_safe_failure(self):
        before = signal.getsignal(signal.SIGTERM)
        for boom in (OSError(13, "denied npub1secret", "/private/secret/evidence.json"),
                     RuntimeError("token=abc /private/x event-content-secret")):
            out, err = io.StringIO(), io.StringIO()
            args = self._args()
            args.evidence = "/nonexistent-dir/evidence.json"
            with mock.patch.object(runner, "isolated_env",
                                   side_effect=runner.HarnessError("local relay did not start listening")), \
                 mock.patch.object(runner, "write_evidence", side_effect=boom), \
                 contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
                code = runner.run_offline_acceptance(args)
            self.assertEqual(code, 1)
            payload = json.loads(out.getvalue())
            self.assertEqual(payload["failure"], "local relay did not start listening")
            self.assertTrue(payload["evidence_write_failed"])
            for leaked in ("private", "secret", "npub1", "token", "Traceback"):
                self.assertNotIn(leaked, out.getvalue() + err.getvalue())
            self.assertIs(signal.getsignal(signal.SIGTERM), before)

    def test_evidence_write_failure_on_success_is_not_reported_as_pass(self):
        out = io.StringIO()
        args = self._args()
        args.evidence = "/nonexistent-dir/evidence.json"
        with mock.patch.object(runner, "write_evidence", side_effect=OSError(28, "full", "/private/x")), \
             contextlib.redirect_stdout(out):
            code = runner.emit_summary(args, {"status": "pass"}, 0)
        self.assertEqual(code, 1)
        payload = json.loads(out.getvalue())
        self.assertNotEqual(payload["status"], "pass")
        self.assertTrue(payload["evidence_write_failed"])
        self.assertNotIn("private", out.getvalue())


if __name__ == "__main__":
    unittest.main()
