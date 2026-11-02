#!/usr/bin/env python3
"""Combine an unsigned Hyphae bundle with externally signed macOS binaries into the final bundle.

Signing and notarization happen on someone else's machine; this tool never handles credentials.
"""

from __future__ import annotations

import argparse
import contextlib
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
from typing import Any, Callable

from build_release_artifacts import (
    GO_VERSION,
    REPOSITORY,
    RECIPE,
    TAG_PATTERN,
    BuildError,
    sha256_file,
    validate_source,
    write_archive,
    write_json,
)

TEAM_ID = "HYAP444FN9"
# A bare, notarized, un-stapled CLI Mach-O is rejected by `--type execute` ("does not seem to be an
# app"); `--type install` is the check that returns "accepted, source=Notarized Developer ID".
# Measured by the consumer (Agent24); not reproducible without Apple credentials.
SPCTL_ASSESS_TYPE = "install"
MACHO_ARM64 = bytes.fromhex("cffaedfe0c000001")
REPO_URL = "https://github.com/iDoris-ai/Hyphae"
CONTRACT_TESTED = "1.0"
PROVIDES = ("nostr-dm", "nostr-group", "nostr-identity", "nostr-relay")
SIGNED_NAMES = ("hyphae-darwin-arm64", "hyphae-relay-darwin-arm64")
METADATA = ("artifact-manifest.json", "hyphae.lock.json", "SHA256SUMS")

Runner = Callable[[list[str]], "tuple[int, str, str]"]


def run_tool(args: list[str]) -> tuple[int, str, str]:
    result = subprocess.run(args, check=False, capture_output=True, text=True)
    return result.returncode, result.stdout, result.stderr


class MacVerifier:
    """Developer ID + Hardened Runtime + notarization check, via an injectable command runner."""

    def __init__(self, runner: Runner = run_tool) -> None:
        self.runner = runner

    def _run(self, label: str, args: list[str]) -> str:
        code, out, err = self.runner(args)
        if code:
            raise BuildError(f"{label} failed for {Path(args[-1]).name}: {err.strip()[:300]}")
        return out + "\n" + err

    def verify(self, path: Path) -> None:
        target = str(path)
        self._run("codesign --verify", ["codesign", "--verify", "--strict", target])
        details = self._run("codesign -dv", ["codesign", "-dv", "--verbose=4", target])
        if f"TeamIdentifier={TEAM_ID}" not in details.splitlines():
            raise BuildError(f"{path.name} is not signed by team {TEAM_ID}")
        if not re.search(r"flags=\S*\([^)]*runtime[^)]*\)", details):
            raise BuildError(f"{path.name} lacks the Hardened Runtime flag")
        verdict = self._run("spctl --assess", ["spctl", "--assess", "--type", SPCTL_ASSESS_TYPE, "-vv", target])
        if "source=Notarized Developer ID" not in verdict:
            raise BuildError(f"{path.name} was accepted but not as a Notarized Developer ID")


def regular_file(path: Path, what: str) -> None:
    if path.is_symlink() or not path.is_file():
        raise BuildError(f"{what} must be a regular file: {path.name}")


def load_unsigned(bundle: Path) -> dict[str, Any]:
    regular_file(bundle / "artifact-manifest.json", "manifest")
    manifest = json.loads((bundle / "artifact-manifest.json").read_text(encoding="utf-8"))
    if manifest.get("schema") != 1 or not re.fullmatch(TAG_PATTERN, str(manifest.get("release_tag", ""))):
        raise BuildError("unsigned bundle manifest has an unexpected schema or release tag")
    expected = set(METADATA)
    for record in manifest["platforms"].values():
        for item in (*record["binaries"].values(), record["archive"]):
            expected.add(item["filename"])
            regular_file(bundle / item["filename"], "bundle file")
            if sha256_file(bundle / item["filename"]) != item["sha256"]:
                raise BuildError(f"unsigned bundle file does not match its recorded sha256: {item['filename']}")
    if {p.name for p in bundle.iterdir()} != expected:
        raise BuildError("unsigned bundle has missing or extra files")
    for name in METADATA[1:]:
        regular_file(bundle / name, "bundle file")
    return manifest


def check_signed_dir(signed: Path) -> dict[str, Path]:
    found = {p.name: p for p in signed.iterdir()}
    if "hyphae-darwin-arm64" not in found or set(found) - set(SIGNED_NAMES):
        raise BuildError("signed-dir must contain hyphae-darwin-arm64 and optionally hyphae-relay-darwin-arm64 only")
    for path in found.values():
        regular_file(path, "signed file")
    return found


def finalize(unsigned: Path, signed_dir: Path, source: Path, output: Path, verifier: MacVerifier | None) -> None:
    unsigned, signed_dir, source = (p.resolve(strict=True) for p in (unsigned, signed_dir, source))
    output = output.expanduser().absolute()
    if not output.parent.is_dir():
        raise BuildError("output parent must already exist")
    output = output.parent.resolve() / output.name
    if os.path.lexists(output):
        raise BuildError("output must be a new, nonexistent directory")
    manifest = load_unsigned(unsigned)
    tag, head = manifest["release_tag"], manifest["source"]["sha"]
    validate_source(source, head)
    signed = check_signed_dir(signed_dir)
    darwin, linux = manifest["platforms"]["darwin-arm64"], manifest["platforms"]["linux-x64"]
    with tempfile.TemporaryDirectory(prefix=".hyphae-final-", dir=output.parent) as temporary:
        stage = Path(temporary) / "bundle"
        stage.mkdir()
        for item in unsigned.iterdir():
            if "linux" in item.name:
                shutil.copyfile(item, stage / item.name)
        cli = stage / darwin["binaries"]["hyphae"]["filename"]
        relay = stage / darwin["binaries"]["hyphae-relay"]["filename"]
        shutil.copyfile(unsigned / cli.name, cli)
        shutil.copyfile(unsigned / relay.name, relay)
        for name, path in signed.items():
            shutil.copyfile(path, stage / name)  # verify the staged copy, not the mutable input
        for name in signed:
            twin_key = "hyphae" if name == SIGNED_NAMES[0] else "hyphae-relay"
            if verifier is not None:
                if (stage / name).read_bytes()[:8] != MACHO_ARM64:
                    raise BuildError(f"{name} is not an arm64 Mach-O binary")
                if sha256_file(stage / name) == darwin["binaries"][twin_key]["sha256"]:
                    raise BuildError(f"{name} is identical to the unsigned binary; signing did not change it")
                verifier.verify(stage / name)
        for binary in stage.iterdir():
            if binary.name.startswith("hyphae-") and not binary.name.endswith(".tar.gz") and binary.name not in METADATA:
                binary.chmod(0o755)
        archive = stage / darwin["archive"]["filename"]
        write_archive(archive, source, cli, relay)
        unsigned_sha = darwin["binaries"]["hyphae"]["sha256"]
        darwin["binaries"]["hyphae"].update(sha256=sha256_file(cli), unsigned_sha256=unsigned_sha)
        darwin["binaries"]["hyphae-relay"].update(
            sha256=sha256_file(relay), unsigned_sha256=darwin["binaries"]["hyphae-relay"]["sha256"]
        )
        darwin["archive"]["sha256"] = sha256_file(archive)
        linux_cli_sha = linux["binaries"]["hyphae"]["sha256"]
        manifest["publication_status"] = "signed; not published by this tool"
        manifest["signature_verified"] = verifier is not None
        manifest["signed_binaries"] = sorted(signed)  # anything else in the macOS bundle is unsigned pass-through
        base_url = f"https://github.com/{REPOSITORY}/releases/download/{tag}"
        manifest["intended_urls"]["assets"] = {
            name: f"{base_url}/{name}" for name in sorted({*(p.name for p in stage.iterdir()), *METADATA})
        }
        write_json(stage / "artifact-manifest.json", manifest)
        lock = {
            "schema": 2,
            "id": "hyphae",
            "version": tag,
            "source": {"repo": REPO_URL, "commit": head},
            "contract": {"name": "hyphae-cli", "major": 1, "min_minor": 0, "tested": CONTRACT_TESTED},
            "release_url_base": f"{REPO_URL}/releases/download",
            "assets": {
                "darwin-arm64": {"name": "hyphae-darwin-arm64", "sha256": darwin["binaries"]["hyphae"]["sha256"], "files": ["hyphae"], "executables": ["hyphae"]},
                "linux-x64": {"name": "hyphae-linux-amd64", "sha256": linux_cli_sha, "files": ["hyphae"], "executables": ["hyphae"]},
            },
            "signing": {"macos_team_id": TEAM_ID, "notarized": verifier is not None},
            "reproducible": {
                "toolchain": GO_VERSION,
                "recipe": RECIPE,
                "unsigned_sha256": {"darwin-arm64": unsigned_sha, "linux-x64": linux_cli_sha},
            },
            "stop_grace_ms": 15000,
            "provides": [{"id": name} for name in PROVIDES],
            "requires": {"kernel": ["events"], "operations": []},
            "assurance": [],
        }
        write_json(stage / "hyphae.lock.json", lock)
        sums = {p.name: sha256_file(p) for p in stage.iterdir() if p.name != "SHA256SUMS"}
        (stage / "SHA256SUMS").write_text("".join(f"{d}  {n}\n" for n, d in sorted(sums.items())), encoding="ascii")
        consistency_check(stage, lock, manifest)
        publish(stage, output)


def publish(stage: Path, output: Path) -> None:
    output.mkdir()  # exclusive: fails if anything appeared meanwhile (rename could replace an empty dir)
    created: list[Path] = []
    try:
        for item in sorted(stage.iterdir()):
            destination = output / item.name
            with item.open("rb") as reader, destination.open("xb") as writer:
                shutil.copyfileobj(reader, writer)
            created.append(destination)
            destination.chmod(item.stat().st_mode & 0o777)
    except BaseException:
        for item in created:
            item.unlink(missing_ok=True)
        with contextlib.suppress(OSError):
            output.rmdir()
        raise


def consistency_check(stage: Path, lock: dict[str, Any], manifest: dict[str, Any]) -> None:
    listed = dict(line.split("  ", 1)[::-1] for line in (stage / "SHA256SUMS").read_text(encoding="ascii").splitlines())
    actual = {p.name: sha256_file(p) for p in stage.iterdir() if p.name != "SHA256SUMS"}
    if listed != actual:
        raise BuildError("SHA256SUMS disagrees with the output files")
    for record in manifest["platforms"].values():
        for item in (*record["binaries"].values(), record["archive"]):
            if actual[item["filename"]] != item["sha256"]:
                raise BuildError(f"manifest disagrees with output file {item['filename']}")
    for asset in lock["assets"].values():
        if actual[asset["name"]] != asset["sha256"]:
            raise BuildError(f"lock disagrees with output file {asset['name']}")
    for record in manifest["platforms"].values():
        with tarfile.open(stage / record["archive"]["filename"], "r:gz") as archive:
            if sorted(archive.getnames()) != ["LICENSE", "NOTICE", "hyphae", "hyphae-relay"]:
                raise BuildError(f"unexpected members in {record['archive']['filename']}")
            for member, key in (("hyphae", "hyphae"), ("hyphae-relay", "hyphae-relay")):
                if hashlib.sha256(archive.extractfile(member).read()).hexdigest() != record["binaries"][key]["sha256"]:
                    raise BuildError(f"{record['archive']['filename']} does not contain the recorded {key}")


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("unsigned-bundle", "signed-dir", "source-dir", "output"):
        parser.add_argument(f"--{name}", required=True, type=Path)
    parser.add_argument("--no-verify-signature", action="store_true", help=argparse.SUPPRESS)
    return parser.parse_args(argv)


def main(argv: list[str] | None = None, verifier: MacVerifier | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    try:
        if args.no_verify_signature:
            print("WARNING: signature verification DISABLED; output is NOT release-grade", file=sys.stderr)
            verifier = None
        elif sys.platform != "darwin":
            raise BuildError("signature verification needs macOS; refusing to continue")
        else:
            verifier = verifier or MacVerifier()
        finalize(args.unsigned_bundle, args.signed_dir, args.source_dir, args.output, verifier)
    except (BuildError, OSError, KeyError, json.JSONDecodeError, tarfile.TarError) as exc:
        print(f"finalize failed: {exc}", file=sys.stderr)
        return 1
    print(f"wrote final bundle to {args.output.absolute()}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
