"""Unit tests for validation, isolation, HTTP auth handling, and exact cleanup."""
from __future__ import annotations

import argparse
import errno
import hashlib
import http.server
import importlib.util
import inspect
import json
import os
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path
from types import SimpleNamespace

SCRIPT = Path(__file__).with_name("test_agent24_joint.py")
SPEC = importlib.util.spec_from_file_location("agent24_joint", SCRIPT)
assert SPEC and SPEC.loader
joint = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = joint
SPEC.loader.exec_module(joint)


class JointRunnerTests(unittest.TestCase):
    def args(self, root: Path) -> argparse.Namespace:
        paths = {}
        hashes = {}
        for name in ("hyphae", "agent24", "agent24d", "relay"):
            path = root / name
            path.write_bytes((name + " test binary").encode())
            path.chmod(0o700)
            paths[f"{name}_bin"] = path
            hashes[f"expected_{name}_sha256"] = joint.sha256_file(path)
        platform = joint.host_platform()
        lock = {
            "schema": 1,
            "source_sha": "a" * 40,
            "go": "go1.27.1",
            "recipe": "release-build --trimpath",
            "binaries": {platform: hashes["expected_hyphae_sha256"]},
        }
        lock_path = root / "hyphae.lock.json"
        lock_path.write_text(json.dumps(lock), encoding="utf-8")
        return argparse.Namespace(
            **paths,
            **hashes,
            hyphae_sha="a" * 40,
            agent24_sha="b" * 40,
            lock=lock_path,
            expected_lock_sha256=joint.sha256_file(lock_path),
        )

    def test_complete_inputs_pass_and_both_source_shas_are_reported_separately(self):
        with tempfile.TemporaryDirectory() as tmp:
            args = self.args(Path(tmp))
            hashes = joint.check_inputs(args)
            self.assertEqual(set(hashes), {"hyphae", "agent24", "agent24d", "relay"})
            self.assertEqual(args.hyphae_sha, "a" * 40)
            self.assertEqual(args.agent24_sha, "b" * 40)

    def test_missing_binary_and_wrong_hash_fail_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = self.args(root)
            args.agent24d_bin = root / "missing-agent24d"
            with self.assertRaisesRegex(joint.SafeFailure, "^agent24d-binary-missing$"):
                joint.check_inputs(args)
            args = self.args(root)
            args.expected_agent24_sha256 = "0" * 64
            with self.assertRaisesRegex(joint.SafeFailure, "^agent24-binary-hash-mismatch$"):
                joint.check_inputs(args)

    def test_missing_inputs_are_required_by_command_line_parser(self):
        proc = subprocess.run([sys.executable, str(SCRIPT)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
        self.assertEqual(proc.returncode, 2)
        self.assertIn(b"--hyphae-bin", proc.stderr)
        help_result = subprocess.run([sys.executable, str(SCRIPT), "--help"], stdout=subprocess.PIPE,
                                     stderr=subprocess.PIPE, check=False)
        self.assertEqual(help_result.returncode, 0)
        self.assertIn(b"--expected-lock-sha256", help_result.stdout)

    def test_production_lock_hash_and_source_are_hard_gates(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = self.args(root)
            args.expected_lock_sha256 = "0" * 64
            with self.assertRaisesRegex(joint.SafeFailure, "^production-lock-hash-mismatch$"):
                joint.check_inputs(args)
            args = self.args(root)
            args.hyphae_sha = "c" * 40
            with self.assertRaisesRegex(joint.SafeFailure, "^production-lock-source-mismatch$"):
                joint.check_inputs(args)

    def test_positive_control_assertion_failure_is_a_real_failure(self):
        with self.assertRaisesRegex(joint.SafeFailure, "^authenticated-http-rejected$"):
            joint.require(False, "authenticated-http-rejected")

    def test_initial_contact_setup_avoids_managed_duplicate_and_agent24_adds_before_send(self):
        from unittest import mock

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            managed, peer = root / "managed", root / "peer"
            with mock.patch.object(joint, "invoke_hyphae", return_value={}) as direct:
                joint.configure_direct_relays_and_peer_contact(
                    Path("/bin/hyphae"), managed, peer, root, "ws://127.0.0.1:9000", "npub1alice", 2.0)
            calls = [call.args for call in direct.call_args_list]
            direct_contacts = [args for args in calls if args[3][:2] == ["contact", "add"]]
            self.assertEqual(len(direct_contacts), 1)
            self.assertEqual(direct_contacts[0][1], peer)
            self.assertEqual(direct_contacts[0][3], ["contact", "add", "--nickname", "alice", "--npub", "npub1alice"])
            self.assertEqual(sum(args[3][:2] == ["relay", "set"] for args in calls), 2)

            with mock.patch.object(joint, "invoke_agent24", return_value={}) as managed_contact:
                self.assertEqual(joint.add_managed_agent24_contact(Path("/bin/agent24"), managed, root, "npub1bob"), {})
            self.assertEqual(managed_contact.call_args.args[3], ["comm", "contact", "add", "bob", "npub1bob"])
            execute_source = inspect.getsource(joint.execute)
            self.assertLess(execute_source.index("add_managed_agent24_contact("),
                            execute_source.index('["comm", "send"'))

    def test_real_offline_send_shape_requires_l1_zero_publish_and_pending_retry(self):
        body = {
            "event_id": "e" * 64,
            "published_to": 0,
            "queued_for_retry": True,
            "layer": "L1",
        }
        self.assertEqual(joint.validate_offline_l1(body), "e" * 64)
        pending = [{"id": "e" * 64, "status": "pending"}]
        self.assertEqual(joint.outbox_entries_for(pending, "e" * 64), pending)
        self.assertEqual(joint.outbox_entries_for([], "e" * 64), [])
        for field, value, label in (
            ("published_to", 1, "offline-send-published-to-not-zero"),
            ("queued_for_retry", False, "offline-send-not-queued"),
            ("layer", "L2", "offline-send-layer-not-l1"),
            ("event_id", "short", "offline-send-event-id-invalid"),
        ):
            invalid = dict(body)
            invalid[field] = value
            with self.assertRaisesRegex(joint.SafeFailure, f"^{label}$"):
                joint.validate_offline_l1(invalid)

    def test_agent24_daemon_status_reads_data_process_generation_and_failure_counter(self):
        data = {"process": {"state": "running", "generation": 17, "consecutive_failures": 2}}
        status = joint.daemon_process_status(data, "status")
        self.assertEqual(status["generation"], 17)
        self.assertEqual(status["consecutive_failures"], 2)
        self.assertEqual(status["state"], "running")
        with self.assertRaisesRegex(joint.SafeFailure, "^status-generation-shape$"):
            joint.daemon_process_status({"process": {**data["process"], "generation": "17"}}, "status")

    def test_generation_restart_requires_running_and_unchanged_failure_count(self):
        previous = {"generation": 7, "consecutive_failures": 3}
        status = {"process": {"state": "running", "generation": 8, "consecutive_failures": 3}}
        current = joint.daemon_process_status(status, "status")
        self.assertNotEqual(current["generation"], previous["generation"])
        self.assertEqual(current["consecutive_failures"], previous["consecutive_failures"])
        failed_status = {"process": {"state": "running", "generation": 8, "consecutive_failures": 4}}
        with self.assertRaisesRegex(joint.SafeFailure, "^config-restart-failed-count-changed$"):
            joint.require(joint.daemon_process_status(failed_status, "status")["consecutive_failures"] == previous["consecutive_failures"],
                          "config-restart-failed-count-changed")

    def test_duplicate_json_lock_fields_fail(self):
        with self.assertRaisesRegex(joint.SafeFailure, "^production-lock-duplicate-field$"):
            joint.unique_object([("schema", 1), ("schema", 2)])

    def test_environment_isolated_from_unrelated_secrets(self):
        previous = os.environ.get("JOINT_PRIVATE_CANARY")
        os.environ["JOINT_PRIVATE_CANARY"] = "must-not-cross"
        try:
            with tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                home = root / "home"
                home.mkdir()
                code = "import json,os;print(json.dumps({'home':os.getenv('HOME'),'canary':os.getenv('JOINT_PRIVATE_CANARY')}))"
                stdout, stderr = joint.run_child([sys.executable, "-c", code], joint.child_env(home, root), timeout=3)
                self.assertEqual(stderr, b"")
                data = json.loads(stdout)
                self.assertEqual(data["home"], str(home))
                self.assertIsNone(data["canary"])
        finally:
            if previous is None:
                os.environ.pop("JOINT_PRIVATE_CANARY", None)
            else:
                os.environ["JOINT_PRIVATE_CANARY"] = previous

    def test_process_group_sigterm_cleanup_is_bounded_and_exact(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            home.mkdir()
            proc = subprocess.Popen([sys.executable, "-c", "import signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);time.sleep(30)"],
                                    env=joint.child_env(home, root), start_new_session=True,
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            pid = proc.pid
            joint.stop_owned_group(proc)
            self.assertIsNotNone(proc.poll())
            self.assertFalse(joint.process_alive(pid))

    def test_owned_group_term_race_after_complete_exit_is_confirmed_clean(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            home.mkdir()
            proc = subprocess.Popen([sys.executable, "-c", "import time;time.sleep(30)"],
                                    env=joint.child_env(home, root), start_new_session=True,
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            pgid = proc.pid
            os.killpg(pgid, signal.SIGKILL)
            proc.wait(timeout=2)
            self.assertFalse(joint.owned_group_exists(pgid))
            joint.stop_owned_group(proc)
            self.assertFalse(joint.owned_group_exists(pgid))

    def test_owned_group_kill_escalation_race_only_passes_after_group_disappears(self):
        from unittest import mock

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            home.mkdir()
            proc = subprocess.Popen(
                [sys.executable, "-c", "import signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);print('ready',flush=True);time.sleep(30)"],
                env=joint.child_env(home, root), start_new_session=True,
                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            assert proc.stdout is not None
            self.assertEqual(proc.stdout.readline().strip(), b"ready")
            real_killpg = os.killpg
            escalated = False

            def vanish_during_kill(pgid, sig):
                nonlocal escalated
                if sig == signal.SIGKILL:
                    escalated = True
                    real_killpg(pgid, sig)
                    proc.wait(timeout=2)
                    raise ProcessLookupError(errno.ESRCH, "group disappeared during escalation")
                return real_killpg(pgid, sig)

            try:
                with mock.patch.object(joint.os, "killpg", side_effect=vanish_during_kill):
                    joint.stop_owned_group(proc, force=True)
                self.assertTrue(escalated)
                self.assertFalse(joint.owned_group_exists(proc.pid))
            finally:
                if proc.poll() is None:
                    proc.kill()
                    proc.wait(timeout=2)
                proc.stdout.close()

    def test_signal_cleanup_escalates_only_the_owned_group(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            home.mkdir()
            daemon = joint.Agent24d(Path(sys.executable), home, root, Path(sys.executable))
            daemon.proc = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"],
                                           env=joint.child_env(home, root), start_new_session=True,
                                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            pid = daemon.proc.pid
            daemon.stop(force=True, sig=signal.SIGKILL)
            self.assertIsNone(daemon.proc)
            self.assertFalse(joint.process_alive(pid))

    def test_pid_start_marker_mismatch_refuses_to_kill_reused_pid(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            comm = home / ".agent24" / "comm"
            comm.mkdir(parents=True)
            proc = subprocess.Popen([sys.executable, "-c", "import signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);print('ready',flush=True);time.sleep(30)"],
                                    start_new_session=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            assert proc.stdout is not None
            self.assertEqual(proc.stdout.readline().strip(), b"ready")
            record = {
                "pid": proc.pid,
                "pgid": proc.pid,
                "start_marker": "Mon Jan  1 00:00:00 1900",
                "generation": 1,
                "bin_sha256": "a" * 64,
            }
            pidfile = comm / "hyphae-daemon.pid"
            pidfile.write_text(json.dumps(record), encoding="utf-8")
            try:
                with self.assertRaisesRegex(joint.SafeFailure, "^managed-hyphae-start-marker-mismatch$"):
                    joint.cleanup_managed_group(home, "a" * 64, record)
                self.assertTrue(joint.process_alive(proc.pid), "marker mismatch must leave process untouched")
            finally:
                joint.stop_owned_group(proc, force=True)
                if proc.stdout:
                    proc.stdout.close()

    def test_owned_group_lookup_race_with_remaining_member_still_fails(self):
        from unittest import mock

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            home.mkdir()
            proc = subprocess.Popen(
                [sys.executable, "-c", "import signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);print('ready',flush=True);time.sleep(30)"],
                env=joint.child_env(home, root), start_new_session=True,
                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            assert proc.stdout is not None
            self.assertEqual(proc.stdout.readline().strip(), b"ready")
            real_killpg = os.killpg

            def report_kill_race_but_leave_group(pgid, sig):
                if sig == signal.SIGKILL:
                    raise ProcessLookupError(errno.ESRCH, "simulated ESRCH while group still exists")
                return real_killpg(pgid, sig)

            try:
                with mock.patch.object(joint.os, "killpg", side_effect=report_kill_race_but_leave_group):
                    with self.assertRaisesRegex(joint.SafeFailure, "^owned-process-group-signal-failed$"):
                        joint.stop_owned_group(proc, force=True)
                self.assertTrue(joint.owned_group_exists(proc.pid), "live owned group must not be accepted as clean")
            finally:
                try:
                    real_killpg(proc.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                proc.wait(timeout=2)
                proc.stdout.close()

    def test_real_pid_start_marker_matches_and_group_cleanup_confirms_empty(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            comm = home / ".agent24" / "comm"
            comm.mkdir(parents=True)
            proc = subprocess.Popen([sys.executable, "-c", "import signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);print('ready',flush=True);time.sleep(30)"],
                                    start_new_session=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            assert proc.stdout is not None
            self.assertEqual(proc.stdout.readline().strip(), b"ready")
            record = {"pid": proc.pid, "pgid": proc.pid, "start_marker": joint.process_start_marker(proc.pid),
                      "generation": 1, "bin_sha256": "a" * 64}
            pidfile = comm / "hyphae-daemon.pid"
            pidfile.write_text(json.dumps(record), encoding="utf-8")
            self.assertEqual(joint.read_managed_pid_record(home, "a" * 64)["start_marker"], record["start_marker"])
            reaper = threading.Thread(target=proc.wait, daemon=True)
            reaper.start()
            self.assertTrue(joint.cleanup_managed_group(home, "a" * 64, record, grace_seconds=1))
            self.assertFalse(joint.process_group_alive(proc.pid))
            self.assertFalse(pidfile.exists())
            reaper.join(timeout=1)
            proc.stdout.close()

    def test_leader_exit_with_live_descendant_is_not_misreported_as_clean(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            comm = home / ".agent24" / "comm"
            comm.mkdir(parents=True)
            code = ("import subprocess,sys,time; "
                    "p=subprocess.Popen([sys.executable,'-c',"
                    "'import signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);time.sleep(30)']); "
                    "print(p.pid,flush=True);time.sleep(30)")
            proc = subprocess.Popen([sys.executable, "-c", code], start_new_session=True,
                                    stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            assert proc.stdout is not None
            descendant_pid = int(proc.stdout.readline().strip())
            record = {"pid": proc.pid, "pgid": proc.pid, "start_marker": joint.process_start_marker(proc.pid),
                      "generation": 1, "bin_sha256": "a" * 64}
            (comm / "hyphae-daemon.pid").write_text(json.dumps(record), encoding="utf-8")
            try:
                proc.terminate()
                proc.wait(timeout=2)
                self.assertTrue(joint.process_group_alive(proc.pid), "descendant should remain after leader exits")
                with self.assertRaisesRegex(joint.SafeFailure, "^managed-hyphae-process-not-live$"):
                    joint.cleanup_managed_group(home, "a" * 64, record, grace_seconds=0.3)
                self.assertTrue(joint.process_group_alive(proc.pid), "descendant keeps the original PGID alive")
            finally:
                try:
                    os.kill(descendant_pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                if proc.poll() is None:
                    proc.wait(timeout=2)
                if proc.stdout:
                    proc.stdout.close()

    def test_pid_marker_change_blocks_kill_escalation(self):
        from unittest import mock

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            comm = home / ".agent24" / "comm"
            comm.mkdir(parents=True)
            proc = subprocess.Popen([sys.executable, "-c", "import signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);print('ready',flush=True);time.sleep(30)"],
                                    start_new_session=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            assert proc.stdout is not None
            self.assertEqual(proc.stdout.readline().strip(), b"ready")
            record = {"pid": proc.pid, "pgid": proc.pid, "start_marker": joint.process_start_marker(proc.pid),
                      "generation": 1, "bin_sha256": "a" * 64}
            pidfile = comm / "hyphae-daemon.pid"
            pidfile.write_text(json.dumps(record), encoding="utf-8")
            real_sleep = time.sleep
            changed = False

            def mutate_marker(seconds):
                nonlocal changed
                if not changed:
                    altered = dict(record, start_marker="changed-marker")
                    pidfile.write_text(json.dumps(altered), encoding="utf-8")
                    changed = True
                real_sleep(seconds)

            try:
                with mock.patch.object(joint.time, "sleep", side_effect=mutate_marker):
                    with self.assertRaisesRegex(joint.SafeFailure, "^managed-hyphae-start-marker-mismatch$"):
                        joint.cleanup_managed_group(home, "a" * 64, record, grace_seconds=0.1)
                self.assertTrue(joint.process_alive(proc.pid), "marker change must prevent KILL")
            finally:
                try:
                    proc.kill()
                except ProcessLookupError:
                    pass
                proc.wait(timeout=2)
                proc.stdout.close()

    def test_unlock_success_404_and_reunlock_after_new_daemon_generation(self):
        seen = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                length = int(self.headers.get("Content-Length", "0"))
                seen.append((self.headers.get("Authorization"), json.loads(self.rfile.read(length)))
                            if length else (self.headers.get("Authorization"), None))
                if self.path == "/missing":
                    self.send_response(404)
                    self.end_headers()
                    return
                body = json.dumps({"ok": True, "data": {"unlocked": True, "remembered": False}}).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *_args):
                pass

        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as tmp:
                home = Path(tmp) / "home"
                state_dir = home / ".agent24"
                state_dir.mkdir(parents=True)
                fake_process = SimpleNamespace(state={}, proc=SimpleNamespace(pid=12345))
                password = "unit-test-password-do-not-log"
                recorder = joint.RunRecorder()
                previous_recorder = joint._ACTIVE_RECORDER
                joint._ACTIVE_RECORDER = recorder
                try:
                    for token in ("generation-one", "generation-two"):
                        (state_dir / "daemon.json").write_text(json.dumps({
                            "pid": 12345, "port": server.server_port, "token": token,
                            "auth_mode": "legacy_single_token",
                        }), encoding="utf-8")
                        result = joint.unlock_agent24d(home, fake_process, password)
                        self.assertEqual(result, {"unlocked": True, "remembered": False})
                finally:
                    joint._ACTIVE_RECORDER = previous_recorder
                self.assertEqual(len(seen), 2)
                self.assertEqual([row[0] for row in seen], ["Bearer generation-one", "Bearer generation-two"])
                self.assertEqual([row[1] for row in seen], [
                    {"password": password, "remember": False}, {"password": password, "remember": False},
                ])
                self.assertNotIn(password, json.dumps(recorder.executions))
            missing_status, missing_envelope = joint.http_json(
                f"http://127.0.0.1:{server.server_port}/missing", None, method="POST")
            self.assertEqual(missing_status, 404)
            with self.assertRaisesRegex(joint.Blocked, "^comm-unlock-route-not-implemented$"):
                joint.validate_unlock_response(missing_status, missing_envelope)
            for invalid_status, invalid_body in ((500, None), (200, {"ok": True, "data": {"unlocked": False, "remembered": False}}),
                                                 (200, {"ok": True, "data": {"unlocked": True, "remembered": True}})):
                with self.assertRaises(joint.SafeFailure):
                    joint.validate_unlock_response(invalid_status, invalid_body)
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

    def test_failure_execution_and_cleanup_evidence_are_redacted_and_persisted(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            home.mkdir()
            recorder = joint.RunRecorder()
            previous = joint._ACTIVE_RECORDER
            joint._ACTIVE_RECORDER = recorder
            try:
                recorder.current_stage = "offline-send-control"
                code = "import sys; print('private-message-body', file=sys.stderr); raise SystemExit(7)"
                with self.assertRaisesRegex(joint.SafeFailure, "^child-exit-code$"):
                    joint.run_child([sys.executable, "-c", code], joint.child_env(home, root), expected_exit=0)
                command = joint.sanitized_argv(["agent24", "comm", "send", "bob", "sensitive body", "--from", "alice"])
                self.assertNotIn("sensitive body", command)
                self.assertIn("[REDACTED]", command)
                recorder.note_cleanup("managed-hyphae-process-group", "failed", reason="start-marker-mismatch")
            finally:
                joint._ACTIVE_RECORDER = previous
            run_record = {
                "result": "FAIL",
                "failed_at_stage": "offline-send-control",
                "started_at": "2026-10-05T00:00:00.000Z",
                "ended_at": "2026-10-05T00:00:01.000Z",
                "executions": recorder.executions,
                "cleanup": recorder.cleanup,
            }
            evidence = root / "evidence.json"
            joint.write_evidence(evidence, run_record)
            saved = json.loads(evidence.read_text(encoding="utf-8"))
            self.assertEqual(saved["result"], "FAIL")
            self.assertEqual(saved["executions"][0]["exit"], 7)
            self.assertTrue(saved["executions"][0]["started_at"])
            self.assertTrue(saved["executions"][0]["ended_at"])
            self.assertEqual(saved["executions"][0]["cleanup"], "not-needed")
            self.assertEqual(saved["cleanup"][0]["status"], "failed")
            self.assertNotIn("private-message-body", evidence.read_text(encoding="utf-8"))

    def test_timeout_does_not_leak_descendants_holding_output_pipe(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            home.mkdir()
            code = "import subprocess,sys; subprocess.Popen([sys.executable,'-c','import time;time.sleep(30)']); print('ready',flush=True)"
            started = time.monotonic()
            with self.assertRaisesRegex(joint.SafeFailure, "^child-timeout$"):
                joint.run_child([sys.executable, "-c", code], joint.child_env(home, root), timeout=0.2)
            self.assertLess(time.monotonic() - started, 5)


if __name__ == "__main__":
    unittest.main()
