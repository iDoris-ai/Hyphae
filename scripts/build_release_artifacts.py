#!/usr/bin/env python3
"""Build local, reproducible Hyphae release candidates from a clean Git checkout."""

from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
from typing import Any


GO_VERSION = "go1.26.4"
REPOSITORY = "iDoris-ai/hyphae"
RECIPE = (
    "GOTOOLCHAIN=go1.26.4 CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build "
    "-trimpath -buildvcs=false -ldflags='-buildid=' -o hyphae ./cmd/hyphae"
)
RELAY_RECIPE = (
    "GOTOOLCHAIN=go1.26.4 CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build "
    "-trimpath -buildvcs=false -ldflags='-buildid=' -o hyphae-relay ./cmd/hyphae-relay"
)
PLATFORMS = (
    {"os": "darwin", "arch": "arm64", "lock_key": "darwin-arm64"},
    {"os": "linux", "arch": "amd64", "lock_key": "linux-x64"},
)
LOCK_PLATFORM_KEYS = ("darwin-arm64", "linux-x64", "darwin-x64", "linux-arm64")


class BuildError(Exception):
    """A fail-closed validation, build, or packaging error."""


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def run_git(source: Path, *args: str) -> str:
    env = os.environ.copy()
    for key in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"):
        env.pop(key, None)
    result = subprocess.run(
        ["git", "-C", str(source), *args],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=env,
    )
    if result.returncode:
        raise BuildError(f"git {' '.join(args)} failed: {result.stderr.strip()}")
    return result.stdout.strip()


def validate_source(source: Path, expected_sha: str) -> str:
    if not source.is_dir():
        raise BuildError("source-dir must be an existing directory")
    try:
        source = source.resolve(strict=True)
    except OSError as exc:
        raise BuildError(f"source-dir is not accessible: {exc}") from exc
    if run_git(source, "rev-parse", "--is-inside-work-tree") != "true":
        raise BuildError("source-dir is not inside a Git working tree")
    top = Path(run_git(source, "rev-parse", "--show-toplevel")).resolve()
    if source != top:
        raise BuildError("source-dir must be the Git checkout root")
    head = run_git(source, "rev-parse", "--verify", "HEAD^{commit}")
    if head != expected_sha:
        raise BuildError(f"source HEAD mismatch: expected {expected_sha}, got {head}")
    dirty = run_git(
        source,
        "status",
        "--porcelain=v1",
        "--untracked-files=all",
        "--ignored=matching",
        "--ignore-submodules=none",
    )
    if dirty:
        raise BuildError("source checkout has tracked or untracked changes")
    for relative in ("cmd/hyphae", "cmd/hyphae-relay"):
        if not (source / relative).is_dir():
            raise BuildError(f"required build target is missing: {relative}")
    for relative in ("LICENSE", "NOTICE"):
        item = source / relative
        if not item.is_file() or item.is_symlink():
            raise BuildError(f"required regular file is missing: {relative}")
    return head


def selected_go(env: dict[str, str]) -> tuple[str, str]:
    executable = shutil.which("go")
    if not executable:
        raise BuildError("Go is not available on PATH")
    env = env.copy()
    env["GOTOOLCHAIN"] = "local"
    result = subprocess.run(
        [executable, "version"],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=env,
    )
    output = result.stdout.strip()
    version_fields = output.split()
    actual_version = version_fields[2] if len(version_fields) >= 4 and version_fields[:2] == ["go", "version"] else ""
    if result.returncode or actual_version != GO_VERSION:
        detail = result.stderr.strip() or output or f"exit {result.returncode}"
        raise BuildError(f"requires local {GO_VERSION}; refusing toolchain download/switch ({detail})")
    return executable, GO_VERSION


def go_environment(*, toolchain: str, home: Path, cache: Path, platform: dict[str, str] | None = None) -> dict[str, str]:
    """Keep build results independent of user Go/workspace overrides."""
    env = {key: os.environ[key] for key in ("PATH", "GOPATH") if key in os.environ}
    env.setdefault("GOPATH", str(Path.home() / "go"))
    env.update(
        {
            "HOME": str(home),
            "GOCACHE": str(cache),
            "GOTOOLCHAIN": toolchain,
            "GOENV": "off",
            "GOWORK": "off",
        }
    )
    if platform is not None:
        env.update(
            {
                "CGO_ENABLED": "0",
                "GOOS": platform["os"],
                "GOARCH": platform["arch"],
            }
        )
    return env


def build_binary(go: str, source: Path, output: Path, platform: dict[str, str], target: str, name: str, base_env: dict[str, str]) -> None:
    env = base_env.copy()
    env.update({"GOTOOLCHAIN": GO_VERSION, "CGO_ENABLED": "0", "GOOS": platform["os"], "GOARCH": platform["arch"]})
    output.parent.mkdir(parents=True, exist_ok=True)
    command = [
        go,
        "build",
        "-trimpath",
        "-buildvcs=false",
        "-ldflags=-buildid=",
        "-o",
        str(output),
        target,
    ]
    result = subprocess.run(command, cwd=source, env=env, check=False, text=True, capture_output=True)
    if result.returncode:
        detail = result.stderr.strip() or result.stdout.strip() or f"exit {result.returncode}"
        raise BuildError(f"{platform['os']}/{platform['arch']} {name} build failed: {detail}")
    try:
        info = output.lstat()
    except OSError as exc:
        raise BuildError(f"Go did not produce {name}: {exc}") from exc
    if not stat.S_ISREG(info.st_mode) or info.st_size == 0:
        raise BuildError(f"Go output is not a non-empty regular file: {name}")
    output.chmod(0o755)


def write_archive(output: Path, source: Path, hyphae: Path, relay: Path) -> None:
    members = {
        "LICENSE": source / "LICENSE",
        "NOTICE": source / "NOTICE",
        "hyphae": hyphae,
        "hyphae-relay": relay,
    }
    with output.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", compresslevel=9, fileobj=raw, mtime=0) as gz:
            with tarfile.open(fileobj=gz, mode="w|", format=tarfile.USTAR_FORMAT) as archive:
                for name in sorted(members):
                    path = members[name]
                    info = tarfile.TarInfo(name=name)
                    info.size = path.stat().st_size
                    info.mtime = 0
                    info.uid = 0
                    info.gid = 0
                    info.uname = ""
                    info.gname = ""
                    info.mode = 0o755 if name in ("hyphae", "hyphae-relay") else 0o644
                    with path.open("rb") as stream:
                        archive.addfile(info, stream)


def write_json(path: Path, value: Any) -> None:
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def build_bundle(source: Path, source_sha: str, output: Path, release_tag: str) -> None:
    source = source.resolve(strict=True)
    output = output.expanduser().absolute()
    if not output.parent.is_dir():
        raise BuildError("output parent must already exist")
    output = output.parent.resolve() / output.name
    if os.path.lexists(output):
        raise BuildError("output must be a new, nonexistent directory")
    if source == output or source in output.parents or output in source.parents:
        raise BuildError("output and source checkout must not overlap")

    head = validate_source(source, source_sha)
    with tempfile.TemporaryDirectory(prefix=".hyphae-artifacts-", dir=output.parent) as temporary:
        staging = Path(temporary)
        build_home = staging / "home"
        build_cache = staging / "go-cache"
        artifacts = staging / "artifacts"
        build_home.mkdir()
        build_cache.mkdir()
        artifacts.mkdir()
        base_env = go_environment(toolchain="local", home=build_home, cache=build_cache)
        go, go_version = selected_go(base_env)
        records: dict[str, dict[str, Any]] = {}
        hashes: dict[str, str] = {}
        for platform in PLATFORMS:
            suffix = f"{platform['os']}-{platform['arch']}"
            raw_cli_name = f"hyphae-{suffix}"
            raw_relay_name = f"hyphae-relay-{suffix}"
            cli = artifacts / raw_cli_name
            relay = artifacts / raw_relay_name
            build_binary(go, source, cli, platform, "./cmd/hyphae", "hyphae", base_env)
            build_binary(go, source, relay, platform, "./cmd/hyphae-relay", "hyphae-relay", base_env)
            archive_name = f"hyphae-{suffix}.tar.gz"
            archive = artifacts / archive_name
            write_archive(archive, source, cli, relay)
            records[platform["lock_key"]] = {
                "os": platform["os"],
                "arch": platform["arch"],
                "binaries": {
                    "hyphae": {"filename": raw_cli_name, "sha256": sha256_file(cli)},
                    "hyphae-relay": {"filename": raw_relay_name, "sha256": sha256_file(relay)},
                },
                "archive": {"filename": archive_name, "sha256": sha256_file(archive)},
            }
            hashes[raw_cli_name] = sha256_file(cli)
            hashes[raw_relay_name] = sha256_file(relay)
            hashes[archive_name] = sha256_file(archive)

        intended_release_url = f"https://github.com/{REPOSITORY}/releases/tag/{release_tag}"
        intended_download_base = f"https://github.com/{REPOSITORY}/releases/download/{release_tag}"
        manifest = {
            "schema": 1,
            "release_tag": release_tag,
            "publication_status": "intended; not published by this tool",
            "source": {"sha": head, "clean": True},
            "go": go_version,
            "recipe": RECIPE,
            "relay_recipe": RELAY_RECIPE,
            "platforms": records,
            "intended_urls": {
                "release": intended_release_url,
                "assets": {
                    name: f"{intended_download_base}/{name}"
                    for name in sorted(
                        [
                            *hashes.keys(),
                            "artifact-manifest.json",
                            "hyphae.lock.json",
                            "SHA256SUMS",
                        ]
                    )
                },
            },
        }
        write_json(artifacts / "artifact-manifest.json", manifest)

        lock_binaries: dict[str, str | None] = {key: None for key in LOCK_PLATFORM_KEYS}
        for platform in PLATFORMS:
            lock_key = platform["lock_key"]
            lock_binaries[lock_key] = records[lock_key]["binaries"]["hyphae"]["sha256"]
        lock = {"schema": 1, "source_sha": head, "go": go_version, "recipe": RECIPE, "binaries": lock_binaries}
        write_json(artifacts / "hyphae.lock.json", lock)
        hashes["artifact-manifest.json"] = sha256_file(artifacts / "artifact-manifest.json")
        hashes["hyphae.lock.json"] = sha256_file(artifacts / "hyphae.lock.json")
        (artifacts / "SHA256SUMS").write_text(
            "".join(f"{digest}  {name}\n" for name, digest in sorted(hashes.items())), encoding="ascii"
        )

        if validate_source(source, source_sha) != head:
            raise BuildError("source HEAD changed during build")
        if os.path.lexists(output):
            raise BuildError("output appeared during build; refusing to overwrite it")
        output.mkdir()
        created: list[Path] = []
        try:
            for item in sorted(artifacts.iterdir(), key=lambda path: path.name):
                destination = output / item.name
                with item.open("rb") as reader, destination.open("xb") as writer:
                    shutil.copyfileobj(reader, writer)
                created.append(destination)
                destination.chmod(stat.S_IMODE(item.stat().st_mode))
        except Exception:
            # Leave anything inserted by another process untouched.
            for item in created:
                item.unlink(missing_ok=True)
            try:
                output.rmdir()
            except OSError:
                pass
            raise


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-dir", required=True, type=Path)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--release-tag", required=True)
    args = parser.parse_args(argv)
    if not re.fullmatch(r"[0-9a-f]{40}", args.source_sha):
        parser.error("--source-sha must be a full lowercase Git commit SHA")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", args.release_tag):
        parser.error("--release-tag contains unsupported characters")
    return args


def main(argv: list[str] | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    try:
        build_bundle(args.source_dir, args.source_sha, args.output, args.release_tag)
    except (BuildError, OSError, tarfile.TarError, subprocess.SubprocessError) as exc:
        print(f"artifact build failed: {exc}", file=sys.stderr)
        return 1
    print(f"wrote local artifact bundle to {args.output.absolute()}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
