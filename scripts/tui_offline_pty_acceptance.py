#!/usr/bin/env python3
"""Opt-in real-relay/PTTY acceptance for durable TUI outbox retry.

This is deliberately not wired into the default test suite. Use --execute only
against a build that implements the documented outbox UI contract. The runner
keeps PTY bytes in memory and writes only a redacted JSON summary when asked.
"""

from __future__ import annotations

import argparse
import base64
import fcntl
import hashlib
import json
import os
import pty
import re
import secrets
import select
import signal
import socket
import sqlite3
import struct
import subprocess
import sys
import tempfile
import termios
import time
import tty
import urllib.parse
import uuid
from pathlib import Path


EXIT_GAP = 77
QUEUED_RE = re.compile(
    r"Outbox:\s*queued for retry\s*[•|]\s*([0-9a-f]{12})"
    r"\s*\(not delivered; awaiting relay ACK\)", re.I
)
ACCEPTED_RE = re.compile(
    r"Outbox:\s*relay accepted\s*[•|]\s*([0-9a-f]{12})"
    r"\s*\(recipient delivery/read not confirmed\)", re.I
)
FAILED_RE = re.compile(r"Outbox:\s*failed\b", re.I)
DELIVERED_RE = re.compile(r"Outbox:\s*(?:delivered|sent|read by recipient)\b", re.I)
INBOX_CONNECTED_RE = re.compile(r"Inbox:\s*Connected(?:\s|•|;|$)", re.I)
INBOX_RECONNECTING_RE = re.compile(r"Inbox:\s*Relay reconnecting", re.I)
INBOX_HISTORY_WARNING_RE = re.compile(r"Inbox:\s*Connected; history sync warning", re.I)

GROUP_ASSERTIONS = [
    "three isolated HOME directories; no identity or key material shared",
    "all invited members remain pending until explicit accept; no early group UI",
    "all active members receive each unique encrypted group message exactly once",
    "fanout has recipient-specific event IDs/ciphertexts and one p recipient per event",
    "relay restart retries the original event IDs; receiver history has no duplicates",
    "captured relay payloads and redacted evidence contain no group body, roster, or keys",
]


class Screen:
    """Small VT100 screen buffer for Bubble Tea's current-screen assertions."""

    def __init__(self, rows: int = 32, cols: int = 120):
        self.rows, self.cols = rows, cols
        self.cells = [[" "] * cols for _ in range(rows)]
        self.row = self.col = 0
        self.state = "text"
        self.csi = ""
        self.utf8 = bytearray()

    def feed(self, data: bytes) -> None:
        for byte in data:
            if self.state == "esc":
                if byte == ord("["):
                    self.state, self.csi = "csi", ""
                elif byte == ord("]"):
                    self.state = "osc"
                else:
                    self.state = "text"
                continue
            if self.state == "osc":
                if byte == 0x07:
                    self.state = "text"
                elif byte == 0x1B:
                    self.state = "osc_esc"
                continue
            if self.state == "osc_esc":
                self.state = "text" if byte == ord("\\") else "osc"
                continue
            if self.state == "csi":
                if 0x40 <= byte <= 0x7E:
                    self._csi(chr(byte), self.csi)
                    self.state, self.csi = "text", ""
                elif len(self.csi) < 64:
                    self.csi += chr(byte)
                else:
                    self.state, self.csi = "text", ""
                continue
            if byte == 0x1B:
                self._flush_utf8()
                self.state = "esc"
            elif byte == 0x0D:
                self._flush_utf8()
                self.col = 0
            elif byte == 0x0A:
                self._flush_utf8()
                self.row = min(self.rows - 1, self.row + 1)
            elif byte == 0x08:
                self._flush_utf8()
                self.col = max(0, self.col - 1)
            elif byte == 0x09:
                self._flush_utf8()
                self.col = min(self.cols - 1, (self.col // 8 + 1) * 8)
            elif byte >= 0x20:
                self.utf8.append(byte)
                try:
                    char = self.utf8.decode("utf-8")
                except UnicodeDecodeError as exc:
                    if exc.reason == "unexpected end of data":
                        continue
                    char = "�"
                self.utf8.clear()
                self._put(char)

    def _flush_utf8(self) -> None:
        if self.utf8:
            self._put("�")
            self.utf8.clear()

    def _put(self, char: str) -> None:
        if self.row < self.rows and self.col < self.cols:
            self.cells[self.row][self.col] = char
        self.col = min(self.cols - 1, self.col + 1)

    def _csi(self, final: str, params: str) -> None:
        clean = params.lstrip("?=>")
        values = [int(x) if x.isdigit() else 0 for x in clean.split(";") if x != ""]
        first = values[0] if values else 0
        if final in ("H", "f"):
            self.row = max(0, min(self.rows - 1, (values[0] if values and values[0] else 1) - 1))
            self.col = max(0, min(self.cols - 1, (values[1] if len(values) > 1 and values[1] else 1) - 1))
        elif final == "A":
            self.row = max(0, self.row - max(1, first))
        elif final == "B":
            self.row = min(self.rows - 1, self.row + max(1, first))
        elif final == "C":
            self.col = min(self.cols - 1, self.col + max(1, first))
        elif final == "D":
            self.col = max(0, self.col - max(1, first))
        elif final == "G":
            self.col = max(0, min(self.cols - 1, max(1, first) - 1))
        elif final == "d":
            self.row = max(0, min(self.rows - 1, max(1, first) - 1))
        elif final == "J" and first in (0, 2):
            if first == 2:
                self.cells = [[" "] * self.cols for _ in range(self.rows)]
                self.row = self.col = 0
            else:
                for row in range(self.row, self.rows):
                    start = self.col if row == self.row else 0
                    self.cells[row][start:] = [" "] * (self.cols - start)
        elif final == "K":
            mode = first
            if mode == 1:
                self.cells[self.row][: self.col + 1] = [" "] * (self.col + 1)
            elif mode == 2:
                self.cells[self.row] = [" "] * self.cols
            else:
                self.cells[self.row][self.col :] = [" "] * (self.cols - self.col)

    def text(self) -> str:
        return "\n".join("".join(row).rstrip() for row in self.cells)


class PtyProcess:
    def __init__(self, argv: list[str], env: dict[str, str], rows: int = 32, cols: int = 120):
        self.pid, self.fd = pty.fork()
        self.screen = Screen(rows, cols)
        self.rows, self.cols = rows, cols
        if self.pid == 0:
            os.environ.clear()
            os.environ.update(env)
            os.execvpe(argv[0], argv, env)
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        os.kill(self.pid, signal.SIGWINCH)
        tty.setraw(self.fd, termios.TCSANOW)

    def drain(self, duration: float = 0.25) -> None:
        deadline = time.monotonic() + duration
        while time.monotonic() < deadline:
            remaining = max(0, deadline - time.monotonic())
            ready, _, _ = select.select([self.fd], [], [], min(0.1, remaining))
            if not ready:
                continue
            try:
                chunk = os.read(self.fd, 65536)
            except OSError:
                return
            if not chunk:
                return
            self.screen.feed(chunk)

    def send(self, data: bytes) -> None:
        os.write(self.fd, data)

    def wait(self, timeout: float) -> bool:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            done, _ = os.waitpid(self.pid, os.WNOHANG)
            if done:
                self.pid = 0
                return True
            self.drain(0.1)
        return False

    def stop(self) -> None:
        if self.pid:
            try:
                os.kill(self.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            if not self.wait(3):
                try:
                    os.kill(self.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                if not self.wait(2):
                    raise RuntimeError("owned TUI child did not stop after SIGKILL")
            self.pid = 0
        try:
            os.close(self.fd)
        except OSError:
            pass


class Relay:
    def __init__(self, binary: Path, data_dir: Path, port: int, env: dict[str, str]):
        self.binary, self.data_dir, self.port, self.env = binary, data_dir, port, env
        self.proc: subprocess.Popen[bytes] | None = None

    def start(self) -> None:
        self.proc = subprocess.Popen(
            [str(self.binary), "--listen", "127.0.0.1", "--port", str(self.port), "--data-dir", str(self.data_dir)],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            env=self.env,
            start_new_session=True,
        )
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            if self.proc.poll() is not None:
                raise RuntimeError("local relay exited before listening")
            with socket.socket() as conn:
                conn.settimeout(0.15)
                if conn.connect_ex(("127.0.0.1", self.port)) == 0:
                    return
            time.sleep(0.05)
        raise RuntimeError("local relay did not start listening")

    def stop(self) -> None:
        if self.proc is None or self.proc.poll() is not None:
            return
        self.proc.send_signal(signal.SIGINT)
        try:
            self.proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            # Only signal the exact child PID created by this runner.
            self.proc.kill()
            self.proc.wait(timeout=2)


def isolated_env(home: Path, cache: Path) -> dict[str, str]:
    env = {k: v for k, v in os.environ.items() if k not in {
        "HOME", "HYPHAE_OUTPUT", "AGENT_SPEAKER_OUTPUT",
        "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
    }}
    env.update({
        "HOME": str(home),
        "GOCACHE": str(cache),
        "XDG_CONFIG_HOME": str(home / ".config"),
        "XDG_DATA_HOME": str(home / ".local" / "share"),
        "XDG_CACHE_HOME": str(home / ".cache"),
        "TERM": "xterm-256color",
        "NO_COLOR": "1",
    })
    return env


def run_cli(binary: Path, home: Path, cache: Path, *args: str, timeout: int = 20) -> bytes:
    env = isolated_env(home, cache)
    result = subprocess.run([str(binary), *args], stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            env=env, timeout=timeout, check=False)
    if result.returncode:
        # Intentionally never forward command output: it may contain identity data.
        raise RuntimeError(f"isolated CLI setup command failed: {args[0]} (exit {result.returncode})")
    return result.stdout


def free_port() -> int:
    with socket.socket() as conn:
        conn.bind(("127.0.0.1", 0))
        return int(conn.getsockname()[1])


def wait_screen(proc: PtyProcess, condition, timeout: float, label: str) -> str:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        proc.drain(0.2)
        current = proc.screen.text()
        if condition(current):
            return current
        if proc.pid == 0:
            raise RuntimeError(f"TUI exited while waiting for {label}")
    raise RuntimeError(f"timed out waiting for UI state: {label}")


def create_identity(binary: Path, home: Path, cache: Path, nickname: str) -> str:
    run_cli(binary, home, cache, "identity", "create", "--nickname", nickname, "--default")
    output = run_cli(binary, home, cache, "identity", "list", "--json")
    data = json.loads(output)
    for item in data.get("data", []):
        if item.get("nickname") == nickname:
            npub = item.get("npub", "")
            if npub.startswith("npub1") and item.get("default") is True:
                return npub
    raise RuntimeError("isolated identity setup failed to expose one public identity")


def set_contact(binary: Path, home: Path, cache: Path, nickname: str, npub: str) -> None:
    run_cli(binary, home, cache, "contact", "add", "--nickname", nickname, "--npub", npub)


def decode_npub(binary: Path, home: Path, cache: Path, npub: str) -> str:
    output = run_cli(binary, home, cache, "decode", "--input", npub).decode("utf-8", "replace")
    match = re.search(r"^Hex:\s*([0-9a-f]{64})$", output, re.I | re.M)
    if not match:
        raise RuntimeError("isolated identity public key did not decode to 32 bytes")
    return match.group(1).lower()


def contact_matches(binary: Path, home: Path, cache: Path, nickname: str, npub: str) -> bool:
    payload = json.loads(run_cli(binary, home, cache, "contact", "list", "--json"))
    return any(item.get("nickname") == nickname and item.get("npub") == npub
               for item in payload.get("data", []))


def start_chat(binary: Path, home: Path, cache: Path, contact: str, relay_url: str) -> PtyProcess:
    return PtyProcess([str(binary), "tui", "chat", "--with", contact, "--relay", relay_url],
                      isolated_env(home, cache))


def wait_outbox(home: Path, event_prefix: str, wanted: bool, timeout: float = 5,
                forbidden_body: str | None = None) -> None:
    # The existing durable outbox format is intentionally inspected only for
    # event identity/state; contents and recipient values are never emitted.
    path = home / ".hyphae" / "outbox.json"
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            payload = json.loads(path.read_text())
            entries = payload.get("entries", [])
            matching = [entry for entry in entries if str(entry.get("id", "")).startswith(event_prefix)]
            present = bool(matching)
            if present == wanted:
                if wanted and forbidden_body and any(forbidden_body in json.dumps(entry) for entry in matching):
                    raise RuntimeError("outbox stored the unique message body in plaintext")
                return
        except (OSError, json.JSONDecodeError):
            pass
        time.sleep(0.1)
    raise RuntimeError("durable outbox state did not reach expected state")


def load_outbox_event(home: Path, event_prefix: str, body: str) -> tuple[dict[str, object], dict[str, object]]:
    path = home / ".hyphae" / "outbox.json"
    try:
        payload = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as exc:
        raise RuntimeError("durable outbox snapshot could not be read") from exc
    matches = [entry for entry in payload.get("entries", [])
               if str(entry.get("id", "")).startswith(event_prefix)]
    if len(matches) != 1:
        raise RuntimeError("durable outbox did not contain exactly one matching event")
    entry = matches[0]
    if body in json.dumps(entry):
        raise RuntimeError("outbox stored the unique message body in plaintext")
    try:
        event = json.loads(entry["event_json"])
    except (KeyError, TypeError, json.JSONDecodeError) as exc:
        raise RuntimeError("durable outbox event could not be decoded") from exc
    return entry, event


def event_targets(event: dict[str, object], sender_hex: str, recipient_hex: str) -> bool:
    tags = event.get("tags", [])
    p_tags = [tag[1].lower() for tag in tags
              if isinstance(tag, list) and len(tag) >= 2 and tag[0] == "p"]
    return (
        event.get("pubkey", "").lower() == sender_hex
        and event.get("kind") == 30078
        and len(p_tags) == 1
        and p_tags[0] == recipient_hex
        and any(isinstance(tag, list) and len(tag) >= 2 and tag[0] == "enc" and tag[1] == "nip44"
                for tag in tags)
        and isinstance(event.get("content"), str)
    )


def verify_event_signature(binary: Path, home: Path, cache: Path, event_json: str, event_id: str) -> bool:
    result = subprocess.run([str(binary), "verify", event_json], stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            env=isolated_env(home, cache), timeout=15, check=False)
    output = result.stdout.decode("utf-8", "replace")
    return result.returncode == 0 and "Signature is VALID" in output and event_id in output


def _read_exact(sock: socket.socket, count: int) -> bytes:
    data = bytearray()
    while len(data) < count:
        chunk = sock.recv(count - len(data))
        if not chunk:
            raise RuntimeError("relay diagnostic socket closed")
        data.extend(chunk)
    return bytes(data)


def _ws_send_text(sock: socket.socket, payload: bytes) -> None:
    mask = secrets.token_bytes(4)
    size = len(payload)
    if size < 126:
        header = bytes((0x81, 0x80 | size))
    elif size < (1 << 16):
        header = bytes((0x81, 0x80 | 126)) + struct.pack("!H", size)
    else:
        header = bytes((0x81, 0x80 | 127)) + struct.pack("!Q", size)
    masked = bytes(value ^ mask[i % 4] for i, value in enumerate(payload))
    sock.sendall(header + mask + masked)


def _ws_read_frame(sock: socket.socket) -> tuple[int, bytes]:
    first, second = _read_exact(sock, 2)
    opcode = first & 0x0F
    length = second & 0x7F
    if length == 126:
        length = struct.unpack("!H", _read_exact(sock, 2))[0]
    elif length == 127:
        length = struct.unpack("!Q", _read_exact(sock, 8))[0]
    masked = bool(second & 0x80)
    mask = _read_exact(sock, 4) if masked else b""
    data = _read_exact(sock, length)
    if masked:
        data = bytes(value ^ mask[i % 4] for i, value in enumerate(data))
    return opcode, data


def query_relay_events(relay_url: str, event_id: str) -> list[dict[str, object]]:
    parsed = urllib.parse.urlsplit(relay_url)
    if parsed.scheme != "ws" or parsed.hostname not in ("127.0.0.1", "localhost"):
        raise RuntimeError("diagnostic relay query is restricted to local ws:// relay")
    port = parsed.port or 80
    path = parsed.path or "/"
    if parsed.query:
        path += "?" + parsed.query
    key = base64.b64encode(secrets.token_bytes(16)).decode("ascii")
    expected = base64.b64encode(hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest()).decode()
    events: list[dict[str, object]] = []
    sub_id = "pty-diag-" + event_id[:12]
    with socket.create_connection((parsed.hostname, port), timeout=5) as conn:
        conn.settimeout(5)
        host = parsed.hostname if port == 80 else f"{parsed.hostname}:{port}"
        request = (f"GET {path} HTTP/1.1\r\nHost: {host}\r\nUpgrade: websocket\r\n"
                   f"Connection: Upgrade\r\nSec-WebSocket-Key: {key}\r\n"
                   "Sec-WebSocket-Version: 13\r\n\r\n")
        conn.sendall(request.encode("ascii"))
        headers = bytearray()
        while not headers.endswith(b"\r\n\r\n") and len(headers) < 8192:
            headers.extend(_read_exact(conn, 1))
        header_text = headers.decode("ascii", "replace")
        if " 101 " not in header_text or f"Sec-WebSocket-Accept: {expected}" not in header_text:
            raise RuntimeError("local relay diagnostic websocket handshake failed")
        query = json.dumps(["REQ", sub_id, {"ids": [event_id]}], separators=(",", ":")).encode()
        _ws_send_text(conn, query)
        saw_eose = False
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            opcode, frame = _ws_read_frame(conn)
            if opcode == 8:
                break
            if opcode == 9:
                continue
            if opcode != 1:
                continue
            try:
                message = json.loads(frame)
            except json.JSONDecodeError:
                continue
            if not isinstance(message, list) or len(message) < 2:
                continue
            if message[0] == "EVENT" and message[1] == sub_id and len(message) >= 3:
                event = message[2]
                if isinstance(event, dict) and event.get("id") == event_id:
                    events.append(event)
            elif message[0] == "EOSE" and message[1] == sub_id:
                saw_eose = True
                break
            elif message[0] == "CLOSED" and message[1] == sub_id:
                raise RuntimeError("local relay rejected exact-event history query")
        if not saw_eose:
            raise RuntimeError("local relay exact-event query did not reach EOSE")
    return events


def event_rows(home: Path, body: str) -> list[str]:
    db_path = home / ".hyphae" / "messages.db"
    if not db_path.exists():
        return []
    with sqlite3.connect(db_path) as db:
        rows = db.execute("SELECT id FROM messages WHERE plaintext = ?", (body,)).fetchall()
    return [str(row[0]) for row in rows]


def independent_inbox_probe(binary: Path, home: Path, cache: Path,
                            relay_url: str, event_id: str, body: str) -> dict[str, object]:
    try:
        output = run_cli(binary, home, cache, "agent", "inbox", "--as", "receiver",
                         "--relay", relay_url, "--json", timeout=20)
        payload = json.loads(output)
        entries = [item for item in payload.get("data", [])
                   if isinstance(item, dict) and item.get("event_id") == event_id]
        return {
            "command_succeeded": bool(payload.get("ok")),
            "exact_event_count": len(entries),
            "encrypted": bool(entries and entries[0].get("encrypted")),
            "decrypted": bool(entries and entries[0].get("decrypted")),
            "body_matches_unique_probe": bool(entries and entries[0].get("content") == body),
        }
    except (RuntimeError, json.JSONDecodeError, AttributeError):
        return {
            "command_succeeded": False,
            "exact_event_count": 0,
            "encrypted": False,
            "decrypted": False,
            "body_matches_unique_probe": False,
        }


def tree_contains(path: Path, needle: bytes) -> bool:
    for item in path.rglob("*"):
        if item.is_file():
            try:
                if needle in item.read_bytes():
                    return True
            except OSError:
                continue
    return False


def safe_hash(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()[:16]


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def write_evidence(path: str | None, data: dict[str, object]) -> None:
    if not path:
        return
    output = Path(path)
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("x", encoding="utf-8") as stream:
        os.chmod(output, 0o600)
        stream.write(json.dumps(data, indent=2, sort_keys=True) + "\n")


def run_offline_acceptance(args: argparse.Namespace) -> int:
    root = Path(__file__).resolve().parents[1]
    evidence: dict[str, object] = {
        "test": "tui-offline-retry-pty",
        "status": "not_run",
        "scope": "local isolated homes and local relay only",
        "source_commit": args.source_commit,
    }
    try:
        with tempfile.TemporaryDirectory(prefix="hyphae-tui-offline-") as temp_name:
            root_temp = Path(temp_name)
            root_temp.chmod(0o700)
            cache = root_temp / "go-cache"
            cache.mkdir(mode=0o700)
            homes = [root_temp / f"home-{x}" for x in ("sender", "receiver")]
            for home in homes:
                home.mkdir(mode=0o700)
            sender_home, receiver_home = homes
            sender_env = isolated_env(sender_home, cache)

            if args.bin_dir:
                bin_dir = Path(args.bin_dir).resolve()
                cli_bin, relay_bin = bin_dir / "hyphae", bin_dir / "hyphae-relay"
                if not cli_bin.is_file() or not relay_bin.is_file():
                    raise RuntimeError("--bin-dir must contain hyphae and hyphae-relay")
            else:
                cli_bin, relay_bin = root_temp / "hyphae", root_temp / "hyphae-relay"
                for package, output in (("./cmd/hyphae", cli_bin), ("./cmd/hyphae-relay", relay_bin)):
                    built = subprocess.run(["go", "build", "-o", str(output), package], cwd=root,
                                            env=sender_env, stdout=subprocess.DEVNULL,
                                            stderr=subprocess.DEVNULL, check=False, timeout=180)
                    if built.returncode:
                        raise RuntimeError(f"local build failed for {package}; output suppressed")

            cli_sha = file_sha256(cli_bin)
            relay_sha = file_sha256(relay_bin)
            if args.expected_cli_sha256 and cli_sha != args.expected_cli_sha256.lower():
                raise RuntimeError("candidate CLI SHA-256 differs from expected value")
            if args.expected_relay_sha256 and relay_sha != args.expected_relay_sha256.lower():
                raise RuntimeError("candidate relay SHA-256 differs from expected value")
            evidence["binary_sha256"] = {"hyphae": cli_sha, "hyphae_relay": relay_sha}

            sender_npub = create_identity(cli_bin, sender_home, cache, "sender")
            receiver_npub = create_identity(cli_bin, receiver_home, cache, "receiver")
            set_contact(cli_bin, sender_home, cache, "receiver", receiver_npub)
            set_contact(cli_bin, receiver_home, cache, "sender", sender_npub)
            sender_hex = decode_npub(cli_bin, sender_home, cache, sender_npub)
            receiver_hex = decode_npub(cli_bin, receiver_home, cache, receiver_npub)
            contacts_match = (
                contact_matches(cli_bin, sender_home, cache, "receiver", receiver_npub)
                and contact_matches(cli_bin, receiver_home, cache, "sender", sender_npub)
            )
            if not contacts_match:
                raise RuntimeError("isolated current identity/contact mapping did not match")

            port = free_port()
            relay_url = f"ws://127.0.0.1:{port}"
            relay_data = root_temp / "relay-data"
            relay_data.mkdir(mode=0o700)
            relay = Relay(relay_bin, relay_data, port, sender_env)
            sender_tui = receiver_tui = None
            try:
                relay.start()
                sender_tui = start_chat(cli_bin, sender_home, cache, "receiver", relay_url)
                receiver_tui = start_chat(cli_bin, receiver_home, cache, "sender", relay_url)
                wait_screen(sender_tui, lambda s: "Type a message" in s, 15, "sender chat open")
                wait_screen(receiver_tui, lambda s: "Type a message" in s, 15, "receiver chat open")
                sender_connected = wait_screen(sender_tui, lambda s: bool(INBOX_CONNECTED_RE.search(s)),
                                               15, "sender inbox connected to local relay")
                receiver_connected = wait_screen(receiver_tui, lambda s: bool(INBOX_CONNECTED_RE.search(s)),
                                                 15, "receiver inbox connected to local relay")
                evidence["pre_send_checks"] = {
                    "sender_default_identity": True,
                    "receiver_default_identity": True,
                    "sender_contact_matches_receiver": contacts_match,
                    "receiver_contact_matches_sender": contacts_match,
                    "sender_and_receiver_use_same_explicit_relay": True,
                    "sender_inbox_connected_before_outage": bool(INBOX_CONNECTED_RE.search(sender_connected)),
                    "receiver_inbox_connected_before_outage": bool(INBOX_CONNECTED_RE.search(receiver_connected)),
                    "sender_npub_sha256_16": safe_hash(sender_npub),
                    "receiver_npub_sha256_16": safe_hash(receiver_npub),
                    "relay_url_sha256_16": safe_hash(relay_url),
                }

                relay.stop()
                receiver_reconnecting = wait_screen(receiver_tui, lambda s: bool(INBOX_RECONNECTING_RE.search(s)),
                                                    15, "receiver inbox detects relay outage")
                sender_reconnecting = wait_screen(sender_tui, lambda s: bool(INBOX_RECONNECTING_RE.search(s)),
                                                  15, "sender inbox detects relay outage")
                evidence["outage_status"] = {
                    "sender_observed_reconnecting": bool(INBOX_RECONNECTING_RE.search(sender_reconnecting)),
                    "receiver_observed_reconnecting": bool(INBOX_RECONNECTING_RE.search(receiver_reconnecting)),
                }
                body = "HY-P0-OFFLINE-" + uuid.uuid4().hex
                sender_tui.send(body.encode("ascii") + b"\r")
                queued_screen = wait_screen(sender_tui, lambda s: bool(QUEUED_RE.search(s)), 15,
                                            "durably queued (not accepted by relay)")
                if FAILED_RE.search(queued_screen) or ACCEPTED_RE.search(queued_screen) or DELIVERED_RE.search(queued_screen):
                    raise RuntimeError("offline send was not shown as queued")
                id_match = QUEUED_RE.search(queued_screen)
                assert id_match is not None
                event_prefix = id_match.group(1).lower()
                wait_outbox(sender_home, event_prefix, True, forbidden_body=body)
                entry, queued_event = load_outbox_event(sender_home, event_prefix, body)
                event_id = str(entry.get("id", ""))
                event_json = str(entry.get("event_json", ""))
                sender_matches_event = queued_event.get("pubkey", "").lower() == sender_hex
                recipient_matches_event = event_targets(queued_event, sender_hex, receiver_hex)
                signature_valid = verify_event_signature(cli_bin, sender_home, cache, event_json, event_id)
                entry_matches_send = (
                    event_id.startswith(event_prefix)
                    and entry.get("recipient_npub") == receiver_npub
                    and entry.get("relays") == [relay_url]
                )
                evidence["queued_event"] = {
                    "event_id_sha256_16": safe_hash(event_id),
                    "sender_pubkey_sha256_16": safe_hash(str(queued_event.get("pubkey", ""))),
                    "recipient_p_tag_sha256_16": safe_hash(next((tag[1] for tag in queued_event.get("tags", [])
                                                                  if isinstance(tag, list) and len(tag) >= 2 and tag[0] == "p"), "")),
                    "sender_matches_current_identity": sender_matches_event,
                    "recipient_p_tag_matches_current_receiver": recipient_matches_event,
                    "signature_valid": signature_valid,
                    "event_id_matches_signed_event": queued_event.get("id") == event_id,
                    "outbox_recipient_and_relay_match": entry_matches_send,
                    "content_excludes_unique_plaintext": body not in str(queued_event.get("content", "")),
                }
                if not (sender_matches_event and recipient_matches_event and signature_valid
                        and entry_matches_send and queued_event.get("id") == event_id
                        and body not in str(queued_event.get("content", ""))):
                    raise RuntimeError("queued event signature/routing did not match isolated identities")

                sender_tui.send(b"\x1b")
                if not sender_tui.wait(8):
                    raise RuntimeError("sender TUI did not exit cleanly")
                sender_tui.stop()
                sender_tui = start_chat(cli_bin, sender_home, cache, "receiver", relay_url)
                restarted_screen = wait_screen(sender_tui, lambda s: bool(QUEUED_RE.search(s)), 15,
                                               "queued item restored after TUI restart")
                restored = QUEUED_RE.search(restarted_screen)
                if not restored or restored.group(1).lower() != event_prefix:
                    raise RuntimeError("restored queue entry changed event ID")

                relay.start()
                receiver_reconnected = wait_screen(receiver_tui, lambda s: bool(INBOX_CONNECTED_RE.search(s)),
                                                   15, "receiver inbox reconnected and subscribed")
                evidence["recovery_status"] = {
                    "receiver_observed_connected_after_restart": bool(INBOX_CONNECTED_RE.search(receiver_reconnected)),
                    "receiver_history_sync_warning": bool(INBOX_HISTORY_WARNING_RE.search(receiver_reconnected)),
                }
                accepted_screen = wait_screen(sender_tui, lambda s: bool(ACCEPTED_RE.search(s)), 35,
                                              "automatic retry accepted by relay")
                accepted = ACCEPTED_RE.search(accepted_screen)
                if not accepted or accepted.group(1).lower() != event_prefix:
                    raise RuntimeError("retry changed the queued event ID")
                wait_outbox(sender_home, event_prefix, False, timeout=8)
                relay_events = query_relay_events(relay_url, event_id)
                relay_event_matches = len(relay_events) == 1 and all(
                    relay_events[0].get(field) == queued_event.get(field)
                    for field in ("id", "pubkey", "created_at", "kind", "tags", "content", "sig")
                )
                evidence["relay_history"] = {
                    "exact_event_id_hit_count": len(relay_events),
                    "stored_event_matches_signed_queued_event": relay_event_matches,
                }
                if not relay_event_matches:
                    raise RuntimeError("relay exact-ID history did not contain exactly the signed queued event")

                try:
                    receiver_screen = wait_screen(receiver_tui, lambda s: body in s, 35,
                                                  "receiver TUI displays the unique message")
                except RuntimeError as exc:
                    receiver_rows = event_rows(receiver_home, body)
                    current = receiver_tui.screen.text()
                    safe_diag = (
                        f"receiver TUI visibility timeout (history_rows={len(receiver_rows)}, "
                        f"chat_prompt={'Type a message' in current}, "
                        f"inbox_connected={bool(INBOX_CONNECTED_RE.search(current))}, "
                        f"inbox_reconnecting={bool(INBOX_RECONNECTING_RE.search(current))}, "
                        f"inbox_status={'Inbox:' in current}, "
                        f"outbox_status={'Outbox:' in current})"
                    )
                    evidence["receiver_after_retry"] = {
                        "history_rows_for_event": len(receiver_rows),
                        "chat_prompt_visible": "Type a message" in current,
                        "inbox_connected": bool(INBOX_CONNECTED_RE.search(current)),
                        "inbox_reconnecting": bool(INBOX_RECONNECTING_RE.search(current)),
                        "inbox_history_sync_warning": bool(INBOX_HISTORY_WARNING_RE.search(current)),
                        "inbox_reject_event_cue": "reject event" in current.lower(),
                        "inbox_decode_error_cue": "decode incoming" in current.lower(),
                        "inbox_store_error_cue": "store incoming" in current.lower(),
                        "message_visible": body in current,
                    }
                    evidence["receiver_independent_inbox_probe"] = independent_inbox_probe(
                        cli_bin, receiver_home, cache, relay_url, event_id, body)
                    raise RuntimeError(safe_diag) from exc
                sender_rows = event_rows(sender_home, body)
                receiver_rows = event_rows(receiver_home, body)
                if len(sender_rows) != 1 or len(receiver_rows) != 1:
                    raise RuntimeError("sender/receiver history did not contain exactly one message row")
                event_id = sender_rows[0]
                if not event_id.startswith(event_prefix) or receiver_rows[0] != event_id:
                    raise RuntimeError("stored/received event ID differed from the queued event ID")
                if receiver_screen.count(body) != 1:
                    raise RuntimeError("receiver current UI did not display the message exactly once")
                if tree_contains(relay_data, body.encode("ascii")):
                    raise RuntimeError("local relay persistence contained the message body in plaintext")

                evidence.update({
                    "status": "pass",
                    "sender_home": "temporary isolated HOME (removed at exit)",
                    "receiver_home": "temporary isolated HOME (removed at exit)",
                    "relay": "local loopback, same data directory across restart",
                    "external_daemon": "not started",
                    "queue_event_id_sha256_16": safe_hash(event_id),
                    "sender_history_rows": len(sender_rows),
                    "receiver_history_rows": len(receiver_rows),
                    "receiver_visible_count": 1,
                    "sensitive_data": "message body, public/private keys, raw PTY and relay output not written",
                })
            finally:
                if sender_tui:
                    sender_tui.stop()
                if receiver_tui:
                    receiver_tui.stop()
                relay.stop()
    except Exception as exc:  # produce a redacted failure, never the raw exception payload
        evidence.update({"status": "fail", "failure": str(exc)[:240]})
        write_evidence(args.evidence, evidence)
        print(json.dumps(evidence, sort_keys=True))
        return 1
    write_evidence(args.evidence, evidence)
    print(json.dumps(evidence, sort_keys=True))
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--execute", action="store_true",
                        help="run the real PTY/relay flow; requires the documented outbox UI contract")
    parser.add_argument("--bin-dir", help="optional directory containing prebuilt hyphae and hyphae-relay")
    parser.add_argument("--source-commit", help="exact clean source commit used to build --bin-dir")
    parser.add_argument("--expected-cli-sha256", help="fail unless the hyphae binary matches this SHA-256")
    parser.add_argument("--expected-relay-sha256", help="fail unless hyphae-relay matches this SHA-256")
    parser.add_argument("--evidence", help="write redacted JSON summary (mode 0600), no raw PTY/logs")
    parser.add_argument("--group-skeleton", action="store_true",
                        help="emit the three-user encrypted group acceptance checklist as expected gaps")
    args = parser.parse_args()
    if args.evidence:
        evidence_path = Path(args.evidence).expanduser().resolve()
        try:
            evidence_path.relative_to(Path.home().resolve())
        except ValueError:
            pass
        else:
            print("refusing to write evidence under the caller's HOME", file=sys.stderr)
            return 2
        if evidence_path.exists():
            print("refusing to overwrite an existing evidence file", file=sys.stderr)
            return 2
        args.evidence = str(evidence_path)
    if args.group_skeleton:
        print(json.dumps({"test": "encrypted-group-three-user-acceptance",
                          "status": "expected_gap", "checks": GROUP_ASSERTIONS}, sort_keys=True))
        return EXIT_GAP
    if not args.execute:
        print(json.dumps({"test": "tui-offline-retry-pty", "status": "expected_gap",
                          "reason": "opt-in runner; pass --execute only after building the documented TUI outbox contract"},
                         sort_keys=True))
        return EXIT_GAP
    if args.bin_dir:
        if not args.source_commit or not re.fullmatch(r"[0-9a-fA-F]{40}", args.source_commit):
            parser.error("--execute --bin-dir requires a full 40-hex --source-commit")
        if not args.expected_cli_sha256 or not re.fullmatch(r"[0-9a-fA-F]{64}", args.expected_cli_sha256):
            parser.error("--execute --bin-dir requires a 64-hex --expected-cli-sha256")
        if not args.expected_relay_sha256 or not re.fullmatch(r"[0-9a-fA-F]{64}", args.expected_relay_sha256):
            parser.error("--execute --bin-dir requires a 64-hex --expected-relay-sha256")
    return run_offline_acceptance(args)


if __name__ == "__main__":
    raise SystemExit(main())
