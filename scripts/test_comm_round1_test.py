"""Unit checks for safety-critical orchestration helpers; these are not relay acceptance."""
import hashlib
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import threading
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("test_comm_round1.py")
spec = importlib.util.spec_from_file_location("comm_round1", SCRIPT)
assert spec and spec.loader
comm_round1 = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = comm_round1
spec.loader.exec_module(comm_round1)


class RoundOneHarnessTests(unittest.TestCase):
    def lock(self):
        return {
            "schema": 1,
            "source_sha": comm_round1.BASELINE,
            "go": "go1.26.4",
            "recipe": "release-build --trimpath",
            "binaries": dict(comm_round1.EXPECTED_BINARY_SHA),
        }

    def write_lock(self, directory, value):
        path = Path(directory) / "lock.json"
        path.write_text(json.dumps(value), encoding="utf-8")
        return path

    def test_production_lock_requires_source_go_recipe_and_full_platform_map(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = self.write_lock(tmp, self.lock())
            parsed, expected = comm_round1.load_lock(path, "release-build --trimpath", "darwin-arm64")
            self.assertEqual(parsed["source_sha"], comm_round1.BASELINE)
            self.assertEqual(expected, comm_round1.EXPECTED_BINARY_SHA["darwin-arm64"])
            for field, bad in (("source_sha", "0" * 40), ("go", "go1.27.1"), ("recipe", "other")):
                lock = self.lock()
                lock[field] = bad
                path.write_text(json.dumps(lock), encoding="utf-8")
                with self.assertRaises(comm_round1.SafeFailure):
                    comm_round1.load_lock(path, "release-build --trimpath", "darwin-arm64")
            lock = self.lock()
            lock["binaries"]["darwin-arm64"] = None
            path.write_text(json.dumps(lock), encoding="utf-8")
            with self.assertRaises(comm_round1.SafeFailure):
                comm_round1.load_lock(path, "release-build --trimpath", "darwin-arm64")
            with self.assertRaises(comm_round1.SafeFailure):
                comm_round1.load_lock(path, "release-build --trimpath", "other")

    def test_real_production_lock_file_has_the_expected_schema_and_platform_map(self):
        lock_path = os.environ.get("HYPHAE_COMM_ROUND1_TEST_LOCK")
        path = Path(lock_path) if lock_path else SCRIPT.parent / "fixtures" / "comm-round1" / "hyphae.lock.json"
        if not lock_path:
            self.assertEqual(hashlib.sha256(path.read_bytes()).hexdigest(), "fbb96d21b72597029826d321d65a7d8a2428a3d9f509d079213bb7808667578a")
        raw = json.loads(path.read_text(encoding="utf-8"))
        self.assertEqual(raw["source_sha"], comm_round1.BASELINE)
        self.assertEqual(raw["binaries"], comm_round1.EXPECTED_BINARY_SHA)
        platform_name = comm_round1.host_platform()
        if platform_name in ("darwin-arm64", "linux-x64"):
            lock, expected = comm_round1.load_lock(path, raw["recipe"], platform_name)
            self.assertEqual(expected, lock["binaries"][platform_name])
        else:
            with self.assertRaisesRegex(comm_round1.SafeFailure, "^unsupported-platform$"):
                comm_round1.load_lock(path, raw["recipe"], platform_name)

    def test_verified_copy_uses_hash_and_refuses_symlink(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source, destination = root / "source", root / "copy"
            source.write_bytes(b"verified bytes")
            expected = hashlib.sha256(b"verified bytes").hexdigest()
            self.assertEqual(comm_round1.copy_verified_artifact(source, destination, expected), expected)
            self.assertEqual(destination.read_bytes(), b"verified bytes")
            with self.assertRaises(comm_round1.SafeFailure):
                comm_round1.copy_verified_artifact(source, root / "bad-copy", "0" * 64)
            link = root / "link"
            link.symlink_to(source)
            with self.assertRaises(comm_round1.SafeFailure):
                comm_round1.copy_verified_artifact(link, root / "link-copy", expected)

    def test_child_environment_is_allowlisted_and_stdin_is_pipe_only(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            home.mkdir()
            previous = os.environ.get("COMM_ROUND1_SECRET_CANARY")
            os.environ["COMM_ROUND1_SECRET_CANARY"] = "must-not-cross"
            try:
                code = "import json,os,sys; print(json.dumps({'home':os.environ.get('HOME'),'secret':os.getenv('COMM_ROUND1_SECRET_CANARY'),'stdin':sys.stdin.read()}))"
                status, stdout, stderr = comm_round1.run_child(
                    [sys.executable, "-c", code], comm_round1.child_env(home, root), b"password-on-stdin", timeout=3,
                )
            finally:
                if previous is None:
                    os.environ.pop("COMM_ROUND1_SECRET_CANARY", None)
                else:
                    os.environ["COMM_ROUND1_SECRET_CANARY"] = previous
            self.assertEqual((status, stderr), (0, b""))
            result = json.loads(stdout)
            self.assertEqual(result["home"], str(home))
            self.assertIsNone(result["secret"])
            self.assertEqual(result["stdin"], "password-on-stdin")

    def test_timeout_kills_child_and_hides_child_output(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            home.mkdir()
            code = "import time; print('private diagnostic', flush=True); time.sleep(20)"
            with self.assertRaisesRegex(comm_round1.SafeFailure, "^child-timeout$"):
                comm_round1.run_child([sys.executable, "-c", code], comm_round1.child_env(home, root), timeout=0.15)

    def test_timeout_cleans_group_when_parent_exits_but_descendant_holds_pipe(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            home.mkdir()
            code = "import subprocess,sys; subprocess.Popen([sys.executable,'-c','import time;time.sleep(20)']); print('parent-exit',flush=True)"
            with self.assertRaisesRegex(comm_round1.SafeFailure, "^child-timeout$"):
                comm_round1.run_child([sys.executable, "-c", code], comm_round1.child_env(home, root), timeout=0.2)

    def test_relay_reader_is_drained_and_joined_on_cleanup(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            data = root / "data"
            home.mkdir()
            data.mkdir()
            code = "import time; print('Hyphae relay listening on ws://127.0.0.1:34567',flush=True); time.sleep(20)"
            proc = subprocess.Popen([sys.executable, "-c", code], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env=comm_round1.child_env(home, root), start_new_session=True)
            relay = comm_round1.RelayProcess(Path(sys.executable), data, home, root, 34567)
            relay.proc = proc
            reader = threading.Thread(target=relay._read_ready, args=(proc,), daemon=True)
            relay.reader = reader
            reader.start()
            self.assertTrue(relay.ready_line.wait(2))
            relay.stop()
            self.assertFalse(reader.is_alive())
            self.assertIsNone(relay.proc)

    def test_envelope_and_exit_assertions_are_strict_and_generic(self):
        good = comm_round1.single_json_line(b'{"ok":true,"data":[] }\n', "bad-envelope")
        self.assertTrue(good["ok"])
        for raw in (b"", b'{"ok":true}\nnoise\n', b"not json\n"):
            with self.assertRaisesRegex(comm_round1.SafeFailure, "^bad-envelope$"):
                comm_round1.single_json_line(raw, "bad-envelope")
        with self.assertRaisesRegex(comm_round1.SafeFailure, "^expected-error$"):
            comm_round1.require_data(False, "expected-error")

    def test_deadlines_must_be_finite_and_positive(self):
        for value in (float("nan"), float("inf"), float("-inf"), 0.0, -1.0):
            self.assertFalse(comm_round1.validate_deadlines(value, 1.0))
            self.assertFalse(comm_round1.validate_deadlines(1.0, value))
        self.assertTrue(comm_round1.validate_deadlines(0.01, 0.01))

    def test_expected_error_envelope_does_not_echo_message(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            home.mkdir()
            fake_cli = root / "cli"
            fake_cli.write_text(
                "#!/usr/bin/env python3\nimport sys\nprint('{\\\"ok\\\":false,\\\"error\\\":\\\"user_error\\\",\\\"message\\\":\\\"private payload\\\"}', file=sys.stderr)\nraise SystemExit(1)\n",
                encoding="utf-8",
            )
            fake_cli.chmod(0o700)
            result = comm_round1.invoke_cli(fake_cli, home, root, ["contact", "add"], None, expected_exit=1, expected_error="user_error")
            self.assertEqual(result.error_code, "user_error")
            self.assertNotIn("private payload", repr(result))

    def test_password_argument_is_added_and_secret_only_reaches_stdin(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            home.mkdir()
            fake_cli = root / "cli"
            fake_cli.write_text(
                "#!/usr/bin/env python3\nimport json,sys\nprint(json.dumps({'ok':True,'data':{'argv':sys.argv[1:],'stdin':sys.stdin.read()}}))\n",
                encoding="utf-8",
            )
            fake_cli.chmod(0o700)
            result = comm_round1.invoke_cli(fake_cli, home, root, ["agent", "msg"], "secret-stdin")
            self.assertIn("--password-stdin", result.data["argv"])
            self.assertEqual(result.data["stdin"], "secret-stdin\n")
            self.assertNotIn("secret-stdin", result.data["argv"])


if __name__ == "__main__":
    unittest.main()
