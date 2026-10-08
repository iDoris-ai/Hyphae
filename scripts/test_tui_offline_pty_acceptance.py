import json
import signal
import subprocess
import sys
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


if __name__ == "__main__":
    unittest.main()
