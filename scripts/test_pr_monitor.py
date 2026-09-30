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
        self.main_sha = "a" * 40
        self.branch_returncode = 0
        self.branch_body = {"commit": {"sha": self.main_sha}}
        self.workflow_runs = None
        self.workflow_returncode = 0

    def tearDown(self):
        self.temp.cleanup()

    def fake_run(self, args, **kwargs):
        self.calls.append((args, kwargs))
        if args[1:2] == ["api"]:
            output = json.dumps(self.branch_body)
            return pr_monitor.subprocess.CompletedProcess(args, self.branch_returncode, output, "branch API unavailable" if self.branch_returncode else "")
        if args[1:3] == ["pr", "list"]:
            return pr_monitor.subprocess.CompletedProcess(args, 0, '[{"number": 1}]', "")
        if args[1:3] == ["run", "list"]:
            if self.workflow_returncode:
                return pr_monitor.subprocess.CompletedProcess(args, self.workflow_returncode, "", "workflow API unavailable")
            if self.workflow_runs is not None:
                rows = self.workflow_runs
            elif "--workflow" in args and args[args.index("--workflow") + 1] == "ci.yml" and "--commit" in args and args[args.index("--commit") + 1] == self.main_sha:
                rows = [{"status": "completed", "conclusion": "failure", "headSha": self.main_sha, "url": "current-ci", "workflowName": "CI"}]
            else:
                rows = [
                    {"status": "completed", "conclusion": "failure", "headSha": self.main_sha, "url": "current-ci", "workflowName": "CI"},
                    {"status": "completed", "conclusion": "success", "headSha": self.main_sha, "url": "updater-noop", "workflowName": "Update Dependencies"},
                    {"status": "completed", "conclusion": "success", "headSha": "b" * 40, "url": "old-ci", "workflowName": "CI"},
                ]
            return pr_monitor.subprocess.CompletedProcess(args, 0, json.dumps(rows), "")
        return pr_monitor.subprocess.CompletedProcess(args, 0, "queued", "")

    def assert_gh_repo_binding(self):
        gh_calls = self.calls[:3]
        self.assertEqual(gh_calls[0][0][1:], ["api", "repos/example/hyphae/branches/main"])
        for args, _ in gh_calls[1:]:
            self.assertIn("--repo", args)
            self.assertEqual(args[args.index("--repo") + 1], "example/hyphae")
        run_args = gh_calls[2][0]
        self.assertEqual(run_args[run_args.index("--workflow") + 1], "ci.yml")
        self.assertEqual(run_args[run_args.index("--commit") + 1], self.main_sha)
        self.assertIn("workflowName", run_args[run_args.index("--json") + 1].split(","))
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
        self.assertEqual(len(self.calls), 3)
        self.assert_gh_repo_binding()

    @patch("pr_monitor.subprocess.run")
    def test_scan_only_never_queues(self, run):
        run.side_effect = self.fake_run
        self.assertEqual(pr_monitor.execute(self.config, scan_only=True), 0)
        self.assertEqual(json.loads((self.state / "status.json").read_text())["action"], "scan_only")
        self.assertEqual(len(self.calls), 3)
        self.assert_gh_repo_binding()

    @patch("pr_monitor.subprocess.run")
    def test_missing_database_fails_closed(self, run):
        run.side_effect = self.fake_run
        self.config["queue_db"] = str(self.root / "missing.sqlite")
        self.assertEqual(pr_monitor.execute(self.config), 1)
        status = json.loads((self.state / "status.json").read_text())
        self.assertEqual(status["action"], "error")
        self.assertIn("error", status)
        self.assertEqual(len(self.calls), 3)

    @patch("pr_monitor.subprocess.run")
    def test_queue_failure_is_not_reported_as_queued(self, run):
        def failing(args, **kwargs):
            self.calls.append((args, kwargs))
            if args[1:2] == ["api"]:
                return pr_monitor.subprocess.CompletedProcess(args, 0, json.dumps({"commit": {"sha": self.main_sha}}), "")
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

    @patch("pr_monitor.subprocess.run")
    def test_current_main_failure_is_not_hidden_by_updater_or_old_green(self, run):
        run.side_effect = self.fake_run
        self.assertEqual(pr_monitor.execute(self.config, scan_only=True), 0)
        snapshot = json.loads((self.state / "latest.json").read_text())
        self.assertEqual(snapshot["main_sha"], self.main_sha)
        self.assertEqual(snapshot["main_workflow"], "ci.yml")
        self.assertFalse(snapshot["main_ci_missing"])
        self.assertEqual([item["url"] for item in snapshot["main_runs"]], ["current-ci"])
        self.assertEqual(snapshot["main_runs"][0]["conclusion"], "failure")
        self.assert_gh_repo_binding()

    @patch("pr_monitor.subprocess.run")
    def test_current_head_without_ci_is_explicitly_missing(self, run):
        run.side_effect = self.fake_run
        self.workflow_runs = []
        self.assertEqual(pr_monitor.execute(self.config, scan_only=True), 0)
        snapshot = json.loads((self.state / "latest.json").read_text())
        self.assertEqual(snapshot["main_sha"], self.main_sha)
        self.assertEqual(snapshot["main_runs"], [])
        self.assertTrue(snapshot["main_ci_missing"])

    @patch("pr_monitor.subprocess.run")
    def test_branch_lookup_failure_or_invalid_sha_never_queues_or_writes_snapshot(self, run):
        for returncode, body in [(1, {}), (0, {"commit": {"sha": "not-a-commit"}})]:
            with self.subTest(returncode=returncode, body=body):
                self.calls.clear()
                self.state.mkdir(parents=True, exist_ok=True)
                self.branch_returncode = returncode
                self.branch_body = body
                run.side_effect = self.fake_run
                self.assertEqual(pr_monitor.execute(self.config), 1)
                status = json.loads((self.state / "status.json").read_text())
                self.assertEqual(status["action"], "error")
                self.assertFalse((self.state / "latest.json").exists())
                self.assertFalse(any(args[1:3] == ["run", "list"] for args, _ in self.calls))
                self.assertFalse(any(args[1:3] == ["queue", "--thread"] for args, _ in self.calls))

    @patch("pr_monitor.subprocess.run")
    def test_workflow_scan_failure_never_queues_or_writes_snapshot(self, run):
        run.side_effect = self.fake_run
        self.workflow_returncode = 1
        self.assertEqual(pr_monitor.execute(self.config), 1)
        status = json.loads((self.state / "status.json").read_text())
        self.assertEqual(status["action"], "error")
        self.assertFalse((self.state / "latest.json").exists())
        self.assertFalse(any(args[1:3] == ["queue", "--thread"] for args, _ in self.calls))


if __name__ == "__main__":
    unittest.main()
