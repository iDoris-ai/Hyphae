#!/usr/bin/env python3
"""Small, strict helpers for the fixed artifact CI workflow."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
from typing import Any

if __package__:
    from . import verify_artifact_bundle as verifier
else:
    import verify_artifact_bundle as verifier

SOURCE_SHA = verifier.SOURCE_SHA
GO_VERSION = verifier.GO_VERSION
RECIPE = verifier.RECIPE
LOCK_SHA256 = verifier.PRODUCTION_LOCK_SHA256
CLI_HASHES = verifier.PRODUCTION_BINARIES
DARWIN_RELAY_SHA256 = verifier.DARWIN_RELAY_SHA256
PLATFORMS = set(verifier.PLATFORMS)


class InputError(Exception):
    pass


def require(condition: bool, label: str) -> None:
    if not condition:
        raise InputError(label)


def unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    value: dict[str, Any] = {}
    for key, item in pairs:
        require(key not in value, "duplicate-json-key")
        value[key] = item
    return value


def read_regular(path: Path, limit: int) -> bytes:
    try:
        info = path.lstat()
    except OSError:
        raise InputError("input-missing") from None
    require(stat.S_ISREG(info.st_mode) and info.st_size <= limit, "input-type-or-size")
    try:
        with path.open("rb") as stream:
            data = stream.read(limit + 1)
    except OSError:
        raise InputError("input-unreadable") from None
    require(len(data) <= limit and len(data) == info.st_size, "input-changed-or-too-large")
    return data


def check_lock(path: Path) -> None:
    raw = read_regular(path, 64 * 1024)
    require(hashlib.sha256(raw).hexdigest() == LOCK_SHA256, "production-lock-sha256")
    try:
        lock = json.loads(raw.decode("utf-8"), object_pairs_hook=unique_object)
    except (UnicodeError, json.JSONDecodeError):
        raise InputError("production-lock-json") from None
    expected = {
        "schema": 1,
        "source_sha": SOURCE_SHA,
        "go": GO_VERSION,
        "recipe": RECIPE,
        "binaries": CLI_HASHES,
    }
    require(type(lock) is dict and lock == expected, "production-lock-baseline")


def prepare_download(bundle: Path) -> None:
    """Restore executable mode lost by Actions ZIP transport, then defer all trust to R2a."""
    try:
        root = bundle.lstat()
        names = {item.name for item in bundle.iterdir()}
    except OSError:
        raise InputError("bundle-directory-unreadable") from None
    require(stat.S_ISDIR(root.st_mode) and names == verifier.artifact_names(), "bundle-file-set")
    raw_names = [
        f"hyphae-{platform['suffix']}"
        for platform in verifier.PLATFORMS.values()
    ] + [
        f"hyphae-relay-{platform['suffix']}"
        for platform in verifier.PLATFORMS.values()
    ]
    descriptors: list[int] = []
    try:
        for name in raw_names:
            try:
                descriptor = os.open(bundle / name, os.O_RDONLY | os.O_NONBLOCK | getattr(os, "O_NOFOLLOW", 0))
            except OSError:
                raise InputError("raw-binary-open-failed") from None
            descriptors.append(descriptor)
            info = os.fstat(descriptor)
            require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1 and 0 < info.st_size <= verifier.MAX_RAW_BYTES, "raw-binary-type-or-size")
        for descriptor in descriptors:
            os.fchmod(descriptor, 0o755)
    finally:
        for descriptor in descriptors:
            os.close(descriptor)


def relay_hash(path: Path, platform: str) -> str:
    require(platform in PLATFORMS, "unsupported-platform")
    raw = read_regular(path, 64 * 1024)
    try:
        envelope = json.loads(raw.decode("utf-8"), object_pairs_hook=unique_object)
    except (UnicodeError, json.JSONDecodeError):
        raise InputError("verification-json") from None
    require(type(envelope) is dict and set(envelope) == {"ok", "data"} and envelope["ok"] is True, "verification-envelope")
    data = envelope["data"]
    require(type(data) is dict, "verification-data")
    require(data.get("source_sha") == SOURCE_SHA and data.get("go") == GO_VERSION and data.get("recipe") == RECIPE, "verification-baseline")
    require(data.get("published") is False, "verification-publication-status")
    require(data.get("selected_platform") in (None, platform), "verification-selected-platform")
    records = data.get("platforms")
    require(type(records) is dict and set(records) == PLATFORMS, "verification-platform-set")
    record = records[platform]
    require(type(record) is dict, "verification-platform-record")
    digest = record.get("relay_sha256")
    require(type(digest) is str and re.fullmatch(r"[0-9a-f]{64}", digest) is not None, "relay-sha256")
    if platform == "darwin-arm64":
        require(digest == DARWIN_RELAY_SHA256, "darwin-relay-baseline")
    return digest


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    lock = commands.add_parser("check-lock")
    lock.add_argument("--path", type=Path, required=True)
    relay = commands.add_parser("relay-sha")
    relay.add_argument("--verification-json", type=Path, required=True)
    relay.add_argument("--platform", choices=sorted(PLATFORMS), required=True)
    prepare = commands.add_parser("prepare-download")
    prepare.add_argument("--bundle-dir", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        if args.command == "check-lock":
            check_lock(args.path)
            return 0
        if args.command == "prepare-download":
            prepare_download(args.bundle_dir)
            return 0
        print(relay_hash(args.verification_json, args.platform))
        return 0
    except InputError as error:
        print(f"artifact CI input failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
