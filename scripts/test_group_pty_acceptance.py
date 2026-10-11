"""Unit fixtures for the real M1 group PTY acceptance runner."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import group_pty_acceptance as runner


class GroupPtyFixtureTests(unittest.TestCase):
    def test_screen_tracks_current_visible_text_across_redraw(self):
        screen = runner.Screen(rows=4, cols=48)
        screen.feed(b"\x1b[2J\x1b[HGroup: active\r\nmessage-unique\x1b[0m")
        self.assertIn("Group: active", screen.text())
        self.assertEqual(screen.text().count("message-unique"), 1)

    def test_screen_discards_osc_terminal_queries(self):
        screen = runner.Screen(rows=3, cols=40)
        screen.feed(b"\x1b]11;?\x07\x1b[2J\x1b[HGroup chat")
        self.assertIn("Group chat", screen.text())
        self.assertNotIn("11;?", screen.text())

    def test_status_report_requires_every_check_to_pass(self):
        report = runner.Report()
        self.assertEqual(report.exit_code(), runner.EXIT_NOT_IMPLEMENTED)
        report.set("three_isolated_identities", "PASS")
        self.assertEqual(report.exit_code(), runner.EXIT_NOT_IMPLEMENTED)
        report.set("invite_accept_activate", "FAIL", "fixture rejection")
        self.assertEqual(report.exit_code(), 1)
        self.assertTrue(all(row.startswith(("PASS ", "FAIL ", "NOT_IMPLEMENTED "))
                            for row in report.render()))

    def test_default_command_reports_each_requirement_as_not_implemented(self):
        script = Path(runner.__file__).resolve()
        result = subprocess.run([sys.executable, str(script)], capture_output=True, text=True, check=False)
        self.assertEqual(result.returncode, runner.EXIT_NOT_IMPLEMENTED)
        lines = result.stdout.splitlines()
        self.assertEqual(len(lines), len(runner.CHECKS))
        self.assertTrue(all(line.startswith("NOT_IMPLEMENTED ") for line in lines))

    def test_requirement_matrix_names_every_required_scenario(self):
        labels = dict(runner.CHECKS)
        self.assertEqual(set(labels), {
            "three_isolated_identities", "invite_accept_activate", "three_tui_exactly_once",
            "restart_history", "recipient_retry", "relay_outage_retry",
            "negative_roster_hash", "negative_wrong_invitee", "negative_authority",
            "negative_unknown_sender", "negative_cross_group_replay", "negative_bad_crypto",
            "negative_unknown_version_field", "negative_zero_value",
        })
        for key in ("negative_roster_hash", "negative_wrong_invitee", "negative_authority",
                    "negative_unknown_sender", "negative_cross_group_replay", "negative_bad_crypto",
                    "negative_unknown_version_field", "negative_zero_value"):
            self.assertIn("without history or state changes", labels[key])

    def test_evidence_is_status_only_and_exclusive_create(self):
        report = runner.Report()
        with tempfile.TemporaryDirectory() as temp_name:
            evidence = Path(temp_name) / "evidence.json"
            runner.write_evidence(str(evidence), report)
            payload = json.loads(evidence.read_text())
            self.assertEqual(payload["checks"], report.statuses)
            self.assertNotIn("homes", payload)
            self.assertNotIn("identities", payload)
            self.assertNotIn("reasons", payload)
            self.assertEqual(evidence.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError):
                runner.write_evidence(str(evidence), report)

    def test_three_home_environment_isolated_from_process_home(self):
        with tempfile.TemporaryDirectory() as temp_name:
            root = Path(temp_name)
            cache = root / "cache"
            homes = [root / name for name in ("a", "b", "c")]
            envs = [runner.isolated_env(home, cache) for home in homes]
            self.assertEqual(len({env["HOME"] for env in envs}), 3)
            self.assertTrue(all(env["HOME"] == str(home) for env, home in zip(envs, homes)))
            self.assertTrue(all(env["XDG_CONFIG_HOME"] == str(home / ".config")
                                for env, home in zip(envs, homes)))


if __name__ == "__main__":
    unittest.main()
