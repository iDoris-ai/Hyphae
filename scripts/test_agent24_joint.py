#!/usr/bin/env python3
"""Run a strict, local-only Agent24 × Hyphae CLI/daemon/relay acceptance.

The runner intentionally accepts already-built binaries and a production lock;
it never builds code, contacts remote services, or reads a user's real HOME.
"""
from __future__ import annotations

import argparse
import datetime as dt
import errno
import hashlib
import math
import json
import os
import platform
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any


class SafeFailure(Exception):
    """A stable stage label safe to print without leaking test payloads."""


class Blocked(SafeFailure):
    """A known external contract gate; writes evidence and exits nonzero."""


class RunRecorder:
    def __init__(self) -> None:
        self.current_stage = "input-validation"
        self.executions: list[dict[str, Any]] = []
        self.cleanup: list[dict[str, Any]] = []

    def note_command(self, row: dict[str, Any]) -> None:
        self.executions.append(row)

    def note_cleanup(self, action: str, status: str, **fields: Any) -> None:
        self.cleanup.append({"action": action, "status": status, "at": utc_now(), **fields})

    def note_process(self, stage: str, argv: list[str], started_at: str,
                     exit_code: int | None, cleanup: str, failure: str | None = None) -> None:
        self.executions.append({
            "stage": stage,
            "command": sanitized_argv(argv),
            "exit": exit_code,
            "started_at": started_at,
            "ended_at": utc_now(),
            "cleanup": cleanup,
            "output_redacted": True,
            "failure": failure,
        })


_ACTIVE_RECORDER: RunRecorder | None = None
UNLOCK_ROUTE = "/api/v1/comm/unlock"


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def sanitized_argv(argv: list[str]) -> list[str]:
    """Keep a useful command shape while removing message/password values."""
    if not argv:
        return []
    out = [Path(argv[0]).name]
    redact_next = False
    values = argv[1:]
    comm_send_body_index: int | None = None
    try:
        send_index = values.index("send")
        if send_index >= 0 and "comm" in values[:send_index]:
            comm_send_body_index = send_index + 2
    except ValueError:
        pass
    positional_index = 0
    hyphae_msg = False
    for item in values:
        if redact_next:
            out.append("[REDACTED]")
            redact_next = False
            positional_index += 1
            continue
        if item in ("--password", "--content", "--content-file", "-c"):
            out.append(item)
            redact_next = True
            positional_index += 1
            continue
        if item == "msg" and "agent" in values[:positional_index + 1]:
            hyphae_msg = True
        if item == "--password-stdin":
            out.append(item)
            positional_index += 1
            continue
        if comm_send_body_index == positional_index or (hyphae_msg and item == "--content"):
            out.append("[REDACTED]")
        else:
            out.append(item)
        positional_index += 1
    return out


def require(ok: bool, label: str) -> None:
    if not ok:
        raise SafeFailure(label)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def valid_sha(value: str, width: int = 64) -> bool:
    return len(value) == width and all(char in "0123456789abcdef" for char in value)


def host_platform() -> str:
    machine = platform.machine().lower()
    if sys.platform == "darwin" and machine in ("arm64", "aarch64"):
        return "darwin-arm64"
    if sys.platform.startswith("linux") and machine in ("x86_64", "amd64"):
        return "linux-x64"
    return "unsupported"


def check_inputs(args: argparse.Namespace) -> dict[str, str]:
    """Validate all artifacts before starting any process or writing evidence."""
    require(valid_sha(args.hyphae_sha, 40), "hyphae-source-sha-invalid")
    require(valid_sha(args.agent24_sha, 40), "agent24-source-sha-invalid")
    require(valid_sha(args.expected_lock_sha256), "production-lock-hash-invalid")
    expected = {
        "hyphae": args.expected_hyphae_sha256,
        "agent24": args.expected_agent24_sha256,
        "agent24d": args.expected_agent24d_sha256,
        "relay": args.expected_relay_sha256,
    }
    for name, digest in expected.items():
        require(valid_sha(digest), f"{name}-hash-invalid")
    binaries = {
        "hyphae": args.hyphae_bin,
        "agent24": args.agent24_bin,
        "agent24d": args.agent24d_bin,
        "relay": args.relay_bin,
    }
    actual: dict[str, str] = {}
    for name, path in binaries.items():
        try:
            info = path.lstat()
        except OSError:
            raise SafeFailure(f"{name}-binary-missing") from None
        require(stat.S_ISREG(info.st_mode) and not path.is_symlink(), f"{name}-binary-not-regular")
        require(os.access(path, os.X_OK), f"{name}-binary-not-executable")
        actual[name] = sha256_file(path)
        require(actual[name] == expected[name], f"{name}-binary-hash-mismatch")

    try:
        lock_bytes = args.lock.read_bytes()
    except OSError:
        raise SafeFailure("production-lock-unreadable") from None
    require(hashlib.sha256(lock_bytes).hexdigest() == args.expected_lock_sha256, "production-lock-hash-mismatch")
    try:
        lock = json.loads(lock_bytes.decode("utf-8"), object_pairs_hook=unique_object)
    except (UnicodeError, json.JSONDecodeError):
        raise SafeFailure("production-lock-invalid") from None
    require(isinstance(lock, dict), "production-lock-invalid")
    require(lock.get("source_sha") == args.hyphae_sha, "production-lock-source-mismatch")
    binary_map = lock.get("binaries")
    require(isinstance(binary_map, dict), "production-lock-binaries-missing")
    plat = host_platform()
    require(plat != "unsupported", "unsupported-platform")
    require(binary_map.get(plat) == expected["hyphae"], "production-lock-hyphae-hash-mismatch")
    return actual


def unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for key, value in pairs:
        require(key not in out, "production-lock-duplicate-field")
        out[key] = value
    return out


def child_env(home: Path, temp_root: Path, extra: dict[str, str] | None = None) -> dict[str, str]:
    env = {
        "HOME": str(home),
        "TMPDIR": str(temp_root),
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
        "LC_ALL": "C",
    }
    if sys.platform == "darwin":
        env["TMP"] = str(temp_root)
        env["TEMP"] = str(temp_root)
    if extra:
        env.update(extra)
    return env


def stop_owned_group(proc: subprocess.Popen[bytes], force: bool = False) -> None:
    """Signal only the session/process group created for this exact child."""
    if getattr(proc, "_joint_group_stopped", False):
        return
    pgid = proc.pid
    # If the leader already exited and the exact PGID is empty, do not signal
    # a possibly reused numeric id; record success only after both checks.
    if proc.poll() is not None and not owned_group_exists(pgid):
        proc._joint_group_stopped = True
        return
    try:
        os.killpg(pgid, signal.SIGTERM)
    except OSError as error:
        if error.errno == errno.ESRCH and wait_owned_group_gone(pgid, 0.1) and proc.poll() is not None:
            proc._joint_group_stopped = True
            return
        raise SafeFailure("owned-process-group-signal-failed") from None
    try:
        proc.wait(timeout=2)
    except subprocess.TimeoutExpired:
        force = True
    # The leader may exit while a descendant still owns an inherited pipe.
    # A live original PGID proves there is still a group member to clean.
    if owned_group_exists(pgid):
        force = True
    if force:
        # An exited leader can be reaped before its descendants finish. Only
        # escalate while the original owned group still exists; an empty PGID
        # cannot be reused while any of its original members remain.
        if not owned_group_exists(pgid):
            require(proc.poll() is not None, "owned-process-group-cleanup-failed")
            proc._joint_group_stopped = True
            return
        try:
            os.killpg(pgid, signal.SIGKILL)
        except OSError as error:
            if error.errno == errno.ESRCH and wait_owned_group_gone(pgid, 0.1) and proc.poll() is not None:
                proc._joint_group_stopped = True
                return
            raise SafeFailure("owned-process-group-signal-failed") from None
        try:
            proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            raise SafeFailure("owned-process-group-cleanup-failed") from None
    if not wait_owned_group_gone(pgid, 3) or proc.poll() is None:
        raise SafeFailure("owned-process-group-cleanup-failed") from None
    proc._joint_group_stopped = True


def owned_group_exists(pgid: int) -> bool:
    try:
        os.killpg(pgid, 0)
        return True
    except OSError as error:
        if error.errno == errno.ESRCH:
            return False
        if error.errno == errno.EPERM:
            return True
        raise SafeFailure("owned-process-group-probe-failed") from None


def wait_owned_group_gone(pgid: int, timeout: float) -> bool:
    end = time.monotonic() + timeout
    while True:
        if not owned_group_exists(pgid):
            return True
        if time.monotonic() >= end:
            return False
        time.sleep(0.02)


def run_child(argv: list[str], env: dict[str, str], stdin: bytes = b"", timeout: float = 30.0,
              expected_exit: int = 0) -> tuple[bytes, bytes]:
    started_at = utc_now()
    proc: subprocess.Popen[bytes] | None = None
    exit_code: int | None = None
    cleanup = "not-needed"
    failure: str | None = None
    stdout = stderr = b""
    try:
        try:
            proc = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                    stderr=subprocess.PIPE, env=env, start_new_session=True)
        except (OSError, ValueError):
            raise SafeFailure("child-launch-failed") from None
        try:
            stdout, stderr = proc.communicate(stdin, timeout=timeout)
        except subprocess.TimeoutExpired:
            cleanup = "attempted"
            stop_owned_group(proc, force=True)
            cleanup = "completed"
            try:
                proc.communicate(timeout=2)
            except subprocess.TimeoutExpired:
                raise SafeFailure("child-pipes-did-not-close") from None
            raise SafeFailure("child-timeout") from None
        exit_code = proc.returncode
        require(exit_code == expected_exit, "child-exit-code")
        return stdout, stderr
    except SafeFailure as error:
        failure = str(error)
        raise
    finally:
        cleanup_failed = False
        if proc is not None:
            if not getattr(proc, "_joint_group_stopped", False):
                cleanup = "attempted"
                try:
                    stop_owned_group(proc)
                    cleanup = "completed"
                except SafeFailure:
                    cleanup = "failed"
                    failure = "child-cleanup-failed"
                    cleanup_failed = True
            exit_code = proc.poll() if exit_code is None else exit_code
            for pipe in (proc.stdin, proc.stdout, proc.stderr):
                if pipe is not None:
                    try:
                        pipe.close()
                    except OSError:
                        pass
        if _ACTIVE_RECORDER is not None:
            _ACTIVE_RECORDER.note_command({
                "stage": _ACTIVE_RECORDER.current_stage,
                "command": sanitized_argv(argv),
                "exit": exit_code,
                "started_at": started_at,
                "ended_at": utc_now(),
                "cleanup": cleanup,
                "stdout_bytes": len(stdout),
                "stderr_bytes": len(stderr),
                "output_redacted": True,
                "failure": failure,
            })
        if cleanup_failed:
            raise SafeFailure("child-cleanup-failed") from None


def parse_json(raw: bytes, label: str) -> Any:
    try:
        return json.loads(raw.decode("utf-8"))
    except (UnicodeError, json.JSONDecodeError):
        raise SafeFailure(label) from None


def invoke_hyphae(binary: Path, home: Path, tmp: Path, args: list[str], password: str | None = None,
                  timeout: float = 30.0, expected_exit: int = 0, expected_error: str | None = None) -> Any:
    argv = [str(binary), "--json", *args]
    stdin = b""
    if password is not None:
        if "--password-stdin" not in argv:
            argv.append("--password-stdin")
        stdin = password.encode("utf-8") + b"\n"
    stdout, stderr = run_child(argv, child_env(home, tmp), stdin, timeout, expected_exit)
    if expected_exit == 0:
        require(not stderr, "hyphae-unexpected-stderr")
        envelope = parse_json(stdout, "hyphae-json-invalid")
        require(isinstance(envelope, dict) and envelope.get("ok") is True and "data" in envelope,
                "hyphae-envelope-invalid")
        return envelope["data"]
    require(not stdout, "hyphae-error-on-stdout")
    envelope = parse_json(stderr, "hyphae-error-invalid")
    require(isinstance(envelope, dict) and envelope.get("ok") is False and envelope.get("error") == expected_error,
            "hyphae-error-contract")
    return envelope


def invoke_agent24(binary: Path, home: Path, tmp: Path, args: list[str], timeout: float = 30.0,
                   expected_exit: int = 0) -> Any:
    stdout, stderr = run_child([str(binary), *args], child_env(home, tmp), timeout=timeout, expected_exit=expected_exit)
    if expected_exit == 0:
        require(not stderr, "agent24-unexpected-stderr")
        return parse_json(stdout, "agent24-json-invalid")
    return {"stdout": stdout.decode("utf-8", "replace"), "stderr": stderr.decode("utf-8", "replace")}


class Relay:
    def __init__(self, binary: Path, data: Path, home: Path, tmp: Path, port: int):
        self.binary, self.data, self.home, self.tmp, self.port = binary, data, home, tmp, port
        self.proc: subprocess.Popen[bytes] | None = None
        self.ready = threading.Event()
        self.reader: threading.Thread | None = None

    def start(self, timeout: float) -> None:
        require(self.proc is None, "relay-already-running")
        argv = [str(self.binary), "--listen", "127.0.0.1", "--port", str(self.port), "--data-dir", str(self.data)]
        started_at = utc_now()
        try:
            self.proc = subprocess.Popen(
                argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                env=child_env(self.home, self.tmp), start_new_session=True)
        except OSError:
            if _ACTIVE_RECORDER is not None:
                _ACTIVE_RECORDER.note_process("relay-start", argv, started_at, None, "not-started", "child-launch-failed")
            raise SafeFailure("child-launch-failed") from None
        if _ACTIVE_RECORDER is not None:
            _ACTIVE_RECORDER.note_process("relay-start", argv, started_at, None, "owned-process-group", None)
        proc = self.proc
        self.reader = threading.Thread(target=self._read_ready, args=(proc,), daemon=True)
        self.reader.start()
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            if self.ready.is_set() and self._connects():
                return
            if proc.poll() is not None:
                break
            time.sleep(0.05)
        self.stop(force=True)
        raise SafeFailure("relay-readiness-timeout")

    def _read_ready(self, proc: subprocess.Popen[bytes]) -> None:
        assert proc.stdout is not None
        for line in iter(proc.stdout.readline, b""):
            if b"Hyphae relay listening on ws://127.0.0.1:" in line:
                self.ready.set()

    def _connects(self) -> bool:
        try:
            with socket.create_connection(("127.0.0.1", self.port), timeout=0.2):
                return True
        except OSError:
            return False

    def stop(self, force: bool = False) -> None:
        if self.proc is None:
            return
        proc = self.proc
        try:
            stop_owned_group(proc, force)
        except SafeFailure:
            if _ACTIVE_RECORDER is not None:
                _ACTIVE_RECORDER.note_cleanup("relay-process-group", "failed", pid=proc.pid, pgid=proc.pid)
            raise
        if proc.stdout:
            proc.stdout.close()
        if self.reader:
            self.reader.join(timeout=2)
            require(not self.reader.is_alive(), "relay-reader-cleanup-failed")
        self.proc = None
        self.reader = None
        if _ACTIVE_RECORDER is not None:
            _ACTIVE_RECORDER.note_cleanup("relay-process-group", "completed", pid=proc.pid, pgid=proc.pid)

    @property
    def url(self) -> str:
        return f"ws://127.0.0.1:{self.port}"


class Agent24d:
    def __init__(self, binary: Path, home: Path, tmp: Path, hyphae: Path):
        self.binary, self.home, self.tmp, self.hyphae = binary, home, tmp, hyphae
        self.proc: subprocess.Popen[bytes] | None = None
        self.state: dict[str, Any] | None = None

    def start(self, timeout: float) -> dict[str, Any]:
        require(self.proc is None, "agent24d-already-running")
        env = child_env(self.home, self.tmp, {
            "A24_HYPHAE_BIN": str(self.hyphae),
            "A24_COMM_PASSWORD_STORE": "memory",
        })
        argv = [str(self.binary), "serve", "--port", "0"]
        started_at = utc_now()
        try:
            self.proc = subprocess.Popen(argv, stdin=subprocess.DEVNULL,
                                         stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, env=env,
                                         start_new_session=True)
        except OSError:
            if _ACTIVE_RECORDER is not None:
                _ACTIVE_RECORDER.note_process("agent24d-start", argv, started_at, None, "not-started", "child-launch-failed")
            raise SafeFailure("child-launch-failed") from None
        if _ACTIVE_RECORDER is not None:
            _ACTIVE_RECORDER.note_process("agent24d-start", argv, started_at, None, "owned-process-group", None)
        state_path = self.home / ".agent24" / "daemon.json"
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.proc.poll() is not None:
                raise SafeFailure("agent24d-exited-before-ready")
            try:
                state = json.loads(state_path.read_text(encoding="utf-8"))
            except (OSError, UnicodeError, json.JSONDecodeError):
                time.sleep(0.05)
                continue
            if state.get("pid") == self.proc.pid and int(state.get("port", 0)) > 0:
                self.state = state
                if http_json(f"http://127.0.0.1:{state['port']}/api/v1/health", None)[0] < 500:
                    return state
            time.sleep(0.05)
        self.stop(force=True)
        raise SafeFailure("agent24d-readiness-timeout")

    def stop(self, force: bool = False, sig: int = signal.SIGTERM) -> None:
        if self.proc is None:
            return
        proc = self.proc
        try:
            os.killpg(proc.pid, sig)
        except ProcessLookupError:
            pass
        except OSError:
            if _ACTIVE_RECORDER is not None:
                _ACTIVE_RECORDER.note_cleanup("agent24d-process-group", "failed", pid=proc.pid, pgid=proc.pid)
            raise SafeFailure("agent24d-cleanup-failed") from None
        timed_out = False
        try:
            proc.wait(timeout=3 if sig == signal.SIGTERM else 1)
        except subprocess.TimeoutExpired:
            timed_out = True
        if force and sig != signal.SIGKILL:
            group_exists = False
            try:
                os.killpg(proc.pid, 0)
                group_exists = True
            except ProcessLookupError:
                pass
            except OSError:
                if _ACTIVE_RECORDER is not None:
                    _ACTIVE_RECORDER.note_cleanup("agent24d-process-group", "failed", pid=proc.pid, pgid=proc.pid)
                raise SafeFailure("agent24d-cleanup-failed") from None
            if timed_out or group_exists:
                try:
                    os.killpg(proc.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                except OSError:
                    if _ACTIVE_RECORDER is not None:
                        _ACTIVE_RECORDER.note_cleanup("agent24d-process-group", "failed", pid=proc.pid, pgid=proc.pid)
                    raise SafeFailure("agent24d-cleanup-failed") from None
                try:
                    proc.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    if _ACTIVE_RECORDER is not None:
                        _ACTIVE_RECORDER.note_cleanup("agent24d-process-group", "failed", pid=proc.pid, pgid=proc.pid)
                    raise SafeFailure("agent24d-cleanup-failed") from None
        if proc.poll() is None:
            if _ACTIVE_RECORDER is not None:
                _ACTIVE_RECORDER.note_cleanup("agent24d-process-group", "failed", pid=proc.pid, pgid=proc.pid)
            raise SafeFailure("agent24d-cleanup-failed")
        if proc.stdout:
            proc.stdout.close()
        if _ACTIVE_RECORDER is not None:
            _ACTIVE_RECORDER.note_cleanup("agent24d-process-group", "completed", pid=proc.pid, pgid=proc.pid,
                                          signal=signal.Signals(sig).name)
            _ACTIVE_RECORDER.note_process("agent24d-stop", [str(self.binary), "serve", "--port", "0"],
                                          utc_now(), proc.returncode, "completed", None)
        self.proc = None
        self.state = None


def http_json(url: str, token: str | None, body: dict[str, Any] | None = None,
              method: str | None = None, timeout: float = 8.0) -> tuple[int, Any]:
    started_at = utc_now()
    started_clock = time.monotonic()
    headers = {"Accept": "application/json"}
    data = None
    if token:
        headers["Authorization"] = "Bearer " + token
    if body is not None:
        data = json.dumps(body, separators=(",", ":")).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    status: int | None = None
    result: Any = None
    failure: str | None = None
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            payload = response.read(4 * 1024 * 1024 + 1)
            require(len(payload) <= 4 * 1024 * 1024, "http-response-too-large")
            status = response.status
            result = parse_json(payload, "http-json-invalid")
    except urllib.error.HTTPError as error:
        raw = error.read(4 * 1024 * 1024 + 1)
        status = error.code
        try:
            result = json.loads(raw.decode("utf-8")) if raw else None
        except (UnicodeError, json.JSONDecodeError):
            result = None
    except (urllib.error.URLError, TimeoutError, OSError):
        failure = "http-request-failed"
        raise SafeFailure("http-request-failed") from None
    finally:
        if _ACTIVE_RECORDER is not None:
            _ACTIVE_RECORDER.note_command({
                "stage": _ACTIVE_RECORDER.current_stage,
                "command": ["HTTP", method or ("POST" if body is not None else "GET"),
                            urllib.parse.urlsplit(url).path],
                "exit": status,
                "started_at": started_at,
                "ended_at": utc_now(),
                "duration_ms": round((time.monotonic() - started_clock) * 1000),
                "cleanup": "not-applicable",
                "request_body_redacted": body is not None,
                "response_redacted": True,
                "failure": failure,
            })
    return int(status or 0), result


def bearer_state(host_home: Path, process: Agent24d) -> tuple[str, str, int]:
    require(process.state is not None, "agent24d-state-missing")
    state = json.loads((host_home / ".agent24" / "daemon.json").read_text(encoding="utf-8"))
    require(state.get("pid") == process.proc.pid, "agent24d-pid-mismatch")
    require(state.get("auth_mode", "legacy_single_token") == "legacy_single_token", "agent24d-auth-mode-unsupported")
    token = state.get("token")
    require(isinstance(token, str) and token, "agent24d-token-missing")
    base = f"http://127.0.0.1:{state['port']}"
    return token, base, int(state["port"])


def validate_unlock_response(status: int, envelope: Any) -> dict[str, Any]:
    if status == 404:
        raise Blocked("comm-unlock-route-not-implemented")
    require(status == 200, "comm-unlock-http-rejected")
    data = json_data(envelope, "comm-unlock-envelope")
    require(isinstance(data, dict), "comm-unlock-data-shape")
    require(data.get("unlocked") is True, "comm-unlock-not-unlocked")
    require(data.get("remembered") is False, "comm-unlock-password-was-remembered")
    return data


def unlock_agent24d(host_home: Path, process: Agent24d, password: str) -> dict[str, Any]:
    # Read daemon.json for every instance: tokens/ports are process-generation scoped.
    token, base, _ = bearer_state(host_home, process)
    status, envelope = http_json(base + UNLOCK_ROUTE, token,
                                 {"password": password, "remember": False}, "POST")
    return validate_unlock_response(status, envelope)


def json_data(value: Any, label: str) -> Any:
    require(isinstance(value, dict) and value.get("ok") is True and "data" in value, label)
    return value["data"]


def rows_for(data: Any, event_id: str) -> list[dict[str, Any]]:
    require(isinstance(data, list), "history-shape")
    return [row for row in data if isinstance(row, dict) and row.get("id", row.get("event_id")) == event_id]


def validate_offline_l1(data: Any) -> str:
    require(isinstance(data, dict), "offline-send-shape")
    require(data.get("published_to") == 0, "offline-send-published-to-not-zero")
    require(data.get("queued_for_retry") is True, "offline-send-not-queued")
    require(data.get("layer") == "L1", "offline-send-layer-not-l1")
    event_id = data.get("event_id")
    require(isinstance(event_id, str) and len(event_id) == 64, "offline-send-event-id-invalid")
    return event_id


def outbox_entries_for(data: Any, event_id: str) -> list[dict[str, Any]]:
    require(isinstance(data, list), "outbox-shape")
    return [row for row in data if isinstance(row, dict) and row.get("id", row.get("event_id")) == event_id]


def stage(name: str, evidence: list[dict[str, Any]], **fields: Any) -> None:
    row = {"stage": name, **fields}
    evidence.append(row)
    if _ACTIVE_RECORDER is not None:
        _ACTIVE_RECORDER.current_stage = name
    print("PASS " + name, flush=True)


def block_stage(name: str, evidence: list[dict[str, Any]], **fields: Any) -> None:
    evidence.append({"stage": name, "result": "BLOCKED", **fields})
    set_stage(name)
    print("BLOCKED " + name, flush=True)


def set_stage(name: str) -> None:
    if _ACTIVE_RECORDER is not None:
        _ACTIVE_RECORDER.current_stage = name


def ephemeral_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def configure_direct_relays_and_peer_contact(hyphae_bin: Path, managed_home: Path, peer_home: Path,
                                            tmp: Path, relay_url: str, alice: str,
                                            timeout: float) -> None:
    # Both isolated Hyphae homes share the relay, but only seed the peer's
    # contact directly. The managed-side bob contact is created via Agent24
    # after unlock, exercising the real Agent24 write path without duplication.
    for home in (managed_home, peer_home):
        invoke_hyphae(hyphae_bin, home, tmp, ["relay", "set", "--relay", relay_url], timeout=timeout)
    invoke_hyphae(hyphae_bin, peer_home, tmp,
                  ["contact", "add", "--nickname", "alice", "--npub", alice], timeout=timeout)


def add_managed_agent24_contact(agent24_bin: Path, host_home: Path, tmp: Path, bob: str) -> Any:
    result = invoke_agent24(agent24_bin, host_home, tmp, ["comm", "contact", "add", "bob", bob])
    require(isinstance(result, dict), "agent24-contact-add-shape")
    return result


def run(args: argparse.Namespace) -> None:
    global _ACTIVE_RECORDER
    args.output_dir.mkdir(parents=True, exist_ok=True)
    run_dir = args.output_dir / ("agent24-joint-" + time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())
                                + "-" + os.urandom(4).hex())
    run_dir.mkdir(mode=0o700)
    evidence: list[dict[str, Any]] = []
    recorder = RunRecorder()
    _ACTIVE_RECORDER = recorder
    records: dict[str, Any] = {
        "schema": "agent24-hyphae-joint-evidence/1",
        "hyphae_sha": args.hyphae_sha,
        "agent24_sha": args.agent24_sha,
        "production_lock_sha256": args.expected_lock_sha256,
        "platform": host_platform(),
        "result": "RUNNING",
        "started_at": utc_now(),
        "assertions": evidence,
        "executions": recorder.executions,
        "cleanup": recorder.cleanup,
    }
    try:
        hashes = check_inputs(args)
        records["binaries"] = hashes
        execute(args, hashes, run_dir, records, evidence)
        records["result"] = "PASS"
    except Blocked as error:
        records["result"] = "BLOCKED"
        records["failure"] = str(error)
        records["blocked_at_stage"] = recorder.current_stage
        raise
    except SafeFailure as error:
        records["result"] = "FAIL"
        records["failure"] = str(error)
        records["failed_at_stage"] = recorder.current_stage
        raise
    except KeyboardInterrupt:
        records["result"] = "FAIL"
        records["failure"] = "interrupted"
        records["failed_at_stage"] = recorder.current_stage
        raise
    except Exception:
        records["result"] = "FAIL"
        records["failure"] = "unexpected-harness-error"
        records["failed_at_stage"] = recorder.current_stage
        raise SafeFailure("unexpected-harness-error") from None
    finally:
        records["ended_at"] = utc_now()
        records["evidence_path"] = str(run_dir / "evidence.json")
        try:
            write_evidence(run_dir / "evidence.json", records)
        except OSError:
            records["result"] = "FAIL"
            records["failure"] = "evidence-write-failed"
            raise SafeFailure("evidence-write-failed") from None
        finally:
            _ACTIVE_RECORDER = None
        print("EVIDENCE " + str(run_dir / "evidence.json"), flush=True)


def write_evidence(path: Path, records: dict[str, Any]) -> None:
    payload = json.dumps(records, ensure_ascii=False, sort_keys=True, indent=2) + "\n"
    temporary = path.with_name(path.name + ".tmp")
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            stream.write(payload)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    except BaseException:
        temporary.unlink(missing_ok=True)
        raise


def execute(args: argparse.Namespace, hashes: dict[str, str], run_dir: Path,
            records: dict[str, Any], evidence: list[dict[str, Any]]) -> None:
    with tempfile.TemporaryDirectory(prefix="agent24-joint-") as tmpname:
        tmp = Path(tmpname)
        host_home, peer_home, relay_home, relay_data = (tmp / name for name in ("host-home", "peer-home", "relay-home", "relay-data"))
        for path in (host_home, peer_home, relay_home, relay_data):
            path.mkdir(mode=0o700)
        managed_home = host_home / ".agent24" / "comm" / "hyphae-home"
        managed_home.mkdir(parents=True, mode=0o700)
        peer_password = "joint-peer-" + os.urandom(20).hex()
        managed_password = "joint-managed-" + os.urandom(20).hex()
        relay = Relay(args.relay_bin, relay_data, relay_home, tmp, ephemeral_port())
        daemon = Agent24d(args.agent24d_bin, host_home, tmp, args.hyphae_bin)
        records["binaries"] = hashes
        try:
            set_stage("isolated-identity-and-relay-setup")
            # Seed a known encrypted managed keystore so memory-store loss is
            # recoverable through the ordinary password-unlock HTTP contract.
            create_host = invoke_hyphae(args.hyphae_bin, managed_home, tmp,
                                        ["identity", "create", "--nickname", "alice", "--default"],
                                        managed_password, args.timeout)
            create_peer = invoke_hyphae(args.hyphae_bin, peer_home, tmp,
                                        ["identity", "create", "--nickname", "bob", "--default"],
                                        peer_password, args.timeout)
            alice, bob = create_host.get("npub"), create_peer.get("npub")
            require(isinstance(alice, str) and alice.startswith("npub1") and isinstance(bob, str) and bob.startswith("npub1"), "identity-create-output")
            relay.start(args.readiness_timeout)
            configure_direct_relays_and_peer_contact(
                args.hyphae_bin, managed_home, peer_home, tmp, relay.url, alice, args.timeout)
            stage("verified-artifacts-isolated-homes-real-relay", evidence, relay=relay.url)

            daemon.start(args.readiness_timeout)
            token, base, _ = bearer_state(host_home, daemon)
            set_stage("memory-password-unlock-initial")
            try:
                initial_unlock = unlock_agent24d(host_home, daemon, managed_password)
            except Blocked:
                block_stage("memory-password-unlock-initial", evidence, route=UNLOCK_ROUTE, http_status=404)
                raise
            stage("memory-password-unlock-initial", evidence,
                  unlocked=initial_unlock["unlocked"], remembered=initial_unlock["remembered"])
            set_stage("authenticated-http-positive-negative-controls")
            # Authentication positive and negative controls are both hard gates.
            unauth_status, _ = http_json(base + "/api/v1/comm/relay", None)
            require(unauth_status in (401, 403), "unauthenticated-http-not-rejected")
            put_status, put_body = http_json(base + "/api/v1/comm/relay", token,
                                             {"relays": [relay.url]}, "PUT")
            require(put_status == 200, "authenticated-http-relay-set-rejected")
            http_relays = json_data(put_body, "authenticated-http-envelope")
            auth_status, auth_body = http_json(base + "/api/v1/comm/relay", token)
            require(auth_status == 200, "authenticated-http-rejected")
            http_relays = json_data(auth_body, "authenticated-http-envelope")
            cli_relays = invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "relay", "list"])
            require(isinstance(cli_relays, dict), "agent24-relay-list-shape")
            require(http_relays == cli_relays and http_relays.get("relays") == [relay.url],
                    "http-cli-relay-config-disagrees")
            stage("authenticated-http-and-cli-positive-control", evidence,
                  unauthenticated_status=unauth_status, authenticated_status=auth_status)

            set_stage("agent24-cli-unlocked-independent-reads")
            identities = invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "identity", "list"])
            require(isinstance(identities, list) and any(row.get("nickname") == "alice" for row in identities if isinstance(row, dict)),
                    "agent24-identity-seed-not-visible")
            add_managed_agent24_contact(args.agent24_bin, host_home, tmp, bob)

            set_stage("hyphae-real-relay-pre-unlock-positive-control")
            inbound_body = "joint-hyphae-preunlock-" + os.urandom(12).hex()
            inbound = invoke_hyphae(args.hyphae_bin, peer_home, tmp,
                                    ["agent", "msg", "--from", "bob", "--to", "alice", "--content", inbound_body],
                                    peer_password, args.timeout)
            inbound_id = inbound.get("event_id")
            require(isinstance(inbound_id, str) and len(inbound_id) == 64, "preunlock-inbound-event-id")
            received = invoke_hyphae(args.hyphae_bin, managed_home, tmp,
                                    ["agent", "inbox", "--as", "alice"], managed_password, args.timeout)
            require(isinstance(received, list) and sum(
                row.get("event_id") == inbound_id and row.get("content") == inbound_body
                for row in received if isinstance(row, dict)
            ) == 1, "preunlock-hyphae-relay-positive-control")
            stage("hyphae-real-relay-pre-unlock-positive-control", evidence, event_id=inbound_id)

            # Dependent acceptance proceeds only after the real memory store
            # accepted this isolated keystore password above.
            create_second = invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "identity", "create", "carol"])
            require(isinstance(create_second, dict), "agent24-second-identity-create-shape")
            to_peer = "joint-a-to-b-" + os.urandom(12).hex()
            sent_ab = invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "send", "bob", to_peer, "--from", "alice"])
            id_ab = sent_ab.get("event_id")
            require(isinstance(id_ab, str) and len(id_ab) == 64, "agent24-send-event-id")
            recv_b = invoke_hyphae(args.hyphae_bin, peer_home, tmp,
                                   ["agent", "inbox", "--as", "bob"], peer_password, args.timeout)
            require(isinstance(recv_b, list) and sum(row.get("event_id") == id_ab and row.get("content") == to_peer for row in recv_b if isinstance(row, dict)) == 1,
                    "agent24-to-hyphae-body-or-event-id")

            to_alice = "joint-b-to-a-" + os.urandom(12).hex()
            sent_ba = invoke_hyphae(args.hyphae_bin, peer_home, tmp,
                                    ["agent", "msg", "--from", "bob", "--to", "alice", "--content", to_alice],
                                    peer_password, args.timeout)
            id_ba = sent_ba.get("event_id")
            require(isinstance(id_ba, str) and len(id_ba) == 64, "hyphae-send-event-id")
            invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "pull", "--as", "alice"])
            history_a = invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "history", "--as", "alice", "--limit", "200"])
            matches_a = [row for row in history_a if isinstance(row, dict) and row.get("id", row.get("event_id")) == id_ba and (row.get("plaintext", row.get("content")) == to_alice)]
            require(len(matches_a) == 1, "hyphae-to-agent24-body-or-event-id")
            stage("bidirectional-event-id-and-body", evidence, a_to_b_event_id=id_ab, b_to_a_event_id=id_ba)

            # Offline retry: the stored outbox id must be the only delivered item.
            relay.stop()
            offline_body = "joint-offline-" + os.urandom(12).hex()
            set_stage("offline-send-l1-and-outbox")
            offline = invoke_agent24(args.agent24_bin, host_home, tmp,
                                     ["comm", "send", "bob", offline_body, "--from", "alice"])
            offline_id = validate_offline_l1(offline)
            outbox_before_retry = invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "outbox", "list"])
            require(isinstance(outbox_before_retry, list), "outbox-before-retry-shape")
            pending_before = outbox_entries_for(outbox_before_retry, offline_id)
            require(len(pending_before) == 1 and pending_before[0].get("status") == "pending",
                    "offline-event-not-pending")
            relay.start(args.readiness_timeout)
            retry = invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "outbox", "retry", offline_id])
            require(retry.get("event_id") == offline_id and retry.get("sent") is True,
                    "retry-changed-event-id")
            outbox_after_retry = invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "outbox", "list"])
            require(isinstance(outbox_after_retry, list), "outbox-after-retry-shape")
            pending_after = outbox_entries_for(outbox_after_retry, offline_id)
            require(not pending_after, "retry-event-still-pending")
            invoke_hyphae(args.hyphae_bin, peer_home, tmp, ["agent", "inbox", "--as", "bob"], peer_password, args.timeout)
            peer_history = invoke_hyphae(args.hyphae_bin, peer_home, tmp, ["history", "inbox", "--as", "bob", "--limit", "200"])
            retry_rows = rows_for(peer_history, offline_id)
            require(len(retry_rows) == 1 and retry_rows[0].get("plaintext") == offline_body, "retry-peer-exactly-one")
            stage("offline-retry-same-id-exactly-one", evidence, event_id=offline_id,
                  layer="L1", published_to=0, queued_for_retry=True, pending_after_retry=0)

            # Let the managed receiver daemon prove 125 offline messages are
            # recovered once, then prove a real process restart adds zero.
            invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "daemon", "stop"])
            history_before_batch = invoke_agent24(args.agent24_bin, host_home, tmp,
                                                   ["comm", "history", "--as", "alice", "--limit", "200"])
            before_ids = {str(row.get("id", row.get("event_id"))) for row in history_before_batch if isinstance(row, dict)}
            batch_ids: list[str] = []
            for index in range(125):
                body = f"joint-backlog-{index:03d}-" + os.urandom(5).hex()
                result = invoke_hyphae(args.hyphae_bin, peer_home, tmp,
                                       ["agent", "msg", "--from", "bob", "--to", "alice", "--content", body],
                                       peer_password, args.timeout)
                event_id = result.get("event_id")
                require(isinstance(event_id, str) and len(event_id) == 64, "backlog-event-id")
                batch_ids.append(event_id)
            invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "daemon", "start"])
            wait_process_running(args.agent24_bin, host_home, tmp, args.deadline)
            wait_until(args.deadline, lambda: history_has_ids(args.agent24_bin, host_home, tmp, batch_ids))
            batch_history = invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "history", "--as", "alice", "--limit", "200"])
            require(all(len(rows_for(batch_history, event_id)) == 1 for event_id in batch_ids), "backlog-not-exactly-125")
            recovered_ids = {str(row.get("id", row.get("event_id"))) for row in batch_history if isinstance(row, dict)}
            require(recovered_ids - before_ids == set(batch_ids), "backlog-recovery-delta-not-125")
            invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "daemon", "stop"])
            invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "daemon", "start"])
            wait_process_running(args.agent24_bin, host_home, tmp, args.deadline)
            sentinel_body = "joint-daemon-rescan-sentinel-" + os.urandom(10).hex()
            sentinel = invoke_hyphae(args.hyphae_bin, peer_home, tmp,
                                     ["agent", "msg", "--from", "bob", "--to", "alice", "--content", sentinel_body],
                                     peer_password, args.timeout)
            sentinel_id = sentinel.get("event_id")
            require(isinstance(sentinel_id, str) and len(sentinel_id) == 64 and sentinel.get("published_to", 0) > 0,
                    "daemon-restart-sentinel-send")
            wait_until(args.deadline, lambda: history_has_id(args.agent24_bin, host_home, tmp, sentinel_id))
            batch_after_restart = invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "history", "--as", "alice", "--limit", "200"])
            require(all(len(rows_for(batch_after_restart, event_id)) == 1 for event_id in batch_ids), "backlog-duplicate-after-restart")
            after_ids = {str(row.get("id", row.get("event_id"))) for row in batch_after_restart if isinstance(row, dict)}
            require(after_ids == recovered_ids | {sentinel_id}, "backlog-restart-added-events")
            stage("daemon-offline-125-recovery-and-restart-zero", evidence, sent=125, recovered=125,
                  after_restart_new=0, readiness_sentinel_event_id=sentinel_id)

            # Configuration change must cause the supervised Hyphae daemon to
            # restart into a new generation (COMM-5b status wire contract).
            set_stage("configuration-restart-generation")
            before_data = invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "daemon", "status"])
            before = daemon_process_status(before_data, "daemon-status-before")
            require(before["state"] == "running", "daemon-not-running-before-config-change")
            invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "identity", "use", "carol"])
            after = wait_generation(args.agent24_bin, host_home, tmp, args.deadline, before)
            require(after["generation"] != before["generation"], "config-restart-generation-unchanged")
            stage("config-restart-generation", evidence, generation_before=before["generation"],
                  generation_after=after["generation"], failures_before=before["consecutive_failures"],
                  failures_after=after["consecutive_failures"])

            # Signal exercise: SIGTERM stops the owned host daemon; SIGKILL is
            # applied only to the newly-created runner-owned process group.
            invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "daemon", "start"])
            daemon.stop(sig=signal.SIGTERM)
            require(not managed_daemon_alive(host_home), "sigterm-left-managed-hyphae-daemon")
            daemon.start(args.readiness_timeout)
            set_stage("memory-password-unlock-after-agent24d-restart")
            try:
                restarted_unlock = unlock_agent24d(host_home, daemon, managed_password)
            except Blocked:
                block_stage("memory-password-unlock-after-agent24d-restart", evidence,
                            route=UNLOCK_ROUTE, http_status=404)
                raise
            stage("memory-password-unlock-after-agent24d-restart", evidence,
                  unlocked=restarted_unlock["unlocked"], remembered=restarted_unlock["remembered"])
            invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "daemon", "start"])
            wait_process_running(args.agent24_bin, host_home, tmp, args.deadline)
            managed_record = read_managed_pid_record(host_home, hashes["hyphae"])
            kill_pid = daemon.proc.pid
            daemon.stop(force=True, sig=signal.SIGKILL)
            require(not process_alive(kill_pid), "sigkill-owned-agent24d-survived")
            orphan_cleaned = cleanup_managed_group(host_home, hashes["hyphae"], managed_record)
            require(orphan_cleaned, "sigkill-orphan-managed-group-cleanup-failed")
            stage("sigterm-sigkill-and-process-group-cleanup", evidence, term=True, kill=True,
                  agent24d_pid=kill_pid, managed_hyphae_pgid=managed_record["pgid"])
        finally:
            cleanup_errors: list[str] = []
            if daemon.proc is not None:
                # A CLI stop is a graceful request; its failure is recorded,
                # while exact owned-process cleanup below remains mandatory.
                try:
                    invoke_agent24(args.agent24_bin, host_home, tmp, ["comm", "daemon", "stop"], timeout=8)
                    if _ACTIVE_RECORDER is not None:
                        _ACTIVE_RECORDER.note_cleanup("supervised-hyphae-stop", "completed")
                except SafeFailure:
                    if _ACTIVE_RECORDER is not None:
                        _ACTIVE_RECORDER.note_cleanup("supervised-hyphae-stop", "fallback-required")
                try:
                    daemon.stop(force=True)
                except SafeFailure:
                    cleanup_errors.append("agent24d-process-group-cleanup-failed")
            pid_path = host_home / ".agent24" / "comm" / "hyphae-daemon.pid"
            if pid_path.exists():
                try:
                    record = read_managed_pid_record(host_home, hashes["hyphae"])
                    cleanup_managed_group(host_home, hashes["hyphae"], record)
                except SafeFailure as error:
                    cleanup_errors.append(str(error))
                    if _ACTIVE_RECORDER is not None:
                        _ACTIVE_RECORDER.note_cleanup("managed-hyphae-process-group", "failed", reason=str(error))
            try:
                relay.stop(force=True)
            except SafeFailure as error:
                cleanup_errors.append(str(error))
            if cleanup_errors:
                records["cleanup_failure"] = cleanup_errors
                raise SafeFailure("cleanup-failed") from None


def process_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False


def read_managed_pid_record(host_home: Path, expected_hash: str) -> dict[str, Any]:
    path = host_home / ".agent24" / "comm" / "hyphae-daemon.pid"
    try:
        record = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError):
        raise SafeFailure("managed-hyphae-pid-record-invalid") from None
    require(isinstance(record, dict), "managed-hyphae-pid-record-invalid")
    pid, pgid = record.get("pid"), record.get("pgid")
    require(type(pid) is int and pid > 1 and pgid == pid, "managed-hyphae-pid-pgid-invalid")
    require(record.get("bin_sha256") == expected_hash, "managed-hyphae-binary-hash-mismatch")
    start_marker = record.get("start_marker")
    require(isinstance(start_marker, str) and start_marker.strip(), "managed-hyphae-start-marker-missing")
    try:
        require(os.getpgid(pid) == pgid, "managed-hyphae-pgid-mismatch")
    except ProcessLookupError:
        raise SafeFailure("managed-hyphae-process-not-live") from None
    current_marker = process_start_marker(pid)
    require(current_marker == start_marker.strip(), "managed-hyphae-start-marker-mismatch")
    return record


def process_start_marker(pid: int) -> str:
    ps_bin = next((path for path in ("/bin/ps", "/usr/bin/ps") if Path(path).is_absolute() and Path(path).exists()), None)
    require(ps_bin is not None, "process-start-marker-ps-missing")
    try:
        result = subprocess.run([ps_bin, "-o", "lstart=", "-p", str(pid)], check=True,
                                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                text=True, timeout=2, env={"LC_ALL": "C", "TZ": "UTC"})
    except (OSError, subprocess.SubprocessError):
        raise SafeFailure("process-start-marker-unavailable") from None
    marker = result.stdout.strip()
    require(bool(marker), "process-start-marker-unavailable")
    return marker


def process_group_alive(pgid: int) -> bool:
    try:
        os.killpg(pgid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        # EPERM proves a process group exists, even if this user cannot signal it.
        return True


def attest_managed_identity(host_home: Path, expected_hash: str,
                            expected_record: dict[str, Any]) -> dict[str, Any]:
    current = read_managed_pid_record(host_home, expected_hash)
    for key in ("pid", "pgid", "start_marker", "bin_sha256"):
        require(current.get(key) == expected_record.get(key), "managed-hyphae-pid-record-changed")
    return current


def require_pidfile_unchanged(host_home: Path, expected_hash: str,
                             expected_record: dict[str, Any]) -> None:
    path = host_home / ".agent24" / "comm" / "hyphae-daemon.pid"
    try:
        current = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError):
        raise SafeFailure("managed-hyphae-pid-record-invalid") from None
    for key, expected in (("pid", expected_record.get("pid")), ("pgid", expected_record.get("pgid")),
                          ("start_marker", expected_record.get("start_marker")),
                          ("bin_sha256", expected_hash)):
        require(current.get(key) == expected, "managed-hyphae-pid-record-changed")


def cleanup_managed_group(host_home: Path, expected_hash: str, record: dict[str, Any],
                          grace_seconds: float = 3.0) -> bool:
    # Attest the exact pidfile identity before TERM and again before any KILL
    # escalation. Never infer whole-group cleanup from the leader alone.
    current = attest_managed_identity(host_home, expected_hash, record)
    pgid = int(current["pgid"])
    try:
        os.killpg(pgid, signal.SIGTERM)
    except ProcessLookupError:
        if not process_group_alive(pgid):
            require_pidfile_unchanged(host_home, expected_hash, record)
            (host_home / ".agent24" / "comm" / "hyphae-daemon.pid").unlink()
            return True
    except OSError:
        if _ACTIVE_RECORDER is not None:
            _ACTIVE_RECORDER.note_cleanup("managed-hyphae-process-group", "failed", pid=current["pid"], pgid=pgid)
        raise SafeFailure("managed-hyphae-process-group-cleanup-failed") from None
    end = time.monotonic() + grace_seconds
    while time.monotonic() < end and process_group_alive(pgid):
        time.sleep(0.05)
    if process_group_alive(pgid):
        # A changed/missing leader marker means we cannot safely signal this
        # numeric PGID: the original leader may have exited and its PID reused.
        attest_managed_identity(host_home, expected_hash, record)
        try:
            os.killpg(pgid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        except OSError:
            if _ACTIVE_RECORDER is not None:
                _ACTIVE_RECORDER.note_cleanup("managed-hyphae-process-group", "failed", pid=current["pid"], pgid=pgid)
            raise SafeFailure("managed-hyphae-process-group-cleanup-failed") from None
    end = time.monotonic() + grace_seconds
    while time.monotonic() < end and process_group_alive(pgid):
        time.sleep(0.05)
    stopped = not process_group_alive(pgid)
    if _ACTIVE_RECORDER is not None:
        _ACTIVE_RECORDER.note_cleanup("managed-hyphae-process-group", "completed" if stopped else "failed",
                                      pid=current["pid"], pgid=current["pgid"], signal_escalated=not stopped)
    require(stopped, "managed-hyphae-process-group-cleanup-failed")
    require_pidfile_unchanged(host_home, expected_hash, record)
    (host_home / ".agent24" / "comm" / "hyphae-daemon.pid").unlink()
    return True


def managed_daemon_alive(host_home: Path) -> bool:
    path = host_home / ".agent24" / "comm" / "hyphae-daemon.pid"
    try:
        record = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError):
        return False
    return type(record.get("pid")) is int and process_alive(record["pid"])


def history_has_ids(cli: Path, home: Path, tmp: Path, ids: list[str]) -> bool:
    try:
        history = invoke_agent24(cli, home, tmp, ["comm", "history", "--as", "alice", "--limit", "200"], timeout=10)
    except SafeFailure:
        return False
    if not isinstance(history, list):
        return False
    seen = {str(row.get("id", row.get("event_id"))) for row in history if isinstance(row, dict)}
    return set(ids).issubset(seen)


def history_has_id(cli: Path, home: Path, tmp: Path, event_id: str) -> bool:
    try:
        history = invoke_agent24(cli, home, tmp, ["comm", "history", "--as", "alice", "--limit", "200"], timeout=10)
    except SafeFailure:
        return False
    return isinstance(history, list) and len(rows_for(history, event_id)) == 1


def wait_until(deadline: float, predicate: Any) -> None:
    end = time.monotonic() + deadline
    while time.monotonic() < end:
        if predicate():
            return
        time.sleep(0.25)
    raise SafeFailure("bounded-wait-expired")


def daemon_process_status(data: Any, label: str) -> dict[str, Any]:
    require(isinstance(data, dict) and isinstance(data.get("process"), dict), label + "-shape")
    process = data["process"]
    require(type(process.get("generation")) is int, label + "-generation-shape")
    require(type(process.get("consecutive_failures")) is int, label + "-failed-count-shape")
    require(isinstance(process.get("state"), str), label + "-state-shape")
    return process


def wait_process_running(cli: Path, home: Path, tmp: Path, deadline: float) -> dict[str, Any]:
    end = time.monotonic() + deadline
    last: dict[str, Any] | None = None
    while time.monotonic() < end:
        try:
            data = invoke_agent24(cli, home, tmp, ["comm", "daemon", "status"], timeout=10)
        except SafeFailure:
            time.sleep(0.25)
            continue
        last = daemon_process_status(data, "daemon-status-running")
        if last["state"] == "running":
            return last
        time.sleep(0.25)
    raise SafeFailure("daemon-process-running-timeout")


def wait_generation(cli: Path, home: Path, tmp: Path, deadline: float,
                    previous: dict[str, Any]) -> dict[str, Any]:
    old_generation = previous["generation"]
    old_failures = previous["consecutive_failures"]
    end = time.monotonic() + deadline
    while time.monotonic() < end:
        try:
            data = invoke_agent24(cli, home, tmp, ["comm", "daemon", "status"], timeout=10)
        except SafeFailure:
            time.sleep(0.25)
            continue
        process = daemon_process_status(data, "daemon-status-after")
        if process["state"] == "running" and process["generation"] != old_generation:
            require(process["consecutive_failures"] == old_failures, "config-restart-failed-count-changed")
            return process
        time.sleep(0.25)
    raise SafeFailure("config-restart-generation-timeout")


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description=__doc__)
    for name in ("hyphae", "agent24", "agent24d", "relay"):
        p.add_argument(f"--{name}-bin", type=Path, required=True, help=f"prebuilt local {name} executable")
        p.add_argument(f"--expected-{name}-sha256", required=True, help=f"exact expected {name} binary SHA-256")
    p.add_argument("--hyphae-sha", required=True, help="40-character Hyphae source commit SHA")
    p.add_argument("--agent24-sha", required=True, help="40-character Agent24 source commit SHA")
    p.add_argument("--lock", type=Path, required=True, help="production Hyphae lock JSON")
    p.add_argument("--expected-lock-sha256", required=True, help="exact SHA-256 of production lock")
    p.add_argument("--output-dir", type=Path, required=True, help="directory where a new private evidence folder is created")
    p.add_argument("--timeout", type=float, default=30.0, help="per CLI invocation timeout")
    p.add_argument("--readiness-timeout", type=float, default=15.0, help="bounded relay/agent24d startup timeout")
    p.add_argument("--deadline", type=float, default=45.0, help="bounded daemon recovery/configuration deadline")
    return p


def main(argv: list[str] | None = None) -> int:
    args = parser().parse_args(argv)
    try:
        require(math.isfinite(args.timeout) and args.timeout > 0
                and math.isfinite(args.readiness_timeout) and args.readiness_timeout > 0
                and math.isfinite(args.deadline) and args.deadline > 0,
                "invalid-timeout-configuration")
        run(args)
        return 0
    except Blocked as error:
        print(f"BLOCKED {error}", file=sys.stderr)
        return 2
    except SafeFailure as error:
        print(f"FAIL {error}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("FAIL interrupted; owned child process groups were cleaned", file=sys.stderr)
        return 130
    except Exception:
        print("FAIL unexpected harness error", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
