#!/usr/bin/env python3
"""POSIX process-tree fixture for owned-group cleanup regression tests."""
from __future__ import annotations

import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path


def stdout_signature() -> tuple[int, int, int]:
    info = os.fstat(1)
    return info.st_dev, info.st_ino, info.st_mode


def holder(ready_fd: int, expected_pgid: int, expected_stdout: tuple[int, int, int]) -> int:
    actual = stdout_signature()
    if os.getpgrp() != expected_pgid or actual != expected_stdout:
        return 4
    os.write(ready_fd, json.dumps({"pgid": os.getpgrp(), "stdout": actual}).encode("ascii"))
    os.close(ready_fd)
    while True:
        time.sleep(30)


def supervisor(state_path: Path, leader_ready_fd: int, expected_pgid: int,
               expected_stdout: tuple[int, int, int]) -> int:
    holder_read, holder_write = os.pipe()

    holder_process: subprocess.Popen[bytes] | None = None

    def on_term(_signum: int, _frame: object) -> None:
        # Install before spawning so even a failed readiness handshake gets a
        # reaping opportunity if this supervisor is TERM'd by test cleanup.
        if holder_process is not None:
            try:
                holder_process.wait(timeout=0.4)
            except subprocess.TimeoutExpired:
                pass
        raise SystemExit(0)

    signal.signal(signal.SIGTERM, on_term)
    holder_process = subprocess.Popen(
        [sys.executable, str(Path(__file__).resolve()), "holder", str(holder_write),
         str(expected_pgid), json.dumps(expected_stdout)],
        pass_fds=(holder_write,), close_fds=True)
    os.close(holder_write)
    state_path.write_text(json.dumps({
        "supervisor_pid": os.getpid(),
        "holder_pid": holder_process.pid,
        "pgid": os.getpgrp(),
    }), encoding="ascii")

    raw = bytearray()
    while b"}" not in raw:
        part = os.read(holder_read, 1)
        if not part:
            holder_process.wait(timeout=1)
            return 5
        raw.extend(part)
    os.close(holder_read)
    details = json.loads(raw.decode("ascii"))
    if details.get("pgid") != expected_pgid or tuple(details.get("stdout", ())) != expected_stdout:
        holder_process.terminate()
        holder_process.wait(timeout=1)
        return 6

    os.write(leader_ready_fd, json.dumps({
        "supervisor_pid": os.getpid(),
        "holder_pid": holder_process.pid,
        "pgid": os.getpgrp(),
        "stdout": expected_stdout,
    }).encode("ascii"))
    os.close(leader_ready_fd)
    while True:
        time.sleep(30)


def leader(marker_path: Path, state_path: Path) -> int:
    pgid = os.getpgrp()
    signature = stdout_signature()
    ready_read, ready_write = os.pipe()
    supervisor_process = subprocess.Popen(
        [sys.executable, str(Path(__file__).resolve()), "supervisor", str(state_path), str(ready_write),
         str(pgid), json.dumps(signature)],
        pass_fds=(ready_write,), close_fds=True)
    os.close(ready_write)

    raw = bytearray()
    while b"}" not in raw:
        part = os.read(ready_read, 1)
        if not part:
            supervisor_process.wait(timeout=1)
            return 7
        raw.extend(part)
    os.close(ready_read)
    details = json.loads(raw.decode("ascii"))
    if details.get("pgid") != pgid or tuple(details.get("stdout", ())) != signature:
        return 8

    marker_path.write_text(json.dumps({
        "leader_pid": os.getpid(),
        "supervisor_pid": details["supervisor_pid"],
        "holder_pid": details["holder_pid"],
        "pgid": pgid,
        "stdout": signature,
    }), encoding="ascii")
    os.write(1, b"fixture-ready\n")
    os._exit(0)


def main() -> int:
    role = sys.argv[1]
    if role == "leader":
        return leader(Path(sys.argv[2]), Path(sys.argv[3]))
    if role == "supervisor":
        return supervisor(Path(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]),
                          tuple(json.loads(sys.argv[5])))
    if role == "holder":
        return holder(int(sys.argv[2]), int(sys.argv[3]), tuple(json.loads(sys.argv[4])))
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
