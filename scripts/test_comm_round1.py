#!/usr/bin/env python3
"""Strict, artifact-locked Hyphae round-one CLI/relay integration check."""
from __future__ import annotations

import argparse
import hashlib
import json
import math
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
from dataclasses import dataclass
from pathlib import Path
from typing import Any

BASELINE = "a4aa606eb81d5c040d94c51cdf94553e646d8674"
EXPECTED_BINARY_SHA = {
    "darwin-arm64": "f53c29b31d8ca5eb0124ced246bcff6610f048f18bc8dcc2de27f685dad8b221",
    "linux-x64": "042f6200f43c095cfcec16b31a136467a39b08c810819ab3c875df5cfe0164f6",
    "darwin-x64": None,
    "linux-arm64": None,
}
DARWIN_RELAY_SHA = "a012d86e549cbeb564d5a5932c54f9b3511c2434203846537096420c89f36aef"
JSON_FLAGS = ("--json",)


class SafeFailure(Exception):
    """A deliberately generic failure safe to print to a terminal/log."""


def fail(condition: bool, label: str) -> None:
    if not condition:
        raise SafeFailure(label)


def host_platform() -> str:
    system, machine = sys.platform, platform.machine().lower()
    if system == "darwin" and machine in ("arm64", "aarch64"):
        return "darwin-arm64"
    if system.startswith("linux") and machine in ("x86_64", "amd64"):
        return "linux-x64"
    return "other"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def load_lock(path: Path, expected_recipe: str, platform_name: str) -> tuple[dict[str, Any], str]:
    try:
        lock = json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=_unique_object)
    except (OSError, UnicodeError, json.JSONDecodeError):
        raise SafeFailure("lock-unreadable") from None
    fail(isinstance(lock, dict), "lock-invalid")
    fail(set(lock) == {"schema", "source_sha", "go", "recipe", "binaries"}, "lock-fields")
    fail(lock.get("schema") == 1 and type(lock.get("schema")) is int, "lock-schema")
    fail(lock.get("source_sha") == BASELINE, "lock-source")
    fail(lock.get("go") == "go1.26.4", "lock-go-version")
    recipe = lock.get("recipe")
    fail(isinstance(recipe, str) and 0 < len(recipe) <= 2000 and "\n" not in recipe and recipe == expected_recipe, "lock-recipe")
    binaries = lock.get("binaries")
    fail(isinstance(binaries, dict), "lock-binaries")
    fail(binaries == EXPECTED_BINARY_SHA, "lock-binary-map")
    expected_cli = EXPECTED_BINARY_SHA.get(platform_name)
    fail(isinstance(expected_cli, str) and len(expected_cli) == 64, "unsupported-platform")
    return lock, expected_cli


def _unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        fail(key not in result, "lock-duplicate-field")
        result[key] = value
    return result


def copy_verified_artifact(source: Path, destination: Path, expected_sha: str) -> str:
    """Copy from one opened regular-file descriptor, hash copied bytes, then execute only the copy."""
    fail(len(expected_sha) == 64 and all(c in "0123456789abcdef" for c in expected_sha), "artifact-hash-invalid")
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        fd = os.open(source, flags)
    except OSError:
        raise SafeFailure("artifact-open-failed") from None
    digest = hashlib.sha256()
    try:
        info = os.fstat(fd)
        fail(stat.S_ISREG(info.st_mode), "artifact-not-regular")
        with os.fdopen(fd, "rb", closefd=False) as src, destination.open("xb") as dst:
            for block in iter(lambda: src.read(1024 * 1024), b""):
                digest.update(block)
                dst.write(block)
            dst.flush()
            os.fsync(dst.fileno())
    except SafeFailure:
        destination.unlink(missing_ok=True)
        raise
    except OSError:
        destination.unlink(missing_ok=True)
        raise SafeFailure("artifact-copy-failed") from None
    finally:
        os.close(fd)
    copied_hash = digest.hexdigest()
    fail(copied_hash == expected_sha, "artifact-hash-mismatch")
    try:
        destination.chmod(0o700)
        fail(sha256_file(destination) == expected_sha, "private-copy-hash-mismatch")
    except OSError:
        destination.unlink(missing_ok=True)
        raise SafeFailure("private-copy-check-failed") from None
    return copied_hash


def child_env(home: Path, temp_root: Path) -> dict[str, str]:
    env = {
        "HOME": str(home),
        "TMPDIR": str(temp_root),
        "HYPHAE_OUTPUT": "json",
        "LC_ALL": "C",
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
    }
    if sys.platform == "darwin":
        env["TMP"] = str(temp_root)
        env["TEMP"] = str(temp_root)
    return env


def stop_process_group(proc: subprocess.Popen[bytes], force: bool = False) -> None:
    if getattr(proc, "_comm_round1_group_cleaned", False):
        return
    # The parent may have exited while a descendant still owns our pipes.
    # Since the process was created in an owned session, clean that group once.
    proc._comm_round1_group_cleaned = True
    try:
        os.killpg(proc.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    if force:
        # The leader has not been waited/reaped yet, so its session ID cannot
        # have been recycled. Escalate the entire owned group on CLI timeout.
        time.sleep(0.05)
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except (ProcessLookupError, PermissionError):
            # Managed sandboxes may reject group SIGKILL; waiting and draining
            # below remain bounded and produce a safe cleanup failure if needed.
            pass
    try:
        proc.wait(timeout=2)
    except subprocess.TimeoutExpired:
        # Escalate only against the direct child. Group-wide SIGKILL can be
        # denied by managed sandboxes; pipe draining below remains bounded.
        proc.kill()
        try:
            proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            raise SafeFailure("child-cleanup-failed") from None


def run_child(argv: list[str], env: dict[str, str], stdin: bytes = b"", timeout: float = 30.0) -> tuple[int, bytes, bytes]:
    try:
        proc: subprocess.Popen[bytes] = subprocess.Popen(
            argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            env=env, start_new_session=True,
        )
    except (OSError, ValueError):
        raise SafeFailure("child-launch-failed") from None
    completed = False
    try:
        try:
            stdout, stderr = proc.communicate(stdin, timeout=timeout)
            completed = True
        except subprocess.TimeoutExpired:
            stop_process_group(proc, force=True)
            try:
                proc.communicate(timeout=2)
            except subprocess.TimeoutExpired:
                raise SafeFailure("child-pipe-cleanup-failed") from None
            raise SafeFailure("child-timeout") from None
        return proc.returncode, stdout, stderr
    finally:
        if not completed:
            stop_process_group(proc)
        for pipe in (proc.stdin, proc.stdout, proc.stderr):
            if pipe is not None:
                try:
                    pipe.close()
                except OSError:
                    pass


def single_json_line(raw: bytes, label: str) -> dict[str, Any]:
    try:
        text = raw.decode("utf-8")
        lines = text.splitlines()
        fail(len(lines) == 1 and bool(lines[0]), label)
        result = json.loads(lines[0])
    except (UnicodeError, json.JSONDecodeError):
        raise SafeFailure(label) from None
    fail(isinstance(result, dict), label)
    return result


@dataclass(frozen=True)
class CLIResult:
    data: Any
    error_code: str | None
    exit_code: int


def invoke_cli(cli_path: Path, home: Path, temp_root: Path, args: list[str], password: str | None,
               expected_exit: int = 0, expected_error: str | None = None, timeout: float = 30.0) -> CLIResult:
    stdin = password.encode("utf-8") + b"\n" if password is not None else b""
    argv = [str(cli_path), *JSON_FLAGS, *args]
    if password is not None and "--password-stdin" not in argv:
        argv.append("--password-stdin")
    code, stdout, stderr = run_child(argv, child_env(home, temp_root), stdin, timeout)
    fail(code == expected_exit, "cli-exit-code")
    if expected_exit == 0:
        fail(not stderr, "cli-unexpected-stderr")
        envelope = single_json_line(stdout, "cli-success-envelope")
        fail(envelope.get("ok") is True and "data" in envelope, "cli-success-envelope")
        return CLIResult(envelope["data"], None, code)
    fail(not stdout, "cli-error-stdout")
    envelope = single_json_line(stderr, "cli-error-envelope")
    fail(envelope.get("ok") is False and envelope.get("error") == expected_error, "cli-error-envelope")
    return CLIResult(envelope.get("data"), envelope.get("error"), code)


def require_data(condition: bool, label: str) -> None:
    fail(condition, label)


class RelayProcess:
    def __init__(self, executable: Path, data_dir: Path, home: Path, temp_root: Path, port: int):
        self.executable, self.data_dir, self.home, self.temp_root, self.port = executable, data_dir, home, temp_root, port
        self.proc: subprocess.Popen[bytes] | None = None
        self.ready_line = threading.Event()
        self.reader_done = threading.Event()
        self.reader: threading.Thread | None = None

    def start(self, deadline: float = 10.0) -> None:
        fail(self.proc is None, "relay-already-started")
        self.ready_line.clear()
        self.reader_done.clear()
        env = child_env(self.home, self.temp_root)
        try:
            self.proc = subprocess.Popen(
                [str(self.executable), "--listen", "127.0.0.1", "--port", str(self.port), "--data-dir", str(self.data_dir)],
                stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                env=env, start_new_session=True,
            )
        except OSError:
            raise SafeFailure("relay-launch-failed") from None
        proc = self.proc
        self.reader = threading.Thread(target=self._read_ready, args=(proc,), daemon=True)
        self.reader.start()
        until = time.monotonic() + deadline
        while time.monotonic() < until:
            if self.ready_line.is_set() and self._connectable():
                return
            time.sleep(0.05)
        self.stop()
        raise SafeFailure("relay-readiness-timeout")

    def _read_ready(self, proc: subprocess.Popen[bytes]) -> None:
        assert proc.stdout is not None
        try:
            for line in iter(proc.stdout.readline, b""):
                if b"Hyphae relay listening on ws://127.0.0.1:" in line:
                    self.ready_line.set()
        finally:
            self.reader_done.set()

    def _connectable(self) -> bool:
        try:
            with socket.create_connection(("127.0.0.1", self.port), timeout=0.15):
                return True
        except OSError:
            return False

    def stop(self) -> None:
        if self.proc is not None:
            proc = self.proc
            try:
                if self.reader_done.is_set() and proc.poll() is not None:
                    proc.wait()
                else:
                    stop_process_group(proc)
            finally:
                if proc.stdout is not None:
                    try:
                        proc.stdout.close()
                    except OSError:
                        pass
                if self.reader is not None:
                    self.reader.join(timeout=2)
                    if self.reader.is_alive():
                        raise SafeFailure("relay-reader-cleanup-failed")
                self.proc = None
                self.reader = None

    @property
    def url(self) -> str:
        return f"ws://127.0.0.1:{self.port}"


def free_port() -> int:
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.bind(("127.0.0.1", 0))
            return int(sock.getsockname()[1])
    except OSError:
        raise SafeFailure("local-listener-unavailable") from None


def history_inbox(cli: Path, home: Path, temp_root: Path, nickname: str) -> list[dict[str, Any]]:
    result = invoke_cli(cli, home, temp_root, ["history", "inbox", "--as", nickname, "--limit", "10"], None)
    fail(isinstance(result.data, list) and all(isinstance(row, dict) for row in result.data), "history-inbox-shape")
    return result.data


def json_outbox_id(data: Any, event_id: str) -> str:
    fail(isinstance(data, list), "outbox-shape")
    matches = [item for item in data if isinstance(item, dict) and item.get("id") == event_id]
    fail(len(matches) == 1, "outbox-entry-missing")
    return event_id


def stage(name: str) -> None:
    print(f"PASS {name}", flush=True)


def run_round_one(cli: Path, relay_binary: Path, cli_hash: str, relay_hash: str, lock: dict[str, Any],
                  args: argparse.Namespace) -> None:
    with tempfile.TemporaryDirectory(prefix="hyphae-comm-round1-") as temp_name:
        root = Path(temp_name)
        home_a, home_b = root / "home-a", root / "home-b"
        relay_home, relay_data = root / "relay-home", root / "relay-data"
        for path in (home_a, home_b, relay_home, relay_data):
            path.mkdir(mode=0o700)
        password_a = "R1-" + os.urandom(24).hex()
        password_b = "R1-" + os.urandom(24).hex()
        body_ab = "round-one-ab-" + os.urandom(8).hex()
        body_ba = "round-one-ba-" + os.urandom(8).hex()
        body_offline = "round-one-queued-" + os.urandom(8).hex()
        relay = RelayProcess(relay_binary, relay_data, relay_home, root, free_port())
        try:
            created_a = invoke_cli(cli, home_a, root, ["identity", "create", "--nickname", "alice", "--default", "--password-stdin"], password_a, timeout=args.timeout)
            created_b = invoke_cli(cli, home_b, root, ["identity", "create", "--nickname", "bob", "--default", "--password-stdin"], password_b, timeout=args.timeout)
            require_data(created_a.data.get("encrypted") is True and created_b.data.get("encrypted") is True, "identity-not-encrypted")
            npub_a, npub_b = created_a.data.get("npub"), created_b.data.get("npub")
            require_data(isinstance(npub_a, str) and npub_a.startswith("npub1") and isinstance(npub_b, str) and npub_b.startswith("npub1"), "identity-output-invalid")
            stage("encrypted-identities")

            invoke_cli(cli, home_a, root, ["contact", "add", "--nickname", "bob", "--npub", npub_b], None)
            invoke_cli(cli, home_b, root, ["contact", "add", "--nickname", "alice", "--npub", npub_a], None)
            stage("mutual-contacts")

            relay.start(args.readiness_timeout)
            invoke_cli(cli, home_a, root, ["relay", "set", "--relay", relay.url], None)
            listed_a = invoke_cli(cli, home_a, root, ["relay", "list"], None)
            invoke_cli(cli, home_b, root, ["relay", "set", "--relay", relay.url], None)
            listed_b = invoke_cli(cli, home_b, root, ["relay", "list"], None)
            require_data(listed_a.data.get("source") == "config" and listed_a.data.get("relays") == [relay.url], "relay-config-a")
            require_data(listed_b.data.get("source") == "config" and listed_b.data.get("relays") == [relay.url], "relay-config-b")
            stage("relay-configured")

            require_data(history_inbox(cli, home_b, root, "bob") == [], "history-not-empty-initially")
            inbox_empty = invoke_cli(cli, home_b, root, ["agent", "inbox", "--as", "bob", "--password-stdin"], password_b)
            require_data(inbox_empty.data == [], "pre-pull-inbox-not-empty")
            stage("empty-history-before-pull")

            sent_ab = invoke_cli(cli, home_a, root, ["agent", "msg", "--from", "alice", "--to", "bob", "--content", body_ab], password_a)
            data_ab = sent_ab.data
            event_ab = data_ab.get("event_id")
            require_data(data_ab.get("encrypted") is True and isinstance(event_ab, str) and len(event_ab) == 64 and data_ab.get("published_to", 0) >= 1, "send-a-to-b")
            require_data(history_inbox(cli, home_b, root, "bob") == [], "history-visible-before-inbox-pull")
            inbox_b = invoke_cli(cli, home_b, root, ["agent", "inbox", "--as", "bob", "--password-stdin"], password_b)
            matching_b = [row for row in inbox_b.data if isinstance(row, dict) and row.get("event_id") == event_ab and row.get("content") == body_ab and row.get("encrypted") is True and row.get("decrypted") is True]
            require_data(len(matching_b) == 1, "receive-a-to-b")
            rows_b = history_inbox(cli, home_b, root, "bob")
            require_data(sum(row.get("id") == event_ab and row.get("plaintext") == body_ab and row.get("is_encrypted") is True and row.get("is_incoming") is True for row in rows_b) == 1, "history-a-to-b")
            stage("encrypted-a-to-b")

            sent_ba = invoke_cli(cli, home_b, root, ["agent", "msg", "--from", "bob", "--to", "alice", "--content", body_ba], password_b)
            data_ba = sent_ba.data
            event_ba = data_ba.get("event_id")
            require_data(data_ba.get("encrypted") is True and isinstance(event_ba, str) and len(event_ba) == 64 and data_ba.get("published_to", 0) >= 1, "send-b-to-a")
            inbox_a = invoke_cli(cli, home_a, root, ["agent", "inbox", "--as", "alice", "--password-stdin"], password_a)
            matching_a = [row for row in inbox_a.data if isinstance(row, dict) and row.get("event_id") == event_ba and row.get("content") == body_ba and row.get("encrypted") is True and row.get("decrypted") is True]
            require_data(len(matching_a) == 1, "receive-b-to-a")
            rows_a = history_inbox(cli, home_a, root, "alice")
            require_data(sum(row.get("id") == event_ba and row.get("plaintext") == body_ba and row.get("is_encrypted") is True and row.get("is_incoming") is True for row in rows_a) == 1, "history-b-to-a")
            stage("encrypted-b-to-a")

            relay.stop()
            offline = invoke_cli(cli, home_a, root, ["agent", "msg", "--from", "alice", "--to", "bob", "--content", body_offline], password_a)
            offline_data = offline.data
            offline_id = offline_data.get("event_id")
            require_data(offline_data.get("published_to") == 0 and offline_data.get("queued_for_retry") is True and offline_data.get("history_stored") is True and isinstance(offline_id, str), "offline-queue")
            outbox = invoke_cli(cli, home_a, root, ["storage", "outbox", "list"], None)
            retry_id = json_outbox_id(outbox.data, offline_id)
            stage("offline-queued")

            relay.start(args.readiness_timeout)
            retry = invoke_cli(cli, home_a, root, ["storage", "outbox", "retry", "--id", retry_id], None)
            require_data(retry.data.get("event_id") == offline_id and retry.data.get("sent") is True, "retry-same-event-accepted")
            outbox_after = invoke_cli(cli, home_a, root, ["storage", "outbox", "list"], None)
            require_data(outbox_after.data == [], "outbox-not-cleared-after-send")
            for _ in range(2):
                inbox_retry = invoke_cli(cli, home_b, root, ["agent", "inbox", "--as", "bob", "--password-stdin"], password_b)
                retry_matches = [row for row in inbox_retry.data if isinstance(row, dict) and row.get("event_id") == offline_id and row.get("content") == body_offline and row.get("decrypted") is True]
                require_data(len(retry_matches) == 1, "retry-message-received")
                current = history_inbox(cli, home_b, root, "bob")
                require_data(sum(row.get("id") == offline_id and row.get("plaintext") == body_offline and row.get("is_encrypted") is True and row.get("is_incoming") is True for row in current) == 1, "history-retry-deduplication")
                ids = [row.get("id") for row in current]
                require_data(all(isinstance(identifier, str) for identifier in ids) and len(ids) == len(set(ids)), "history-duplicate-id")
            stage("restart-retry-and-history-dedup")

            wrong = invoke_cli(cli, home_b, root, ["agent", "inbox", "--as", "bob", "--password-stdin"], password_b + "-wrong", expected_exit=3, expected_error="auth_error")
            require_data(wrong.data is None, "wrong-password-data")
            contact = invoke_cli(cli, home_a, root, ["contact", "add", "--nickname", "invalid", "--npub", "bad-public-key"], None, expected_exit=4, expected_error="other_error")
            require_data(contact.data is None, "invalid-contact-data")
            invalid_msg = invoke_cli(cli, home_a, root, ["agent", "msg", "--from", "alice", "--to", "missing-contact", "--content", "invalid-input"], password_a, expected_exit=1, expected_error="user_error")
            require_data(invalid_msg.data is None, "invalid-message-data")
            stage("expected-error-codes")
        finally:
            relay.stop()

        summary = {
            "source_sha": lock["source_sha"], "go": lock["go"], "recipe": lock["recipe"],
            "platform": args.platform, "cli_sha256": cli_hash, "relay_sha256": relay_hash,
        }
        print("ARTIFACT " + json.dumps(summary, sort_keys=True), flush=True)
        stage("complete")


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("--cli", type=Path, required=True, help="path to the prebuilt Hyphae binary")
    result.add_argument("--relay", type=Path, required=True, help="path to the prebuilt local relay binary")
    result.add_argument("--lock", type=Path, required=True, help="production artifact lock JSON")
    result.add_argument("--expected-recipe", required=True, help="exact recipe string required by the lock")
    result.add_argument("--expected-relay-sha256", required=True, help="full expected SHA256 for the relay binary")
    result.add_argument("--timeout", type=float, default=30.0, help="per CLI child timeout in seconds")
    result.add_argument("--readiness-timeout", type=float, default=10.0, help="relay readiness deadline in seconds")
    return result


def validate_deadlines(timeout: float, readiness_timeout: float) -> bool:
    return math.isfinite(timeout) and timeout > 0 and math.isfinite(readiness_timeout) and readiness_timeout > 0


def main(argv: list[str] | None = None) -> int:
    args = parser().parse_args(argv)
    args.platform = host_platform()
    if not validate_deadlines(args.timeout, args.readiness_timeout):
        print("FAIL invalid timeout configuration", file=sys.stderr)
        return 2
    try:
        lock, cli_hash_expected = load_lock(args.lock, args.expected_recipe, args.platform)
        relay_expected = args.expected_relay_sha256
        fail(len(relay_expected) == 64 and all(c in "0123456789abcdef" for c in relay_expected), "relay-hash-invalid")
        if args.platform == "darwin-arm64":
            fail(relay_expected == DARWIN_RELAY_SHA, "relay-hash-lock")
        with tempfile.TemporaryDirectory(prefix="hyphae-round1-artifacts-") as artifact_temp:
            private = Path(artifact_temp)
            cli_copy, relay_copy = private / "hyphae", private / "hyphae-relay"
            cli_hash = copy_verified_artifact(args.cli, cli_copy, cli_hash_expected)
            relay_hash = copy_verified_artifact(args.relay, relay_copy, relay_expected)
            recipe_json = json.dumps(lock["recipe"], ensure_ascii=True)
            print(f"BASELINE source={lock['source_sha']} go={lock['go']} recipe={recipe_json} platform={args.platform} cli_sha256={cli_hash} relay_sha256={relay_hash}", flush=True)
            run_round_one(cli_copy, relay_copy, cli_hash, relay_hash, lock, args)
        return 0
    except SafeFailure as error:
        print(f"FAIL {error}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("FAIL interrupted; child cleanup completed", file=sys.stderr)
        return 130
    except Exception:
        print("FAIL unexpected test harness error", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
