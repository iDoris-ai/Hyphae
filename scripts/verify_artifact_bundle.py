#!/usr/bin/env python3
"""Validate an R1 bundle against the independently pinned Agent24 lock."""

from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import platform as platform_module
import re
import shutil
import stat
import sys
import tarfile
import tempfile
from typing import Any
import zlib


SOURCE_SHA = "a4aa606eb81d5c040d94c51cdf94553e646d8674"
GO_VERSION = "go1.26.4"
RECIPE = (
    "GOTOOLCHAIN=go1.26.4 CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build "
    "-trimpath -buildvcs=false -ldflags='-buildid=' -o hyphae ./cmd/hyphae"
)
RELAY_RECIPE = (
    "GOTOOLCHAIN=go1.26.4 CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build "
    "-trimpath -buildvcs=false -ldflags='-buildid=' -o hyphae-relay ./cmd/hyphae-relay"
)
PRODUCTION_LOCK_SHA256 = "fbb96d21b72597029826d321d65a7d8a2428a3d9f509d079213bb7808667578a"
PRODUCTION_BINARIES: dict[str, str | None] = {
    "darwin-arm64": "f53c29b31d8ca5eb0124ced246bcff6610f048f18bc8dcc2de27f685dad8b221",
    "linux-x64": "042f6200f43c095cfcec16b31a136467a39b08c810819ab3c875df5cfe0164f6",
    "darwin-x64": None,
    "linux-arm64": None,
}
DARWIN_RELAY_SHA256 = "a012d86e549cbeb564d5a5932c54f9b3511c2434203846537096420c89f36aef"
PRODUCTION_REPOSITORY = "iDoris-ai/hyphae"
PLATFORMS = {
    "darwin-arm64": {"os": "darwin", "arch": "arm64", "suffix": "darwin-arm64"},
    "linux-x64": {"os": "linux", "arch": "amd64", "suffix": "linux-amd64"},
}
ARCHIVE_MEMBERS = {"LICENSE": 0o644, "NOTICE": 0o644, "hyphae": 0o755, "hyphae-relay": 0o755}
MAX_RAW_BYTES = 128 * 1024 * 1024
MAX_ARCHIVE_BYTES = 256 * 1024 * 1024
MAX_ARCHIVE_TOTAL_BYTES = 256 * 1024 * 1024
MAX_ARCHIVE_OVERHEAD_BYTES = 16 * 1024 * 1024
MAX_METADATA_BYTES = 2 * 1024 * 1024


class VerifyError(Exception):
    """A controlled validation failure safe for CI logs."""


class BoundedReader:
    """Limit decompressed tar bytes, including headers and padding."""

    def __init__(self, source: Any, limit: int):
        self.source = source
        self.limit = limit
        self.total = 0

    def read(self, size: int = -1) -> bytes:
        remaining = self.limit - self.total
        request = remaining + 1 if size < 0 else min(size, remaining + 1)
        data = self.source.read(request)
        self.total += len(data)
        require(self.total <= self.limit, "archive-expanded-budget")
        return data


def require(condition: bool, reason: str) -> None:
    if not condition:
        raise VerifyError(reason)


def unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        require(key not in result, "json-duplicate-key")
        result[key] = value
    return result


def reject_constant(value: str) -> None:
    raise VerifyError("json-invalid-constant")


def read_regular(path: Path, limit: int) -> bytes:
    try:
        info = path.lstat()
    except OSError:
        raise VerifyError("file-missing") from None
    require(stat.S_ISREG(info.st_mode) and info.st_size <= limit, "file-type-or-size")
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags)
    except OSError:
        raise VerifyError("file-open-failed") from None
    try:
        opened = os.fstat(descriptor)
        require(stat.S_ISREG(opened.st_mode) and opened.st_size <= limit, "file-changed-or-too-large")
        with os.fdopen(descriptor, "rb", closefd=False) as stream:
            data = stream.read(limit + 1)
        require(len(data) <= limit and len(data) == opened.st_size, "file-changed-or-too-large")
        return data
    finally:
        os.close(descriptor)


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def load_json_bytes(data: bytes, reason: str) -> Any:
    try:
        return json.loads(data.decode("utf-8"), object_pairs_hook=unique_object, parse_constant=reject_constant)
    except VerifyError:
        raise
    except (UnicodeError, json.JSONDecodeError):
        raise VerifyError(reason) from None


def digest_file(path: Path, limit: int) -> tuple[str, int]:
    try:
        info = path.lstat()
    except OSError:
        raise VerifyError("artifact-missing") from None
    require(stat.S_ISREG(info.st_mode) and info.st_size <= limit, "artifact-type-or-size")
    digest = hashlib.sha256()
    size = 0
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags)
        with os.fdopen(descriptor, "rb") as stream:
            opened = os.fstat(stream.fileno())
            require(stat.S_ISREG(opened.st_mode) and opened.st_size <= limit, "artifact-changed-or-too-large")
            for block in iter(lambda: stream.read(1024 * 1024), b""):
                size += len(block)
                require(size <= limit, "artifact-too-large")
                digest.update(block)
            require(size == opened.st_size, "artifact-changed-or-too-large")
    except OSError:
        raise VerifyError("artifact-read-failed") from None
    return digest.hexdigest(), size


def host_platform() -> str:
    machine = platform_module.machine().lower()
    if sys.platform == "darwin" and machine in ("arm64", "aarch64"):
        return "darwin-arm64"
    if sys.platform.startswith("linux") and machine in ("x86_64", "amd64"):
        return "linux-x64"
    return "unsupported"


def validate_lock_bytes(data: bytes, expected_sha256: str, expected_lock: dict[str, Any]) -> dict[str, Any]:
    require(sha256_bytes(data) == expected_sha256, "production-lock-sha256")
    lock = load_json_bytes(data, "production-lock-json")
    require(type(lock) is dict and lock == expected_lock, "production-lock-baseline")
    return lock


def artifact_names() -> set[str]:
    names = {"artifact-manifest.json", "hyphae.lock.json", "SHA256SUMS"}
    for record in PLATFORMS.values():
        suffix = record["suffix"]
        names.update(
            {
                f"hyphae-{suffix}",
                f"hyphae-relay-{suffix}",
                f"hyphae-{suffix}.tar.gz",
            }
        )
    return names


def validate_bundle_directory(bundle: Path) -> None:
    try:
        root_info = bundle.lstat()
    except OSError:
        raise VerifyError("bundle-directory-missing") from None
    require(stat.S_ISDIR(root_info.st_mode) and not bundle.is_symlink(), "bundle-directory-invalid")
    expected_names = artifact_names()
    try:
        actual_names = {item.name for item in bundle.iterdir()}
    except OSError:
        raise VerifyError("bundle-directory-unreadable") from None
    require(actual_names == expected_names, "bundle-file-set")
    for name in sorted(expected_names):
        limit = MAX_METADATA_BYTES if name.endswith(".json") or name == "SHA256SUMS" else (
            MAX_ARCHIVE_BYTES if name.endswith(".tar.gz") else MAX_RAW_BYTES
        )
        digest_file(bundle / name, limit)
        if name.startswith("hyphae-") and not name.endswith(".tar.gz"):
            mode = stat.S_IMODE((bundle / name).lstat().st_mode)
            require(mode == 0o755, "raw-binary-mode")


def parse_checksum_file(data: bytes, names: set[str]) -> dict[str, str]:
    try:
        text = data.decode("ascii")
    except UnicodeError:
        raise VerifyError("sha256sums-encoding") from None
    rows: dict[str, str] = {}
    for line in text.splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9._-]+)", line)
        require(match is not None, "sha256sums-line")
        digest, name = match.groups()
        require(name not in rows, "sha256sums-duplicate")
        rows[name] = digest
    require(set(rows) == names - {"SHA256SUMS"}, "sha256sums-file-set")
    return rows


def valid_hash(value: Any) -> bool:
    return type(value) is str and re.fullmatch(r"[0-9a-f]{64}", value) is not None


def validate_manifest(manifest: Any, lock: dict[str, Any], checksums: dict[str, str], *, source_sha: str = SOURCE_SHA, go_version: str = GO_VERSION, recipe: str = RECIPE, relay_recipe: str = RELAY_RECIPE, platforms: dict[str, dict[str, str]] = PLATFORMS) -> dict[str, dict[str, Any]]:
    fields = {"schema", "release_tag", "publication_status", "source", "go", "recipe", "relay_recipe", "platforms", "intended_urls"}
    require(type(manifest) is dict and set(manifest) == fields, "manifest-fields")
    require(type(manifest["schema"]) is int and manifest["schema"] == 1, "manifest-schema")
    require(manifest["source"] == {"sha": source_sha, "clean": True}, "manifest-source")
    require(manifest["go"] == go_version, "manifest-go")
    require(manifest["recipe"] == recipe and manifest["relay_recipe"] == relay_recipe, "manifest-recipe")
    require(manifest["publication_status"] == "intended; not published by this tool", "manifest-publication-status")
    tag = manifest["release_tag"]
    require(type(tag) is str and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", tag) is not None, "manifest-release-tag")
    urls = manifest["intended_urls"]
    require(type(urls) is dict and set(urls) == {"release", "assets"}, "manifest-urls")
    release = f"https://github.com/{PRODUCTION_REPOSITORY}/releases/tag/{tag}"
    base = f"https://github.com/{PRODUCTION_REPOSITORY}/releases/download/{tag}"
    require(urls["release"] == release, "manifest-release-url")
    asset_names = artifact_names() - {"SHA256SUMS"}
    asset_names.add("SHA256SUMS")
    require(type(urls["assets"]) is dict, "manifest-assets")
    require(urls["assets"] == {name: f"{base}/{name}" for name in sorted(asset_names)}, "manifest-asset-urls")

    records = manifest["platforms"]
    require(type(records) is dict and set(records) == set(platforms), "manifest-platform-set")
    result: dict[str, dict[str, Any]] = {}
    for key, spec in platforms.items():
        record = records[key]
        require(type(record) is dict, "manifest-platform-fields")
        require(type(record) is dict and set(record) == {"os", "arch", "binaries", "archive"}, "manifest-platform-fields")
        require(record["os"] == spec["os"] and record["arch"] == spec["arch"], "manifest-platform-target")
        binaries = record["binaries"]
        require(type(binaries) is dict and set(binaries) == {"hyphae", "hyphae-relay"}, "manifest-binary-fields")
        names = {
            "hyphae": f"hyphae-{spec['suffix']}",
            "hyphae-relay": f"hyphae-relay-{spec['suffix']}",
        }
        for binary_name, filename in names.items():
            meta = binaries[binary_name]
            require(type(meta) is dict and set(meta) == {"filename", "sha256"}, "manifest-binary-metadata")
            require(meta["filename"] == filename and valid_hash(meta["sha256"]), "manifest-binary-value")
            require(checksums.get(filename) == meta["sha256"], "manifest-sha256sums-binary")
        archive = record["archive"]
        archive_name = f"hyphae-{spec['suffix']}.tar.gz"
        require(type(archive) is dict and set(archive) == {"filename", "sha256"}, "manifest-archive-fields")
        require(archive["filename"] == archive_name and valid_hash(archive["sha256"]), "manifest-archive-value")
        require(checksums.get(archive_name) == archive["sha256"], "manifest-sha256sums-archive")
        result[key] = {"record": record, "names": names}
    return result


def validate_archive(path: Path, expected_archive_sha: str, raw_digests: dict[str, tuple[str, int]]) -> None:
    actual_archive_sha, _ = digest_file(path, MAX_ARCHIVE_BYTES)
    require(actual_archive_sha == expected_archive_sha, "archive-sha256")
    try:
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(descriptor, "rb") as stream:
            header = stream.read(10)
    except OSError:
        raise VerifyError("archive-open-failed") from None
    require(len(header) == 10 and header[:4] == b"\x1f\x8b\x08\x00" and header[4:8] == b"\x00\x00\x00\x00", "archive-gzip-metadata")
    total = 0
    seen: dict[str, tuple[str, int]] = {}
    member_order: list[str] = []
    try:
        with path.open("rb") as compressed:
            with gzip.GzipFile(fileobj=compressed, mode="rb") as decompressed:
                bounded = BoundedReader(decompressed, MAX_ARCHIVE_TOTAL_BYTES + MAX_ARCHIVE_OVERHEAD_BYTES)
                with tarfile.open(fileobj=bounded, mode="r|") as archive:
                    for member in archive:
                        require(member.name in ARCHIVE_MEMBERS and member.name not in seen, "archive-member-name")
                        require(len(seen) < len(ARCHIVE_MEMBERS), "archive-member-count")
                        member_order.append(member.name)
                        require(member.isreg() and member.type in (tarfile.REGTYPE, tarfile.AREGTYPE), "archive-member-type")
                        expected_mode = ARCHIVE_MEMBERS[member.name]
                        require(member.mode == expected_mode, "archive-member-mode")
                        require(member.mtime == 0 and member.uid == 0 and member.gid == 0 and member.uname == "" and member.gname == "", "archive-member-metadata")
                        require(0 <= member.size <= MAX_RAW_BYTES, "archive-member-size")
                        total += member.size
                        require(total <= MAX_ARCHIVE_TOTAL_BYTES, "archive-expanded-size")
                        stream = archive.extractfile(member)
                        require(stream is not None, "archive-member-unreadable")
                        digest = hashlib.sha256()
                        size = 0
                        with stream:
                            for block in iter(lambda: stream.read(1024 * 1024), b""):
                                size += len(block)
                                require(size <= member.size and size <= MAX_RAW_BYTES, "archive-member-size")
                                digest.update(block)
                        require(size == member.size, "archive-member-truncated")
                        seen[member.name] = (digest.hexdigest(), size)
    except VerifyError:
        raise
    except (OSError, EOFError, tarfile.TarError, gzip.BadGzipFile, zlib.error):
        raise VerifyError("archive-invalid") from None
    require(set(seen) == set(ARCHIVE_MEMBERS), "archive-member-set")
    require(member_order == sorted(ARCHIVE_MEMBERS), "archive-member-order")
    for name in ("hyphae", "hyphae-relay"):
        require(seen[name] == raw_digests[name], "archive-raw-binary-mismatch")


def parse_lock_bundle(data: bytes, production_lock: dict[str, Any]) -> None:
    bundle_lock = load_json_bytes(data, "bundle-lock-json")
    require(type(bundle_lock) is dict and set(bundle_lock) == set(production_lock), "bundle-lock-fields")
    require(bundle_lock == production_lock, "bundle-lock-baseline")


def verify_bundle(
    bundle: Path,
    production_lock_path: Path,
    *,
    expected_lock_sha256: str = PRODUCTION_LOCK_SHA256,
    expected_lock: dict[str, Any] | None = None,
    expected_source_sha: str = SOURCE_SHA,
    expected_go: str = GO_VERSION,
    expected_recipe: str = RECIPE,
    expected_relay_recipe: str = RELAY_RECIPE,
    expected_platforms: dict[str, dict[str, str]] = PLATFORMS,
    expected_darwin_relay_sha256: str | None = DARWIN_RELAY_SHA256,
) -> dict[str, Any]:
    if expected_lock is None:
        expected_lock = {
            "schema": 1,
            "source_sha": expected_source_sha,
            "go": expected_go,
            "recipe": expected_recipe,
            "binaries": PRODUCTION_BINARIES,
        }
    production_lock = validate_lock_bytes(read_regular(production_lock_path, 64 * 1024), expected_lock_sha256, expected_lock)
    validate_bundle_directory(bundle)
    manifest = load_json_bytes(read_regular(bundle / "artifact-manifest.json", MAX_METADATA_BYTES), "manifest-json")
    checksums = parse_checksum_file(read_regular(bundle / "SHA256SUMS", MAX_METADATA_BYTES), artifact_names())
    for filename, expected in checksums.items():
        limit = MAX_ARCHIVE_BYTES if filename.endswith(".tar.gz") else (MAX_METADATA_BYTES if filename.endswith(".json") else MAX_RAW_BYTES)
        actual, _ = digest_file(bundle / filename, limit)
        require(actual == expected, "sha256sums-file-digest")
    records = validate_manifest(
        manifest, production_lock, checksums, source_sha=expected_source_sha,
        go_version=expected_go, recipe=expected_recipe,
        relay_recipe=expected_relay_recipe, platforms=expected_platforms,
    )
    parse_lock_bundle(read_regular(bundle / "hyphae.lock.json", MAX_METADATA_BYTES), production_lock)

    platform_digests: dict[str, dict[str, tuple[str, int]]] = {}
    for key, info in records.items():
        record = info["record"]
        raw_digests: dict[str, tuple[str, int]] = {}
        for binary_name, bundle_filename in info["names"].items():
            digest, size = digest_file(bundle / bundle_filename, MAX_RAW_BYTES)
            expected_manifest_digest = record["binaries"][binary_name]["sha256"]
            require(digest == expected_manifest_digest, "raw-binary-manifest-sha256")
            raw_digests[binary_name] = (digest, size)
            if binary_name == "hyphae":
                require(digest == production_lock["binaries"][key], "production-cli-sha256")
            elif key == "darwin-arm64" and expected_darwin_relay_sha256 is not None:
                require(digest == expected_darwin_relay_sha256, "production-darwin-relay-sha256")
        archive_name = record["archive"]["filename"]
        tar_raw = {name: raw_digests[name] for name in ("hyphae", "hyphae-relay")}
        validate_archive(bundle / archive_name, record["archive"]["sha256"], tar_raw)
        platform_digests[key] = raw_digests
    return {
        "source_sha": expected_source_sha,
        "go": expected_go,
        "recipe": expected_recipe,
        "platforms": {
            key: {
                "cli_sha256": platform_digests[key]["hyphae"][0],
                "relay_sha256": platform_digests[key]["hyphae-relay"][0],
                "archive_sha256": records[key]["record"]["archive"]["sha256"],
            }
            for key in expected_platforms
        },
        "_raw_files": {
            key: {name: value for name, value in platform_digests[key].items()}
            for key in expected_platforms
        },
        "published": False,
    }


def extract_binaries(bundle: Path, platform_name: str, destination: Path, raw_names: dict[str, str], expected: dict[str, tuple[str, int]]) -> Path:
    output = destination.expanduser().absolute()
    if not output.parent.is_dir():
        raise VerifyError("extract-parent-missing")
    output = output.parent.resolve() / output.name
    bundle = bundle.resolve(strict=True)
    require(output not in bundle.parents and bundle not in output.parents and output != bundle, "extract-overlaps-bundle")
    require(not os.path.lexists(output), "extract-output-exists")
    with tempfile.TemporaryDirectory(prefix=".hyphae-verified-", dir=output.parent) as temp_name:
        staging = Path(temp_name)
        for output_name, source_name in (("hyphae", raw_names["hyphae"]), ("hyphae-relay", raw_names["hyphae-relay"])):
            source = bundle / source_name
            staged = staging / output_name
            try:
                descriptor = os.open(source, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
                digest = hashlib.sha256()
                size = 0
                with os.fdopen(descriptor, "rb") as reader, staged.open("xb") as writer:
                    if not stat.S_ISREG(os.fstat(reader.fileno()).st_mode):
                        raise VerifyError("binary-extract-type")
                    for block in iter(lambda: reader.read(1024 * 1024), b""):
                        size += len(block)
                        require(size <= expected[output_name][1], "binary-extract-size")
                        digest.update(block)
                        writer.write(block)
                require((digest.hexdigest(), size) == expected[output_name], "binary-extract-changed")
                staged.chmod(0o755)
            except OSError:
                raise VerifyError("binary-extract-failed") from None
        require(not os.path.lexists(output), "extract-output-appeared")
        output.mkdir()
        created: list[Path] = []
        try:
            for staged in sorted(staging.iterdir(), key=lambda item: item.name):
                target = output / staged.name
                with staged.open("rb") as reader, target.open("xb") as writer:
                    created.append(target)
                    shutil.copyfileobj(reader, writer)
                target.chmod(0o755)
                digest, size = digest_file(target, MAX_RAW_BYTES)
                require((digest, size) == expected[staged.name], "extract-final-changed")
        except Exception:
            for path in created:
                path.unlink(missing_ok=True)
            try:
                output.rmdir()
            except OSError:
                pass
            raise
    return output


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("--bundle-dir", type=Path, required=True)
    result.add_argument("--production-lock", type=Path, required=True)
    result.add_argument("--platform", choices=("all", "auto", *PLATFORMS.keys()), required=True)
    result.add_argument("--extract-dir", type=Path)
    return result


def main(argv: list[str] | None = None) -> int:
    args = parser().parse_args(argv)
    try:
        result = verify_bundle(args.bundle_dir, args.production_lock)
        selected = host_platform() if args.platform == "auto" else args.platform
        if args.platform != "all":
            require(selected in PLATFORMS and selected == host_platform(), "host-platform-mismatch")
            require(args.extract_dir is not None, "extract-dir-required")
            extracted = extract_binaries(args.bundle_dir, selected, args.extract_dir, {
                "hyphae": f"hyphae-{PLATFORMS[selected]['suffix']}",
                "hyphae-relay": f"hyphae-relay-{PLATFORMS[selected]['suffix']}",
            }, result["_raw_files"][selected])
            result.pop("_raw_files", None)
            result["selected_platform"] = selected
            result["extract_dir"] = str(extracted)
        else:
            require(args.extract_dir is None, "extract-not-allowed-with-all")
            result["selected_platform"] = None
            result["extract_dir"] = None
        print(json.dumps({"ok": True, "data": result}, sort_keys=True))
        return 0
    except VerifyError as exc:
        print(f"artifact verification failed: {exc}", file=sys.stderr)
        return 1
    except (OSError, tarfile.TarError, gzip.BadGzipFile, EOFError):
        print("artifact verification failed: filesystem-or-archive-error", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
