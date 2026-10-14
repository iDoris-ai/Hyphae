#!/usr/bin/env python3
"""Real-PTY evidence for an offline DM restart.

Drives the compiled `hyphae tui chat` process. Alice presses Enter while the
loopback relay is down, the OS process exits, and a new process with the same
temporary HOME retries the queued event. No daemon is started. The history row
created by the first send is left in place; this is not the delete-and-recover
case. Group runtime is not started and stays UNPROVEN.

The fixture relay speaks only the nostr EVENT/OK and REQ/EOSE frames this
client already uses. It lives in this test helper. Production code is not
modified.

Exit status is 0 only when Bob's TUI has the decrypted plaintext for the
original event id, rendered once. A PTY reset, one existing history row, the
same ciphertext and signature, relay ACK, and the absence of a daemon are not
enough. `--drop-recipient` still ACKs Alice and answers Bob's REQ with EOSE
and no EVENT, so that bounded run must exit non-zero.
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
import threading
import time
import tty
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from tui_offline_pty_acceptance import Screen  # noqa: E402

ALT_ENTER = b"\x1b[?1049h"
ALT_LEAVE = b"\x1b[?1049l"
GROUP_RUNTIME = "UNPROVEN"
WS_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
REAL_HOME_FILES = (
    "keystore.json",
    "outbox.json",
    "messages.db",
    "messages.db-wal",
    "messages.db-shm",
)


def ws_accept_value(key: str) -> str:
    digest = hashlib.sha1((key + WS_GUID).encode("ascii")).digest()
    return base64.b64encode(digest).decode("ascii")


def write_frame(sock: socket.socket, opcode: int, payload: bytes, mask: bool) -> None:
    header = bytearray((0x80 | opcode,))
    mask_bit = 0x80 if mask else 0
    length = len(payload)
    if length < 126:
        header.append(mask_bit | length)
    elif length < 65536:
        header.append(mask_bit | 126)
        header.extend(struct.pack("!H", length))
    else:
        header.append(mask_bit | 127)
        header.extend(struct.pack("!Q", length))
    if mask:
        key = secrets.token_bytes(4)
        header.extend(key)
        payload = bytes(value ^ key[index % 4] for index, value in enumerate(payload))
    sock.sendall(bytes(header) + payload)


class ByteReader:
    def __init__(self, sock: socket.socket, initial: bytes = b"") -> None:
        self.sock = sock
        self.buf = bytearray(initial)

    def read_exact(self, count: int) -> bytes:
        while len(self.buf) < count:
            chunk = self.sock.recv(max(4096, count - len(self.buf)))
            if not chunk:
                raise RuntimeError("socket closed")
            self.buf.extend(chunk)
        out = bytes(self.buf[:count])
        del self.buf[:count]
        return out

    def read_frame(self) -> tuple[int, bytes]:
        first, second = self.read_exact(2)
        opcode = first & 0x0F
        length = second & 0x7F
        if length == 126:
            length = struct.unpack("!H", self.read_exact(2))[0]
        elif length == 127:
            length = struct.unpack("!Q", self.read_exact(8))[0]
        mask = self.read_exact(4) if second & 0x80 else b""
        data = self.read_exact(length)
        if mask:
            data = bytes(value ^ mask[index % 4] for index, value in enumerate(data))
        return opcode, data


def handshake(conn: socket.socket) -> ByteReader:
    data = bytearray()
    while b"\r\n\r\n" not in data:
        chunk = conn.recv(4096)
        if not chunk:
            raise RuntimeError("websocket handshake closed")
        data.extend(chunk)
        if len(data) > 65536:
            raise RuntimeError("websocket handshake too large")
    head, rest = bytes(data).split(b"\r\n\r\n", 1)
    key = ""
    for line in head.decode("iso-8859-1", "replace").split("\r\n")[1:]:
        if line.lower().startswith("sec-websocket-key:"):
            key = line.split(":", 1)[1].strip()
    if not key:
        raise RuntimeError("websocket handshake missing key")
    response = (
        "HTTP/1.1 101 Switching Protocols\r\n"
        "Upgrade: websocket\r\n"
        "Connection: Upgrade\r\n"
        f"Sec-WebSocket-Accept: {ws_accept_value(key)}\r\n"
        "\r\n"
    )
    conn.sendall(response.encode("ascii"))
    return ByteReader(conn, rest)


class FixtureRelay:
    """Loopback nostr relay. It can be down, then withhold OK until release."""

    def __init__(self, port: int, drop_recipient: bool = False) -> None:
        self.port = port
        self.drop_recipient = drop_recipient
        self.hold = threading.Event()
        self.arrived = threading.Event()
        self.stop = threading.Event()
        self.ready = threading.Event()
        self._lock = threading.Lock()
        self.events: list[dict[str, object]] = []
        self.ok_sent = 0
        self.req_answered = 0
        self.mismatch = False
        self._thread: threading.Thread | None = None
        self._sock: socket.socket | None = None

    @property
    def url(self) -> str:
        return f"ws://127.0.0.1:{self.port}"

    def start(self) -> None:
        sock = socket.socket()
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(("127.0.0.1", self.port))
        sock.listen(32)
        sock.settimeout(0.2)
        self._sock = sock
        self._thread = threading.Thread(target=self._serve, name="fixture-relay", daemon=True)
        self._thread.start()
        if not self.ready.wait(2):
            raise RuntimeError("fixture relay did not start")

    def _serve(self) -> None:
        self.ready.set()
        assert self._sock is not None
        while not self.stop.is_set():
            try:
                conn, _ = self._sock.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            threading.Thread(target=self._connection, args=(conn,), daemon=True).start()

    def _connection(self, conn: socket.socket) -> None:
        try:
            conn.settimeout(8)
            reader = handshake(conn)
            while not self.stop.is_set():
                opcode, payload = reader.read_frame()
                if opcode == 0x8:
                    return
                if opcode == 0x9:
                    write_frame(conn, 0xA, payload, mask=False)
                    continue
                if opcode != 0x1:
                    continue
                self._text(conn, payload.decode("utf-8", "replace"))
        except (OSError, RuntimeError, UnicodeError, json.JSONDecodeError):
            return
        finally:
            try:
                conn.close()
            except OSError:
                pass

    def _text(self, conn: socket.socket, payload: str) -> None:
        message = json.loads(payload)
        if not isinstance(message, list) or not message:
            return
        command = message[0]
        if command == "EVENT" and len(message) >= 2 and isinstance(message[1], dict):
            self._note(message[1])
            if not self.hold.wait(8):
                return
            event_id = str(message[1].get("id", ""))
            body = json.dumps(["OK", event_id, True, ""], separators=(",", ":")).encode()
            write_frame(conn, 0x1, body, mask=False)
            with self._lock:
                self.ok_sent += 1
            return
        if command == "REQ" and len(message) >= 2:
            sub = str(message[1])
            with self._lock:
                events = [] if self.drop_recipient else list(self.events)
                self.req_answered += 1
            for event in events:
                frame = json.dumps(["EVENT", sub, event], separators=(",", ":")).encode()
                write_frame(conn, 0x1, frame, mask=False)
            write_frame(conn, 0x1, json.dumps(["EOSE", sub], separators=(",", ":")).encode(), mask=False)

    def _note(self, event: dict[str, object]) -> None:
        with self._lock:
            if self.events and not same_event(self.events[0], event):
                self.mismatch = True
            self.events.append(event)
            self.arrived.set()

    def wait_event(self, timeout: float) -> dict[str, object]:
        if not self.arrived.wait(timeout):
            raise RuntimeError("fixture relay did not receive an event")
        with self._lock:
            if self.mismatch:
                raise RuntimeError("fixture relay observed more than one distinct event")
            return self.events[0]

    def release(self) -> None:
        self.hold.set()

    def close(self) -> None:
        self.stop.set()
        self.hold.set()
        if self._sock is not None:
            try:
                self._sock.close()
            except OSError:
                pass
        if self._thread is not None:
            self._thread.join(timeout=2)


def same_event(left: dict[str, object], right: dict[str, object]) -> bool:
    return all(left.get(field) == right.get(field) for field in ("id", "pubkey", "sig", "content", "kind", "tags"))


def reserve_port() -> int:
    with socket.socket() as sock:
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def alt_screen_result(raw: bytes) -> dict[str, object]:
    enter_at = raw.rfind(ALT_ENTER)
    leave_at = raw.rfind(ALT_LEAVE)
    if enter_at < 0 and leave_at < 0:
        last = "absent"
    elif leave_at > enter_at:
        last = "leave"
    else:
        last = "enter"
    return {
        "enter_count": raw.count(ALT_ENTER),
        "leave_count": raw.count(ALT_LEAVE),
        "last": last,
        "clean": raw.count(ALT_ENTER) >= 1 and raw.count(ALT_LEAVE) >= 1 and last == "leave",
    }


def group_runtime_gate() -> dict[str, str]:
    """The group runtime is a separate claim. This gate does not open a database."""
    return {
        "status": GROUP_RUNTIME,
        "reason": "no group process, send, or subscription was started; a mocked group row is not evidence",
    }


def sha256_text(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def real_home_stamp(home: Path) -> dict[str, object]:
    base = home / ".hyphae"
    stamp: dict[str, object] = {"exists": base.exists()}
    for name in REAL_HOME_FILES:
        path = base / name
        if path.exists():
            stat = path.stat()
            stamp[name] = [stat.st_mtime_ns, stat.st_size]
        else:
            stamp[name] = None
    return stamp


def sanitize(text: str, forbidden: list[str]) -> str:
    redacted = text
    for item in sorted(forbidden, key=len, reverse=True):
        if item:
            redacted = redacted.replace(item, "<redacted>")
    redacted = re.sub(r"nsec1[0-9ac-hj-np-z]+", "<redacted>", redacted, flags=re.I)
    redacted = re.sub(r"npub1[0-9ac-hj-np-z]+", "<redacted>", redacted, flags=re.I)
    redacted = re.sub(r"/Users/[^\s\"']+", "<path>", redacted)
    redacted = re.sub(r"/private/tmp/[^\s\"']+", "<path>", redacted)
    redacted = re.sub(r"/var/folders/[^\s\"']+", "<path>", redacted)
    return redacted[:240]


class PtySession:
    def __init__(self, argv: list[str], env: dict[str, str], cwd: Path, rows: int = 40, cols: int = 120):
        self.argv = argv
        self.raw = bytearray()
        self.screen = Screen(rows, cols)
        self.answered_query = False
        master, slave = pty.openpty()
        tty.setraw(slave)
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        os.set_blocking(master, False)
        self.master = master
        self.proc = subprocess.Popen(
            argv,
            stdin=slave,
            stdout=slave,
            stderr=slave,
            env=env,
            cwd=str(cwd),
            start_new_session=True,
            close_fds=True,
        )
        os.close(slave)
        os.kill(self.proc.pid, signal.SIGWINCH)

    def drain(self, duration: float) -> None:
        deadline = time.monotonic() + duration
        while time.monotonic() < deadline:
            timeout = max(0, min(0.05, deadline - time.monotonic()))
            ready, _, _ = select.select([self.master], [], [], timeout)
            if not ready:
                continue
            while True:
                try:
                    chunk = os.read(self.master, 65536)
                except BlockingIOError:
                    break
                except OSError:
                    return
                if not chunk:
                    return
                self.raw.extend(chunk)
                self.screen.feed(chunk)
                self._answer_queries(chunk)

    def _answer_queries(self, chunk: bytes) -> None:
        if self.answered_query:
            return
        reply = bytearray()
        if b"]11;?" in chunk or b"]10;?" in chunk:
            reply.extend(b"\x1b]11;rgb:0000/0000/0000\x07\x1b]10;rgb:ffff/ffff/ffff\x07")
        if b"\x1b[6n" in chunk:
            reply.extend(b"\x1b[1;1R")
        if reply:
            os.write(self.master, bytes(reply))
            self.answered_query = True

    def send(self, data: bytes) -> None:
        os.write(self.master, data)

    def text(self) -> str:
        self.drain(0.05)
        return self.screen.text()

    def wait_until(self, predicate, timeout: float) -> bool:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            self.drain(0.15)
            if predicate():
                return True
            if self.proc.poll() is not None:
                return False
        return False

    def wait_exit(self, timeout: float) -> bool:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            self.drain(0.1)
            if self.proc.poll() is not None:
                self.drain(0.3)
                return True
        return False

    def close(self) -> int:
        if self.proc.poll() is None:
            try:
                os.killpg(self.proc.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                self.proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(self.proc.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                self.proc.wait(timeout=2)
        self.drain(0.2)
        code = self.proc.returncode if self.proc.returncode is not None else 1
        if self.proc.poll() is None:
            raise RuntimeError("owned TUI child did not stop")
        try:
            os.close(self.master)
        except OSError:
            pass
        return code


def isolated_env(home: Path, cache: Path) -> dict[str, str]:
    return {
        "HOME": str(home),
        "PATH": "/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin",
        "TERM": "xterm-256color",
        "LANG": "C.UTF-8",
        "TMPDIR": str(cache.parent),
        "GOCACHE": str(cache),
        "XDG_CONFIG_HOME": str(home / ".config"),
        "XDG_DATA_HOME": str(home / ".local" / "share"),
        "XDG_CACHE_HOME": str(home / ".cache"),
    }


def run_cli(binary: Path, home: Path, cache: Path, *args: str, timeout: float = 20) -> bytes:
    result = subprocess.run(
        [str(binary), *args],
        cwd=str(cache.parent),
        env=isolated_env(home, cache),
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout,
        check=False,
    )
    if result.returncode != 0:
        raise RuntimeError(f"cli setup failed: {args[0]} exit {result.returncode}")
    return result.stdout


def create_identity(binary: Path, home: Path, cache: Path, nickname: str) -> str:
    run_cli(binary, home, cache, "identity", "create", "--nickname", nickname, "--default")
    payload = json.loads(run_cli(binary, home, cache, "identity", "list", "--json"))
    items = payload.get("data", []) if isinstance(payload, dict) else payload
    for item in items:
        if item.get("nickname") == nickname and item.get("default") is True and str(item.get("npub", "")).startswith("npub1"):
            return str(item["npub"])
    raise RuntimeError("cli setup failed: identity list")


def decode_npub(binary: Path, home: Path, cache: Path, npub: str) -> str:
    output = run_cli(binary, home, cache, "decode", "--input", npub).decode("utf-8", "replace")
    match = re.search(r"^Hex:\s*([0-9a-f]{64})$", output, re.I | re.M)
    if not match:
        raise RuntimeError("cli setup failed: decode")
    return match.group(1).lower()


def load_outbox(home: Path) -> list[dict[str, object]]:
    path = home / ".hyphae" / "outbox.json"
    try:
        payload = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError):
        return []
    entries = payload.get("entries", [])
    return [entry for entry in entries if isinstance(entry, dict)]


def wait_outbox(home: Path, timeout: float) -> list[dict[str, object]]:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        entries = load_outbox(home)
        if len(entries) == 1 and entries[0].get("status") == "pending" and entries[0].get("event_json"):
            return entries
        time.sleep(0.1)
    raise RuntimeError("outbox did not persist exactly one pending event")


def history_rows(home: Path, timeout: float = 3) -> list[dict[str, object]]:
    path = home / ".hyphae" / "messages.db"
    if not path.exists():
        return []
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        for uri in (f"file:{path}?mode=ro", f"file:{path}"):
            try:
                with sqlite3.connect(uri, uri=True, timeout=0.2) as db:
                    db.execute("PRAGMA query_only=ON")
                    found = db.execute(
                        "SELECT id, content, plaintext, is_encrypted, is_incoming FROM messages ORDER BY id"
                    ).fetchall()
                return [
                    {
                        "id": str(row[0]),
                        "content": str(row[1] or ""),
                        "plaintext": str(row[2] or ""),
                        "is_encrypted": bool(row[3]),
                        "is_incoming": bool(row[4]),
                    }
                    for row in found
                ]
            except sqlite3.Error:
                continue
        time.sleep(0.05)
    raise RuntimeError("history database was not readable")


def rows_are_existing_plaintext(rows: list[dict[str, object]], body: str, ciphertext: str, event_id: str) -> bool:
    if len(rows) != 1:
        return False
    row = rows[0]
    return (
        row["id"] == event_id
        and row["content"] == ciphertext
        and row["plaintext"] == body
        and row["is_encrypted"] is True
        and row["is_incoming"] is False
    )


def binary_pids(binary: Path) -> set[int]:
    try:
        output = subprocess.check_output(["ps", "-ax", "-o", "pid=,command="], text=True)
    except (OSError, subprocess.CalledProcessError):
        return set()
    found = set()
    needle = str(binary)
    for line in output.splitlines():
        stripped = line.strip()
        if not stripped or needle not in stripped:
            continue
        pid_text = stripped.split(maxsplit=1)[0]
        if pid_text.isdigit():
            found.add(int(pid_text))
    return found


def verify_event(binary: Path, home: Path, cache: Path, event_json: str) -> bool:
    result = subprocess.run(
        [str(binary), "verify", event_json],
        cwd=str(cache.parent),
        env=isolated_env(home, cache),
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=15,
        check=False,
    )
    return result.returncode == 0 and b"Signature is VALID" in result.stdout


def recipient_observation(
    rows: list[dict[str, object]],
    body: str,
    ciphertext: str,
    event_id: str,
    screen_text: str,
) -> str | None:
    """Classify Bob's view of this event. None means it has not arrived yet."""
    matched = [row for row in rows if row.get("id") == event_id or row.get("plaintext") == body]
    if not matched:
        return None
    if len(matched) != 1:
        return "fail"
    row = matched[0]
    visible = screen_text.count(body)
    exact = (
        row.get("id") == event_id
        and row.get("content") == ciphertext
        and row.get("plaintext") == body
        and row.get("is_encrypted") is True
        and row.get("is_incoming") is True
        and visible == 1
    )
    return "pass" if exact else "fail"


def harness_exit_ok(reps: list[dict[str, object]], reps_requested: int) -> bool:
    """Require Bob's decrypted event. Core restart claims alone stay false-green."""
    if reps_requested < 1 or len(reps) != reps_requested:
        return False
    for item in reps:
        marker = item.get("recipient_event_marker")
        if item.get("ok") is not True or item.get("recipient") != "pass":
            return False
        if not isinstance(marker, str) or not marker or marker != item.get("event_id_sha256"):
            return False
    return True


def composer_ready(session: PtySession) -> bool:
    text = session.screen.text()
    raw = bytes(session.raw)
    return "enter: send" in text or b"enter: send" in raw or "Type a message" in text or b"Type a message" in raw


def start_chat(binary: Path, home: Path, cache: Path, contact: str, relay_url: str) -> PtySession:
    return PtySession(
        [str(binary), "tui", "chat", "--with", contact, "--relay", relay_url],
        isolated_env(home, cache),
        cache.parent,
    )


def quit_clean(session: PtySession) -> int:
    session.send(b"\x1b")
    if not session.wait_exit(10):
        return session.close()
    return session.close()


def run_rep(
    binary: Path,
    cache: Path,
    root_temp: Path,
    real_stamp: dict[str, object],
    real_home: Path,
    drop_recipient: bool = False,
) -> dict[str, object]:
    rep_id = secrets.token_hex(4)
    body = "pty-dm-" + rep_id
    alice_home = root_temp / f"alice-{rep_id}"
    bob_home = root_temp / f"bob-{rep_id}"
    alice_home.mkdir(mode=0o700)
    bob_home.mkdir(mode=0o700)
    if alice_home.resolve() == real_home or bob_home.resolve() == real_home:
        raise RuntimeError("refusing to use the real HOME")
    port = reserve_port()
    relay = FixtureRelay(port, drop_recipient=drop_recipient)
    alice = bob = None
    before_pids = binary_pids(binary)
    checks: dict[str, object] = {
        "rep": rep_id,
        "ok": False,
        "daemon_started": False,
        "group_runtime": group_runtime_gate()["status"],
        "recipient": "not_run",
        "history_deleted": False,
    }
    try:
        alice_npub = create_identity(binary, alice_home, cache, "alice")
        bob_npub = create_identity(binary, bob_home, cache, "bob")
        if real_home_stamp(real_home) != real_stamp:
            raise RuntimeError("real hyphae data directory changed during isolated setup")
        run_cli(binary, alice_home, cache, "contact", "add", "--nickname", "bob", "--npub", bob_npub)
        run_cli(binary, bob_home, cache, "contact", "add", "--nickname", "alice", "--npub", alice_npub)
        bob_hex = decode_npub(binary, alice_home, cache, bob_npub)
        alice_hex = decode_npub(binary, bob_home, cache, alice_npub)

        alice = start_chat(binary, alice_home, cache, "bob", relay.url)
        if not alice.wait_until(lambda: composer_ready(alice), 15):
            raise RuntimeError("alice TUI did not present the composer while offline")
        if ALT_ENTER not in alice.raw:
            raise RuntimeError("alice TUI did not enter the alternate screen")
        alice.send(body.encode("ascii"))
        if not alice.wait_until(lambda: body in alice.screen.text() or body.encode() in alice.raw, 8):
            raise RuntimeError("alice TUI did not echo the typed message")
        alice.send(b"\r")
        entries = wait_outbox(alice_home, 20)
        event_json = str(entries[0]["event_json"])
        try:
            event = json.loads(event_json)
        except json.JSONDecodeError as exc:
            raise RuntimeError("queued event could not be decoded") from exc
        event_id = str(event.get("id", ""))
        ciphertext = str(event.get("content", ""))
        if body in event_json or body in ciphertext:
            raise RuntimeError("outbox stored the message body in plaintext")
        if not isinstance(event.get("tags"), list):
            raise RuntimeError("queued event is not a sealed agent message")
        p_tags = [tag[1].lower() for tag in event["tags"] if isinstance(tag, list) and len(tag) >= 2 and tag[0] == "p"]
        sealed = (
            event.get("pubkey", "").lower() == alice_hex
            and event.get("kind") == 30078
            and p_tags == [bob_hex]
            and any(isinstance(tag, list) and len(tag) >= 2 and tag[0] == "enc" and tag[1] == "nip44" for tag in event["tags"])
            and entries[0].get("id") == event_id
            and entries[0].get("recipient_npub") == bob_npub
            and entries[0].get("relays") == [relay.url]
        )
        if not sealed or not verify_event(binary, alice_home, cache, event_json):
            raise RuntimeError("queued event signature or routing did not match the isolated identities")
        if not alice.wait_until(lambda: b"queued for retry" in alice.raw and b"awaiting relay ACK" in alice.raw, 12):
            raise RuntimeError("alice TUI did not show the queued outbox state")
        original_rows = history_rows(alice_home)
        if not rows_are_existing_plaintext(original_rows, body, ciphertext, event_id):
            raise RuntimeError("existing history row was not stored before the process quit")
        persisted_json = event_json
        alice_exit = quit_clean(alice)
        alice_alt = alt_screen_result(bytes(alice.raw))
        alice = None
        if alice_exit != 0 or not alice_alt["clean"]:
            raise RuntimeError("alice offline TUI did not leave the alternate screen on process exit")
        if binary_pids(binary) - before_pids:
            raise RuntimeError("a hyphae process remained after alice quit")

        relay.start()
        alice = start_chat(binary, alice_home, cache, "bob", relay.url)
        if not alice.wait_until(lambda: composer_ready(alice), 15):
            raise RuntimeError("restarted alice TUI did not present the composer")
        captured = relay.wait_event(20)
        mid_entries = load_outbox(alice_home)
        mid_rows = history_rows(alice_home, timeout=0.8)
        if len(mid_entries) != 1 or str(mid_entries[0].get("event_json")) != persisted_json:
            relay.release()
            raise RuntimeError("restart changed the persisted event before ACK")
        if not rows_are_existing_plaintext(mid_rows, body, ciphertext, event_id):
            relay.release()
            raise RuntimeError("existing history row was duplicated or replaced before ACK")
        if not same_event(event, captured):
            relay.release()
            raise RuntimeError("reconnect event id, signature, or ciphertext differed")
        relay.release()
        deadline = time.monotonic() + 12
        while time.monotonic() < deadline and load_outbox(alice_home):
            time.sleep(0.1)
        if load_outbox(alice_home):
            raise RuntimeError("relay ACK did not remove the queued event")
        final_rows = history_rows(alice_home)
        if not rows_are_existing_plaintext(final_rows, body, ciphertext, event_id):
            raise RuntimeError("existing history row was duplicated or replaced after ACK")
        if not alice.wait_until(lambda: b"relay accepted" in alice.raw and b"not confirmed" in alice.raw, 8):
            raise RuntimeError("alice TUI did not show relay acceptance")
        screen_count = alice.screen.text().count(body)

        bob = start_chat(binary, bob_home, cache, "alice", relay.url)
        recipient = "timeout"
        recipient_deadline = time.monotonic() + 20
        while time.monotonic() < recipient_deadline:
            bob.drain(0.2)
            seen = recipient_observation(history_rows(bob_home), body, ciphertext, event_id, bob.screen.text())
            if seen is None:
                continue
            recipient = seen
            break
        bob_exit = quit_clean(bob)
        bob_alt = alt_screen_result(bytes(bob.raw))
        bob = None
        alice_restart_exit = quit_clean(alice)
        alice_restart_alt = alt_screen_result(bytes(alice.raw))
        alice = None
        if bob_exit != 0 or not bob_alt["clean"] or alice_restart_exit != 0 or not alice_restart_alt["clean"]:
            raise RuntimeError("restarted TUI did not leave the alternate screen on process exit")
        if binary_pids(binary) - before_pids:
            raise RuntimeError("a hyphae process remained after the repetition")
        if real_home_stamp(real_home) != real_stamp:
            raise RuntimeError("real hyphae data directory changed")
        checks.update({
            "ok": True,
            "offline_exit": alice_exit,
            "restart_exit": alice_restart_exit,
            "bob_exit": bob_exit,
            "alt_screen_offline_clean": True,
            "alt_screen_restart_clean": True,
            "alt_screen_bob_clean": True,
            "event_id_sha256": sha256_text(event_id),
            "signature_sha256": sha256_text(str(event.get("sig", ""))),
            "ciphertext_sha256": sha256_text(ciphertext),
            "event_json_sha256": sha256_text(persisted_json),
            "history_rows_before_quit": 1,
            "history_rows_before_ack": 1,
            "history_rows_after_ack": 1,
            "history_deleted": False,
            "same_event_on_reconnect": True,
            "relay_ack": True,
            "alice_visible_count": screen_count,
            "recipient": recipient,
            "recipient_event_marker": sha256_text(event_id) if recipient == "pass" else "",
            "daemon_started": False,
            "group_runtime": GROUP_RUNTIME,
        })
        return checks
    finally:
        for session in (alice, bob):
            if session is not None:
                session.close()
        relay.close()
        leftover = binary_pids(binary) - before_pids
        if leftover:
            for pid in leftover:
                try:
                    os.kill(pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            checks["daemon_started"] = True


def assert_worktree_is_test_kit(root: Path) -> None:
    allowed = {
        "scripts/pty_offline_dm_restart.py",
        "scripts/test_pty_offline_dm_restart.py",
    }
    tracked = subprocess.check_output(["git", "diff", "--name-only", "HEAD"], cwd=root, text=True)
    for path in (line.strip() for line in tracked.splitlines()):
        if path and path not in allowed:
            raise RuntimeError("tracked production files differ from HEAD")
    status = subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True)
    for line in status.splitlines():
        path = line[3:].strip() if len(line) > 3 else line.strip()
        if " -> " in path:
            path = path.split(" -> ", 1)[1].strip()
        if path not in allowed:
            raise RuntimeError("worktree contains a change outside the PTY test kit")


def build_cli(root: Path, output: Path) -> None:
    env = os.environ.copy()
    env["PYTHONDONTWRITEBYTECODE"] = "1"
    result = subprocess.run(
        ["go", "build", "-trimpath", "-o", str(output), "./cmd/hyphae"],
        cwd=root,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=180,
        check=False,
    )
    if result.returncode != 0 or not output.is_file():
        raise RuntimeError(f"go build ./cmd/hyphae failed exit {result.returncode}")


def public_receipt(payload: dict[str, object], forbidden: list[str]) -> dict[str, object]:
    encoded = json.dumps(payload, sort_keys=True)
    if any(token in encoded for token in ("pty-dm-", "nsec1", "npub1", "BEGIN PRIVATE")):
        raise RuntimeError("receipt contained private material")
    leaked = sanitize(encoded, forbidden)
    if "<redacted>" in leaked or "<path>" in leaked:
        raise RuntimeError("receipt contained private material")
    return payload


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reps", type=int, default=3)
    parser.add_argument("--receipt", help="write a sanitized JSON receipt")
    parser.add_argument("--bin", help="use this already-built hyphae binary")
    parser.add_argument(
        "--drop-recipient",
        action="store_true",
        help="ACK Alice and withhold the event from Bob's REQ",
    )
    args = parser.parse_args(argv)
    if args.reps < 1 or args.reps > 10:
        print(json.dumps({"status": "fail", "failure": "reps must be from 1 to 10"}))
        return 2
    root = Path(__file__).resolve().parents[1]
    real_home = Path(os.environ.get("HOME", "")).resolve()
    real_stamp = real_home_stamp(real_home)
    forbidden = [str(real_home)]
    try:
        assert_worktree_is_test_kit(root)
        source = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
        with tempfile.TemporaryDirectory(prefix="hyphae-pty-dm-") as temp_name:
            root_temp = Path(temp_name)
            root_temp.chmod(0o700)
            forbidden.append(str(root_temp))
            cache = root_temp / "go-cache"
            cache.mkdir(mode=0o700)
            binary = Path(args.bin).resolve() if args.bin else root_temp / "hyphae"
            if args.bin:
                if not binary.is_file():
                    raise RuntimeError("prebuilt hyphae binary is missing")
            else:
                build_cli(root, binary)
            if real_home in binary.parents:
                raise RuntimeError("refusing to use a binary inside the real HOME")
            go_version = subprocess.check_output(["go", "version"], text=True).strip()
            uname = subprocess.check_output(["uname", "-srm"], text=True).strip()
            reps = []
            for _ in range(args.reps):
                rep = run_rep(
                    binary,
                    cache,
                    root_temp,
                    real_stamp,
                    real_home,
                    drop_recipient=args.drop_recipient,
                )
                reps.append({key: value for key, value in rep.items() if key != "rep"})
                if not rep.get("ok") or rep.get("recipient") != "pass":
                    break
            core_ok = len(reps) == args.reps and all(item.get("ok") for item in reps)
            recipient_states = [str(item.get("recipient")) for item in reps]
            exit_ok = harness_exit_ok(reps, args.reps)
            receipt = {
                "test": "pty-offline-dm-restart",
                "status": "pass" if exit_ok else "fail",
                "exit_requires_recipient": True,
                "drop_recipient": bool(args.drop_recipient),
                "source_commit": source,
                "binary_sha256": sha256_file(binary),
                "go_version": go_version,
                "os": uname,
                "reps_requested": args.reps,
                "reps_completed": len(reps),
                "core": "PASS" if core_ok else "FAIL",
                "offline_enter_persisted": "PASS" if core_ok else "FAIL",
                "process_restart_same_event": "PASS" if core_ok else "FAIL",
                "existing_history_duplicate_suppression": "PASS" if core_ok else "FAIL",
                "relay_ack": "PASS" if core_ok else "FAIL",
                "alt_screen_cleanup": "PASS" if core_ok else "FAIL",
                "no_standalone_daemon": "PASS" if core_ok and all(not item.get("daemon_started") for item in reps) else "FAIL",
                "recipient_tui": "PASS" if exit_ok else "FAIL",
                "recipient_observation": recipient_states,
                "group_runtime": "UNPROVEN",
                "s7": "NOT_CLAIMED",
                "m1": "NOT_CLAIMED",
                "production_tracked_diff": "empty",
                "commands": [
                    "go build -trimpath -o <temp>/hyphae ./cmd/hyphae",
                    "hyphae identity create --nickname <name> --default",
                    "hyphae identity list --json",
                    "hyphae contact add --nickname <name> --npub <public>",
                    "hyphae decode --input <npub>",
                    "hyphae tui chat --with <contact> --relay ws://127.0.0.1:<ephemeral>",
                    "hyphae verify <queued-event-json>",
                ],
                "reps": reps,
            }
            if not exit_ok:
                if core_ok:
                    receipt["failure"] = "recipient TUI did not show the decrypted event"
                else:
                    receipt["failure"] = "one repetition did not meet the PTY restart checks"
            public_receipt(receipt, forbidden)
            if args.receipt:
                output = Path(args.receipt)
                if real_home in output.resolve().parents or output.resolve() == real_home:
                    raise RuntimeError("refusing to write the receipt inside the real HOME")
                output.parent.mkdir(parents=True, exist_ok=True)
                with output.open("x", encoding="utf-8") as stream:
                    os.chmod(output, 0o600)
                    stream.write(json.dumps(receipt, indent=2, sort_keys=True) + "\n")
            print(json.dumps(receipt, sort_keys=True))
            return 0 if exit_ok else 1
    except Exception as exc:
        failure = sanitize(str(exc), forbidden)
        print(json.dumps({"status": "fail", "failure": failure, "group_runtime": "UNPROVEN"}))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
