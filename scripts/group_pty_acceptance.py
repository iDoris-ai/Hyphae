#!/usr/bin/env python3
"""Real local-relay/PTTY acceptance runner for three-identity M1 group chat.

The default invocation is a capability report and exits 77 while group chat is
not wired into the CLI/TUI. ``--execute`` builds (or accepts) real binaries,
creates three isolated identities and publishes their profiles to one local
relay before checking the public group-chat contract. It never fabricates
protocol events or edits application state directly.

Raw PTY, CLI, relay, identity and message data stays in memory or in a private
temporary directory. Optional evidence contains only check names and statuses.
"""

from __future__ import annotations

import argparse
import fcntl
import json
import os
import pty
import select
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import termios
import time
import tty
from dataclasses import dataclass
from pathlib import Path
from typing import Callable


EXIT_NOT_IMPLEMENTED = 77
CHECKS = (
    ("three_isolated_identities", "three isolated HOME identities and profiles registered on local relay"),
    ("invite_accept_activate", "invite stays pending until explicit accept; activation waits for all members"),
    ("three_tui_exactly_once", "three real TUI clients exchange alternating messages exactly once"),
    ("restart_history", "TUI restart restores history without duplicate messages"),
    ("recipient_retry", "offline recipient gets a per-recipient fanout failure and automatic retry"),
    ("relay_outage_retry", "relay outage reports queued, then retries after restart and delivers once"),
    ("negative_roster_hash", "tampered roster or roster hash is rejected without history or state changes"),
    ("negative_wrong_invitee", "invitation for another identity is rejected without history or state changes"),
    ("negative_authority", "forged authority is rejected without history or state changes"),
    ("negative_unknown_sender", "unknown sender is rejected without history or state changes"),
    ("negative_cross_group_replay", "cross-group replay is rejected without history or state changes"),
    ("negative_bad_crypto", "bad signature or ciphertext is rejected without history or state changes"),
    ("negative_unknown_version_field", "unknown version or field is rejected without history or state changes"),
    ("negative_zero_value", "zero-value verification object is rejected without history or state changes"),
)


class Screen:
    """Minimal VT screen buffer used for assertions against the visible TUI."""

    def __init__(self, rows: int = 36, cols: int = 140):
        self.rows, self.cols = rows, cols
        self.cells = [[" "] * cols for _ in range(rows)]
        self.row = self.col = 0
        self.state, self.csi = "text", ""
        self.utf8 = bytearray()

    def feed(self, data: bytes) -> None:
        for byte in data:
            if self.state == "esc":
                self.state = "csi" if byte == ord("[") else "osc" if byte == ord("]") else "text"
                self.csi = ""
                continue
            if self.state == "osc":
                if byte == 7:
                    self.state = "text"
                elif byte == 27:
                    self.state = "osc_esc"
                continue
            if self.state == "osc_esc":
                self.state = "text" if byte == ord("\\") else "osc"
                continue
            if self.state == "csi":
                if 0x40 <= byte <= 0x7E:
                    self._control(chr(byte), self.csi)
                    self.state, self.csi = "text", ""
                elif len(self.csi) < 64:
                    self.csi += chr(byte)
                else:
                    self.state, self.csi = "text", ""
                continue
            if byte == 27:
                self._flush()
                self.state = "esc"
            elif byte == 13:
                self._flush()
                self.col = 0
            elif byte == 10:
                self._flush()
                self.row = min(self.rows - 1, self.row + 1)
            elif byte == 8:
                self._flush()
                self.col = max(0, self.col - 1)
            elif byte >= 32:
                self.utf8.append(byte)
                try:
                    char = self.utf8.decode("utf-8")
                except UnicodeDecodeError as exc:
                    if exc.reason == "unexpected end of data":
                        continue
                    char = "�"
                self.utf8.clear()
                self._put(char)

    def _flush(self) -> None:
        if self.utf8:
            self._put("�")
            self.utf8.clear()

    def _put(self, char: str) -> None:
        if self.col < self.cols:
            self.cells[self.row][self.col] = char
        self.col = min(self.cols - 1, self.col + 1)

    def _control(self, final: str, params: str) -> None:
        args = params.lstrip("?=>").split(";")
        nums = [int(x) if x.isdigit() else 0 for x in args if x != ""]
        n = nums[0] if nums else 0
        if final in ("H", "f"):
            self.row = max(0, min(self.rows - 1, (nums[0] if n else 1) - 1))
            self.col = max(0, min(self.cols - 1, (nums[1] if len(nums) > 1 and nums[1] else 1) - 1))
        elif final == "A":
            self.row = max(0, self.row - max(1, n))
        elif final == "B":
            self.row = min(self.rows - 1, self.row + max(1, n))
        elif final == "C":
            self.col = min(self.cols - 1, self.col + max(1, n))
        elif final == "D":
            self.col = max(0, self.col - max(1, n))
        elif final == "J" and n in (0, 2):
            if n == 2:
                self.cells = [[" "] * self.cols for _ in range(self.rows)]
                self.row = self.col = 0
            else:
                for row in range(self.row, self.rows):
                    start = self.col if row == self.row else 0
                    self.cells[row][start:] = [" "] * (self.cols - start)
        elif final == "K":
            start = 0 if n == 1 else self.col
            end = self.cols if n in (0, 2) else self.col + 1
            self.cells[self.row][start:end] = [" "] * (end - start)

    def text(self) -> str:
        return "\n".join("".join(row).rstrip() for row in self.cells)


class PtyProcess:
    def __init__(self, argv: list[str], env: dict[str, str], rows: int = 36, cols: int = 140):
        self.pid, self.fd = pty.fork()
        self.screen = Screen(rows, cols)
        if self.pid == 0:
            os.environ.clear()
            os.environ.update(env)
            os.execvpe(argv[0], argv, env)
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        os.kill(self.pid, signal.SIGWINCH)
        tty.setraw(self.fd, termios.TCSANOW)

    def drain(self, duration: float = 0.2) -> None:
        deadline = time.monotonic() + duration
        while time.monotonic() < deadline:
            ready, _, _ = select.select([self.fd], [], [], min(0.1, deadline - time.monotonic()))
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

    def wait_for(self, predicate: Callable[[str], bool], timeout: float, label: str) -> str:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            self.drain()
            current = self.screen.text()
            if predicate(current):
                return current
            if self.pid == 0:
                raise RuntimeError(f"PTY child exited while waiting for {label}")
        raise RuntimeError(f"PTY timed out waiting for {label}")

    def wait_exit(self, timeout: float = 8) -> bool:
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
            if not self.wait_exit(3):
                try:
                    os.kill(self.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                if not self.wait_exit(2):
                    raise RuntimeError("owned PTY child survived SIGKILL")
        try:
            os.close(self.fd)
        except OSError:
            pass


def isolated_env(home: Path, cache: Path) -> dict[str, str]:
    env = {k: v for k, v in os.environ.items() if k not in {
        "HOME", "HYPHAE_OUTPUT", "AGENT_SPEAKER_OUTPUT", "XDG_CONFIG_HOME",
        "XDG_DATA_HOME", "XDG_CACHE_HOME",
    }}
    env.update({
        "HOME": str(home), "GOCACHE": str(cache),
        "XDG_CONFIG_HOME": str(home / ".config"),
        "XDG_DATA_HOME": str(home / ".local" / "share"),
        "XDG_CACHE_HOME": str(home / ".cache"),
        "TERM": "xterm-256color", "NO_COLOR": "1",
    })
    return env


def free_port() -> int:
    with socket.socket() as conn:
        conn.bind(("127.0.0.1", 0))
        return int(conn.getsockname()[1])


def run_cli(binary: Path, home: Path, cache: Path, *args: str, timeout: int = 25) -> bytes:
    result = subprocess.run([str(binary), *args], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, env=isolated_env(home, cache), timeout=timeout,
                            check=False)
    if result.returncode:
        # CLI output can contain private material; only report a stable stage label.
        raise RuntimeError(f"CLI command failed at {args[0]} (exit {result.returncode})")
    return result.stdout


@dataclass
class LocalRelay:
    binary: Path
    data_dir: Path
    port: int
    env: dict[str, str]
    proc: subprocess.Popen[bytes] | None = None

    def start(self) -> None:
        self.proc = subprocess.Popen(
            [str(self.binary), "--listen", "127.0.0.1", "--port", str(self.port), "--data-dir", str(self.data_dir)],
            stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            env=self.env, start_new_session=True,
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
        raise RuntimeError("local loopback relay did not listen before timeout")

    def stop(self) -> None:
        if self.proc is None or self.proc.poll() is not None:
            return
        self.proc.send_signal(signal.SIGINT)
        try:
            self.proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait(timeout=2)


class Report:
    def __init__(self):
        self.statuses = {key: "NOT_IMPLEMENTED" for key, _ in CHECKS}
        self.reasons = {key: "group-chat CLI/TUI protocol integration is not available" for key, _ in CHECKS}

    def set(self, key: str, status: str, reason: str = "") -> None:
        if key not in self.statuses or status not in {"PASS", "FAIL", "NOT_IMPLEMENTED"}:
            raise ValueError("invalid acceptance report update")
        self.statuses[key] = status
        self.reasons[key] = reason

    def render(self) -> list[str]:
        return [f"{self.statuses[key]} {key}: {label}" +
                (f" ({self.reasons[key]})" if self.reasons[key] else "")
                for key, label in CHECKS]

    def exit_code(self) -> int:
        if any(value == "FAIL" for value in self.statuses.values()):
            return 1
        if any(value == "NOT_IMPLEMENTED" for value in self.statuses.values()):
            return EXIT_NOT_IMPLEMENTED
        return 0


def build_binaries(root: Path, temp: Path, env: dict[str, str]) -> tuple[Path, Path]:
    cli, relay = temp / "hyphae", temp / "hyphae-relay"
    for package, output in (("./cmd/hyphae", cli), ("./cmd/hyphae-relay", relay)):
        built = subprocess.run(["go", "build", "-o", str(output), package], cwd=root, env=env,
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                               check=False, timeout=180)
        if built.returncode:
            raise RuntimeError(f"local build failed for {package}; command output suppressed")
    return cli, relay


def setup_real_identities(cli: Path, homes: list[Path], cache: Path,
                          relay_url: str) -> bool:
    """Create three real identities and register public profiles on loopback."""
    participants: list[tuple[str, str]] = []
    for index, (nickname, home) in enumerate(zip(("m1-alice", "m1-bob", "m1-carol"), homes)):
        run_cli(cli, home, cache, "identity", "create", "--nickname", nickname, "--default")
        identities = json.loads(run_cli(cli, home, cache, "identity", "list", "--json"))
        own = [item for item in identities.get("data", []) if item.get("nickname") == nickname
               and item.get("default") is True]
        if len(own) != 1 or not str(own[0].get("npub", "")).startswith("npub1"):
            raise RuntimeError("identity list did not expose the expected default public identity")
        participants.append((nickname, own[0]["npub"]))
        # Profiles are public relay events, with only synthetic test descriptions.
        published = json.loads(run_cli(cli, home, cache, "profile", "publish", "--name", nickname,
                                       "--description", f"M1 isolated PTY participant {index + 1}",
                                       "--relay", relay_url, "--json"))
        published_data = published.get("data", {})
        if published_data.get("published_to") != 1 or published_data.get("relay_count") != 1:
            raise RuntimeError("profile was not accepted by the single local relay")
        relay_results = published_data.get("relays", [])
        if len(relay_results) != 1 or relay_results[0].get("url") != relay_url or not relay_results[0].get("ok"):
            raise RuntimeError("profile publication did not confirm the requested local relay")
    if len({home.resolve() for home in homes}) != 3 or len({npub for _, npub in participants}) != 3:
        return False
    # Read back via the relay from an isolated profile DB. This verifies that
    # every profile is discoverable from the shared loopback relay, not merely
    # that each publishing command returned successfully.
    found = json.loads(run_cli(cli, homes[0], cache, "profile", "discover", "--relay", relay_url,
                               "--limit", "20", "--timeout", "2", "--json", timeout=15))
    observed = {(item.get("npub"), item.get("profile", {}).get("name"))
                for item in found.get("data", [])}
    return all((npub, nickname) in observed for nickname, npub in participants)


def verify_evidence_path(path: str | None) -> str | None:
    if not path:
        return None
    output = Path(path).expanduser().resolve()
    try:
        output.relative_to(Path.home().resolve())
    except ValueError:
        pass
    else:
        raise ValueError("refusing evidence under caller HOME")
    if output.exists():
        raise ValueError("refusing to overwrite evidence file")
    return str(output)


def run_execution(args: argparse.Namespace, report: Report) -> None:
    """Perform real bootstrap, then fail closed until public group UI is wired.

    No database edits, test-only commands, event injection, or output-based
    success guesses are used. The group flows remain NOT_IMPLEMENTED until the
    normal CLI/TUI offers the complete user-visible feature contract.
    """
    root = Path(__file__).resolve().parents[1]
    with tempfile.TemporaryDirectory(prefix="hyphae-group-pty-") as temp_name:
        root_temp = Path(temp_name)
        root_temp.chmod(0o700)
        cache = root_temp / "go-cache"
        cache.mkdir(mode=0o700)
        homes = [root_temp / f"home-{name}" for name in ("alice", "bob", "carol")]
        for home in homes:
            home.mkdir(mode=0o700)
        env = isolated_env(homes[0], cache)
        if args.bin_dir:
            bin_dir = Path(args.bin_dir).resolve()
            cli, relay_bin = bin_dir / "hyphae", bin_dir / "hyphae-relay"
            if not cli.is_file() or not relay_bin.is_file():
                raise RuntimeError("--bin-dir must contain hyphae and hyphae-relay")
        else:
            cli, relay_bin = build_binaries(root, root_temp, env)
        port = free_port()
        relay_url = f"ws://127.0.0.1:{port}"
        relay_data = root_temp / "relay-data"
        relay_data.mkdir(mode=0o700)
        relay = LocalRelay(relay_bin, relay_data, port, env)
        try:
            relay.start()
            if setup_real_identities(cli, homes, cache, relay_url):
                report.set("three_isolated_identities", "PASS", "three isolated identities and local-relay profiles created")
            else:
                report.set("three_isolated_identities", "FAIL", "identity HOME isolation check failed")

            # `group create` currently writes local membership and `group chat`
            # is a placeholder. Calling either would make a false acceptance.
            # Probe the actual user-facing help surface without invoking a state
            # mutation; unimplemented behaviors remain explicit in the report.
            help_result = subprocess.run([str(cli), "group", "--help"], stdin=subprocess.DEVNULL,
                                         stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                         env=isolated_env(homes[0], cache), timeout=15, check=False)
            help_text = (help_result.stdout + help_result.stderr).decode("utf-8", "replace").lower()
            if "coming soon" in help_text or "invite" not in help_text or "accept" not in help_text:
                # This is a capability finding, not a hidden assertion failure.
                reason = "public group invite/accept and encrypted TUI contract is absent"
            else:
                reason = "full end-to-end driver awaits a stable user-facing group command contract"
            for key, _ in CHECKS:
                if report.statuses[key] == "NOT_IMPLEMENTED":
                    report.reasons[key] = reason
        finally:
            relay.stop()


def write_evidence(path: str | None, report: Report) -> None:
    if not path:
        return
    output = Path(path)
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("x", encoding="utf-8") as stream:
        os.chmod(output, 0o600)
        json.dump({"test": "group-pty-acceptance", "checks": report.statuses}, stream,
                  sort_keys=True, indent=2)
        stream.write("\n")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--execute", action="store_true",
                        help="run real isolated identities and a local relay before reporting group capability")
    parser.add_argument("--bin-dir", help="directory containing prebuilt hyphae and hyphae-relay (requires --execute)")
    parser.add_argument("--evidence", help="write a status-only JSON file outside caller HOME")
    args = parser.parse_args(argv)
    if args.bin_dir and not args.execute:
        parser.error("--bin-dir requires --execute")
    try:
        args.evidence = verify_evidence_path(args.evidence)
    except ValueError as exc:
        parser.error(str(exc))

    report = Report()
    if args.execute:
        try:
            run_execution(args, report)
        except Exception as exc:
            # Avoid exception text from subprocesses, PTY and paths; status stays
            # explicit and evidence contains no raw process output.
            report.set("three_isolated_identities", "FAIL", type(exc).__name__)
            for key, _ in CHECKS:
                if report.statuses[key] == "NOT_IMPLEMENTED":
                    report.reasons[key] = "real local setup did not complete"
    write_evidence(args.evidence, report)
    print("\n".join(report.render()))
    return report.exit_code()


if __name__ == "__main__":
    raise SystemExit(main())
