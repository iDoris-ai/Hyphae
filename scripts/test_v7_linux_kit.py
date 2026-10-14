#!/usr/bin/env python3
"""Unit tests for scripts/v7_linux_kit.py pure helpers and fail-closed exits.

These never build, exec legacy binaries, or need strace; the real exercise is
`python3 scripts/v7_linux_kit.py all ...` (see docs/agent/m1-v7-linux-testkit.md).
"""

import contextlib
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import v7_linux_kit as kit  # noqa: E402


class ParseTests(unittest.TestCase):
    def test_parse_and_classify(self):
        home = Path("/tmp/kit-home-x")
        text = "\n".join([
            'execve("/opt/x/hyphae", ["hyphae", "--version"], 0x7ffd /* 9 vars */) = 0',
            'openat(AT_FDCWD, "/tmp/kit-home-x/.hyphae/outbox.json", O_RDWR|O_CREAT|O_CLOEXEC, 0600) = 3',
            'renameat2(AT_FDCWD, "/tmp/kit-home-x/a", AT_FDCWD, "/tmp/kit-home-x/b", 0) = 0',
            'unlinkat(AT_FDCWD, "/tmp/kit-home-x/b", 0) = -1 ENOENT (No such file or directory)',
            'openat(AT_FDCWD, "/sys/kernel/mm/transparent_hugepage/hpage_pmd_size", O_RDONLY) = 3',
            '+++ exited with 0 +++',
        ])
        ev = kit.parse_trace_text(text, "42")
        self.assertEqual(len(ev), 5)
        c = kit.classify(ev, home)
        self.assertEqual(c["execs"], [{"pid": "42", "path": "/opt/x/hyphae", "ok": True}])
        muts = {(m["syscall"], m["path"]) for m in c["mutations"]}
        self.assertIn(("openat", "/tmp/kit-home-x/.hyphae/outbox.json"), muts)
        self.assertIn(("renameat2", "/tmp/kit-home-x/a"), muts)
        self.assertNotIn("unlinkat", {m["syscall"] for m in c["mutations"]})  # failed call is not a mutation
        self.assertEqual(len(c["store_touches"]), 1)

    def test_read_only_open_is_not_mutation(self):
        ev = kit.parse_trace_text('openat(AT_FDCWD, "/etc/hosts", O_RDONLY|O_CLOEXEC) = 3')
        self.assertEqual(kit.classify(ev, Path("/nonexistent"))["mutations"], [])

    def test_group_id_hits(self):
        self.assertEqual(kit.group_id_hits(["hyphae version legacy-abc"]), 0)
        self.assertEqual(kit.group_id_hits(["held: 82bd2972cccd0403b59cdaa4412c6664"]), 1)
        self.assertEqual(kit.group_id_hits(["group_id=" + "a" * 32]), 1)

    def test_snapshot_diff(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d)
            a = kit.snapshot(p)
            (p / "f").write_text("x")
            diff = kit.snapshot_diff(a, kit.snapshot(p))
            self.assertEqual([x["path"] for x in diff], ["f"])


class FailClosedTests(unittest.TestCase):
    def _run(self, argv):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            rc = kit.main(argv)
        return rc, out.getvalue()

    def test_windows_is_notimplemented_nonzero(self):
        rc, out = self._run(["windows"])
        self.assertEqual(rc, kit.EXIT_NOTIMPLEMENTED)
        self.assertEqual(json.loads(out)["status"], "NOTIMPLEMENTED")

    def test_harness_without_launcher_is_notimplemented_nonzero(self):
        with tempfile.TemporaryDirectory() as d:
            rc, out = self._run(["harness", "--work", d])
        self.assertEqual(rc, kit.EXIT_NOTIMPLEMENTED)
        res = json.loads(out)
        self.assertTrue(all(s["status"] == "NOTIMPLEMENTED" for s in res["sequence"]))
        self.assertEqual([s["step"] for s in res["sequence"]], list(kit.SEQUENCE))
        self.assertTrue(res["quiesce_selftest"]["pass"])

    def test_harness_with_fake_launcher_still_notimplemented(self):
        with tempfile.TemporaryDirectory() as d:
            rc, _ = self._run(["harness", "--work", d, "--launcher-bin", "/bin/true",
                               "--launcher-source-sha", "0" * 40])
        self.assertEqual(rc, kit.EXIT_NOTIMPLEMENTED)

    def test_missing_strace_is_skip_nonzero(self):
        with tempfile.TemporaryDirectory() as d:
            m = Path(d) / "m.json"
            m.write_text(json.dumps({"legacy": [], "forward_new": None}))
            with self.assertRaises(SystemExit) as cm:
                self._run(["probe", "--work", d, "--manifest", str(m), "--strace", "/nonexistent/strace"])
        self.assertEqual(cm.exception.code, kit.EXIT_NOTIMPLEMENTED)

    def test_frozen_shas_exact(self):
        self.assertEqual(sorted(kit.FROZEN), sorted([
            "699b36f74c6b3802b93768d670229d7b2d3b6cff",
            "c0a54111f78f357c6ae2772f06f9a3cc4fe7c63b",
            "7861d029b5ccc2959b80a184a390ec132ddd025f"]))


if __name__ == "__main__":
    unittest.main()
