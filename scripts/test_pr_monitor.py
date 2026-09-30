import json
import sqlite3
import tempfile
import unittest
from contextlib import closing
from pathlib import Path
from unittest.mock import patch

import pr_monitor


class MonitorTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.state = self.root / "state"
        self.db = self.root / "queue.sqlite"
        self.prompt = self.root / "prompt.txt"
        self.prompt.write_text("Review open PRs and report findings.", encoding="utf-8")
        with closing(sqlite3.connect(self.db)) as conn:
            conn.execute("CREATE TABLE queued_items (thread_id TEXT)")
            conn.commit()
        self.config = {
            "repo_slug": "example/hyphae", "repo_dir": str(self.root), "thread_id": "thread-123",
            "codex_bin": "/usr/bin/codex", "gh_bin": "/usr/bin/gh", "queue_db": str(self.db),
            "state_dir": str(self.state), "prompt_file": str(self.prompt),
        }
        self.calls = []

    def tearDown(self):
        self.temp.cleanup()

    def fake_run(self, args, **kwargs):
        self.calls.append((args, kwargs))
        if args[1:3] == ["pr", "list"]:
            return pr_monitor.subprocess.CompletedProcess(args, 0, '[{"number": 1}]', "")
        if args[1:3] == ["run", "list"]:
            return pr_monitor.subprocess.CompletedProcess(args, 0, '[{"status": "completed"}]', "")
        return pr_monitor.subprocess.CompletedProcess(args, 0, "queued", "")

    def assert_gh_repo_binding(self):
        gh_calls = self.calls[:2]
        self.assertIn("--repo", gh_calls[0][0])
        self.assertEqual(gh_calls[0][0][gh_calls[0][0].index("--repo") + 1], "example/hyphae")
        self.assertIn("--repo", gh_calls[1][0])
        self.assertEqual(gh_calls[1][0][gh_calls[1][0].index("--repo") + 1], "example/hyphae")
        for _, kwargs in gh_calls:
            self.assertFalse(kwargs["shell"])
            self.assertEqual(kwargs["cwd"], str(self.root))

    def insert_queued(self):
        with closing(sqlite3.connect(self.db)) as conn:
            conn.execute("INSERT INTO queued_items VALUES (?)", ("thread-123",))
            conn.commit()

    @patch("pr_monitor.subprocess.run")
    def test_existing_queue_prevents_duplicate(self, run):
        run.side_effect = self.fake_run
        self.insert_queued()
        self.assertEqual(pr_monitor.execute(self.config), 0)
        status = json.loads((self.state / "status.json").read_text())
        self.assertEqual(status["action"], "already_queued")
        self.assertEqual(status["queued_count"], 1)
        self.assertEqual(len(self.calls), 2)
        self.assert_gh_repo_binding()

    @patch("pr_monitor.subprocess.run")
    def test_scan_only_never_queues(self, run):
        run.side_effect = self.fake_run
        self.assertEqual(pr_monitor.execute(self.config, scan_only=True), 0)
        self.assertEqual(json.loads((self.state / "status.json").read_text())["action"], "scan_only")
        self.assertEqual(len(self.calls), 2)
        self.assert_gh_repo_binding()

    @patch("pr_monitor.subprocess.run")
    def test_missing_database_fails_closed(self, run):
        run.side_effect = self.fake_run
        self.config["queue_db"] = str(self.root / "missing.sqlite")
        self.assertEqual(pr_monitor.execute(self.config), 1)
        status = json.loads((self.state / "status.json").read_text())
        self.assertEqual(status["action"], "error")
        self.assertIn("error", status)
        self.assertEqual(len(self.calls), 2)

    @patch("pr_monitor.subprocess.run")
    def test_queue_failure_is_not_reported_as_queued(self, run):
        def failing(args, **kwargs):
            self.calls.append((args, kwargs))
            if args[1:3] == ["pr", "list"]:
                return pr_monitor.subprocess.CompletedProcess(args, 0, "[]", "")
            if args[1:3] == ["run", "list"]:
                return pr_monitor.subprocess.CompletedProcess(args, 0, "[]", "")
            return pr_monitor.subprocess.CompletedProcess(args, 2, "", "queue unavailable")
        run.side_effect = failing
        self.assertEqual(pr_monitor.execute(self.config), 1)
        status = json.loads((self.state / "status.json").read_text())
        self.assertEqual(status["action"], "error")
        self.assertIn("queue failed", status["error"])

    @patch("pr_monitor.subprocess.run")
    def test_normal_queue_uses_argument_array_and_snapshot_context(self, run):
        run.side_effect = self.fake_run
        self.assertEqual(pr_monitor.execute(self.config), 0)
        args, kwargs = self.calls[-1]
        self.assertEqual(args[:4], ["/usr/bin/codex", "queue", "--thread", "thread-123"])
        self.assertIn("--message", args)
        message = args[args.index("--message") + 1]
        self.assertIn("Review open PRs", message)
        self.assertIn(str(self.state / "latest.json"), message)
        self.assertFalse(kwargs["shell"])
        self.assertEqual(kwargs["cwd"], str(self.root))
        self.assertEqual(kwargs["timeout"], 45)
        self.assertEqual(json.loads((self.state / "status.json").read_text())["action"], "queued")
        self.assert_gh_repo_binding()


if __name__ == "__main__":
    unittest.main()
