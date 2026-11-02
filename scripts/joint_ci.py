#!/usr/bin/env python3
"""Fail-closed provenance helpers for the Agent24 × Hyphae CI gate.

This module handles only the narrow lock/manifest boundary. It never invokes
build tools or the joint runner; callers must pass already built artifacts.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import struct
import sys
from typing import Any

BASELINE_CONFIG_PATH = Path(__file__).resolve().parents[1] / ".github" / "joint-ci-baseline.json"
try:
    BASELINE_CONFIG = json.loads(BASELINE_CONFIG_PATH.read_text(encoding="utf-8"))
except (OSError, json.JSONDecodeError):
    raise RuntimeError("joint-ci-baseline-config-invalid") from None
if type(BASELINE_CONFIG) is not dict or BASELINE_CONFIG.get("schema") != 1:
    raise RuntimeError("joint-ci-baseline-config-invalid")
AGENT24_BASE_SHA = BASELINE_CONFIG.get("agent24_base_sha", "")
PRODUCTION_LOCK_SHA256 = BASELINE_CONFIG.get("production_lock_sha256", "")
PRODUCTION_HYPHAE_SHA = BASELINE_CONFIG.get("production_hyphae_sha", "")
GO_VERSION_VALUE = BASELINE_CONFIG.get("go_version", "")
GO_VERSION = "go" + GO_VERSION_VALUE if isinstance(GO_VERSION_VALUE, str) else ""
if (not isinstance(AGENT24_BASE_SHA, str)
        or not isinstance(PRODUCTION_HYPHAE_SHA, str)
        or not isinstance(PRODUCTION_LOCK_SHA256, str)
        or re.fullmatch(r"[0-9a-f]{40}", AGENT24_BASE_SHA) is None
        or re.fullmatch(r"[0-9a-f]{40}", PRODUCTION_HYPHAE_SHA) is None
        or re.fullmatch(r"[0-9a-f]{64}", PRODUCTION_LOCK_SHA256) is None
        or GO_VERSION != "go1.26.4"):
    raise RuntimeError("joint-ci-baseline-config-invalid")
GO_RECIPE = "GOTOOLCHAIN=go1.26.4 CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -trimpath -buildvcs=false -ldflags='-buildid=' -o hyphae ./cmd/hyphae"
PLATFORM_KEYS = {"darwin-arm64", "linux-x64", "darwin-x64", "linux-arm64"}
ARTIFACTS = ("hyphae", "relay", "agent24", "agent24d")
LOCK2_PLATFORMS = {"darwin-arm64", "linux-x64"}
LOCK2_KEYS = {"schema", "id", "version", "source", "contract", "release_url_base", "assets", "signing",
              "reproducible", "stop_grace_ms", "provides", "requires", "assurance"}
MACOS_TEAM_ID = "HYAP444FN9"
SKIP_REASON = "unsigned candidate; signing verified in pinned-production"
CONTROL_STAGE = "signature-gate-positive-control"
PRECHECK_STAGE = "codesign-precheck"
# The ten acceptance stages of the full runner. An unsigned darwin candidate must report exactly these as
# SKIPPED (no more, no fewer), so dropping a real stage cannot hide behind a remaining SKIPPED one.
STAGE_NAMES = frozenset({
    "verified-artifacts-isolated-homes-real-relay", "memory-password-unlock-initial",
    "authenticated-http-and-cli-positive-control", "hyphae-real-relay-pre-unlock-positive-control",
    "bidirectional-event-id-and-body", "offline-retry-same-id-exactly-one",
    "daemon-offline-125-recovery-and-restart-zero", "config-restart-generation",
    "memory-password-unlock-after-agent24d-restart", "sigterm-sigkill-and-process-group-cleanup"})
DARWIN_CANDIDATE_PASS_STAGES = frozenset({"download-and-hash", PRECHECK_STAGE, CONTROL_STAGE})
RELEASE_URL_BASE = "https://github.com/iDoris-ai/Hyphae/releases/download"


class GateError(Exception):
    """Stable, non-sensitive gate error."""


def require(ok: bool, label: str) -> None:
    if not ok:
        raise GateError(label)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def hash_descriptor(descriptor: int) -> str:
    before = os.fstat(descriptor)
    require(stat.S_ISREG(before.st_mode) and before.st_nlink == 1, "artifact-not-regular")
    os.lseek(descriptor, 0, os.SEEK_SET)
    digest = hashlib.sha256()
    size = 0
    with os.fdopen(descriptor, "rb", closefd=False) as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            size += len(chunk)
            digest.update(chunk)
    after = os.fstat(descriptor)
    require(stat.S_ISREG(after.st_mode) and after.st_nlink == 1 and size == after.st_size == before.st_size,
            "artifact-changed-during-read")
    return digest.hexdigest()


def read_regular(path: Path, limit: int) -> bytes:
    try:
        info = path.lstat()
        require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1 and info.st_size <= limit,
                "input-not-regular-or-too-large")
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    except OSError:
        raise GateError("input-unreadable") from None
    try:
        opened = os.fstat(descriptor)
        require(stat.S_ISREG(opened.st_mode) and opened.st_nlink == 1 and opened.st_size == info.st_size
                and opened.st_size <= limit, "input-not-regular-or-too-large")
        data = bytearray()
        with os.fdopen(descriptor, "rb", closefd=False) as stream:
            for chunk in iter(lambda: stream.read(64 * 1024), b""):
                data.extend(chunk)
                require(len(data) <= limit, "input-too-large")
        require(len(data) == opened.st_size, "input-changed-during-read")
        return bytes(data)
    finally:
        os.close(descriptor)


def file_sha(path: Path) -> str:
    try:
        info = path.lstat()
        require(stat.S_ISREG(info.st_mode) and not path.is_symlink(), "artifact-not-regular")
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        try:
            opened = os.fstat(descriptor)
            require(stat.S_ISREG(opened.st_mode) and opened.st_nlink == 1 and opened.st_size == info.st_size,
                    "artifact-not-regular")
            return hash_descriptor(descriptor)
        finally:
            os.close(descriptor)
    except OSError:
        raise GateError("artifact-unreadable") from None


def verify_binary_platform(path: Path, platform: str) -> None:
    """Fail closed unless a regular executable has the requested native format."""
    require(platform in PLATFORM_KEYS, "producer-platform-invalid")
    try:
        info = path.lstat()
        require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1 and not path.is_symlink(),
                "artifact-not-regular")
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        try:
            opened = os.fstat(descriptor)
            require(stat.S_ISREG(opened.st_mode) and opened.st_nlink == 1 and opened.st_size == info.st_size,
                    "artifact-not-regular")
            header = os.read(descriptor, 64)
        finally:
            os.close(descriptor)
    except OSError:
        raise GateError("artifact-unreadable") from None
    if platform in ("linux-x64", "linux-arm64"):
        require(len(header) >= 20 and header[:4] == b"\x7fELF" and header[4] == 2,
                "artifact-platform-mismatch")
        endian = "little" if header[5] == 1 else "big" if header[5] == 2 else "invalid"
        require(endian != "invalid", "artifact-platform-mismatch")
        machine = int.from_bytes(header[18:20], endian)
        expected_machine = 62 if platform == "linux-x64" else 183
        require(machine == expected_machine, "artifact-platform-mismatch")
    elif platform in ("darwin-arm64", "darwin-x64"):
        require(len(header) >= 8 and header[:4] == b"\xcf\xfa\xed\xfe",
                "artifact-platform-mismatch")
        cpu_type = struct.unpack("<I", header[4:8])[0]
        expected_cpu = 0x0100000C if platform == "darwin-arm64" else 0x01000007
        require(cpu_type == expected_cpu, "artifact-platform-mismatch")


def parse_json(data: bytes, label: str) -> Any:
    try:
        def unique(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
            out: dict[str, Any] = {}
            for key, value in pairs:
                require(key not in out, "duplicate-json-key")
                out[key] = value
            return out
        return json.loads(data.decode("utf-8"), object_pairs_hook=unique)
    except (UnicodeError, json.JSONDecodeError):
        raise GateError(label) from None


def lock_schema(lock: Any) -> int:
    """Dispatch on the lock's own schema field; anything but 1 or 2 is rejected."""
    schema = lock.get("schema") if type(lock) is dict else None
    require(type(schema) is int and schema in (1, 2), "production-lock-schema-unsupported")
    return schema


def validate_lock2(lock: dict[str, Any]) -> None:
    """Exact-shape check of the schema 2 production lock (same strictness as schema 1)."""
    def hex64(value: Any) -> bool:
        return isinstance(value, str) and re.fullmatch(r"[0-9a-f]{64}", value) is not None

    def shape(value: Any, keys: set[str], label: str) -> dict[str, Any]:
        require(type(value) is dict and set(value) == keys, label)
        return value

    shape(lock, LOCK2_KEYS, "production-lock-keys")
    require(lock["id"] == "hyphae" and isinstance(lock["version"], str) and lock["version"], "production-lock-identity")
    source = shape(lock["source"], {"repo", "commit"}, "production-lock-source")
    require(source["commit"] == PRODUCTION_HYPHAE_SHA, "production-lock-baseline")
    assets = shape(lock["assets"], LOCK2_PLATFORMS, "production-lock-platform-set")
    require(isinstance(source["repo"], str), "production-lock-source")
    for item in assets.values():
        require(hex64(shape(item, {"name", "sha256", "files", "executables"}, "production-lock-asset")["sha256"]),
                "production-lock-asset-hash")
        require(isinstance(item["name"], str) and all(type(item[k]) is list and all(isinstance(x, str) for x in item[k])
                                                      for k in ("files", "executables")), "production-lock-asset")
    repro = shape(lock["reproducible"], {"toolchain", "recipe", "unsigned_sha256"}, "production-lock-reproducible")
    unsigned = shape(repro["unsigned_sha256"], LOCK2_PLATFORMS, "production-lock-platform-set")
    require(all(hex64(v) for v in unsigned.values()) and repro["toolchain"] == GO_VERSION,
            "production-lock-reproducible")
    require(assets["linux-x64"]["sha256"] == unsigned["linux-x64"], "production-lock-linux-unsigned-mismatch")
    signing = shape(lock["signing"], {"macos_team_id", "notarized"}, "production-lock-signing")
    require(signing["macos_team_id"] == MACOS_TEAM_ID and signing["notarized"] is True, "production-lock-signing")


def lock_view(lock: dict[str, Any], schema: int, platform: str) -> tuple[Any, Any]:
    """Return (source commit, pinned hyphae digest for platform) for either schema."""
    if schema == 1:
        return lock.get("source_sha"), (lock.get("binaries") or {}).get(platform)
    asset = (lock.get("assets") or {}).get(platform)
    return (lock.get("source") or {}).get("commit"), asset.get("sha256") if type(asset) is dict else None


def release_info(production_bytes: bytes, *, platform: str, hyphae_sha: str | None = None) -> dict[str, str]:
    """Facts the producer needs from the schema 2 lock; the build recipe must match the lock text exactly."""
    require(sha256(production_bytes) == PRODUCTION_LOCK_SHA256, "production-lock-hash")
    lock = parse_json(production_bytes, "production-lock-json")
    require(lock_schema(lock) == 2, "production-lock-schema-unsupported")
    validate_lock2(lock)
    require(platform in LOCK2_PLATFORMS, "unsupported-platform")
    version = lock["version"]
    require(re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", version) is not None, "production-lock-identity")
    ldflags = f"-buildid= -X main.version={version}"
    recipe = ("GOTOOLCHAIN=" + GO_VERSION + " CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -trimpath "
              f"-buildvcs=false -ldflags='{ldflags}' -o hyphae ./cmd/hyphae")
    require(lock["reproducible"]["recipe"] == recipe, "production-lock-recipe")
    require(lock["release_url_base"] == RELEASE_URL_BASE, "production-lock-release-url")
    asset = lock["assets"][platform]
    require(re.fullmatch(r"[A-Za-z0-9._-]{1,100}", asset["name"]) is not None, "production-lock-asset")
    info = {"version": version, "ldflags": ldflags, "asset_name": asset["name"], "asset_sha256": asset["sha256"],
            "unsigned_sha256": lock["reproducible"]["unsigned_sha256"][platform],
            "download_url": f"{RELEASE_URL_BASE}/{version}/{asset['name']}"}
    if hyphae_sha is not None:
        require(re.fullmatch(r"[0-9a-f]{40}", hyphae_sha) is not None, "hyphae-source-sha")
        candidate = f"{version}-joint.{hyphae_sha[:12]}"
        info["candidate_version"] = candidate
        info["candidate_ldflags"] = f"-buildid= -X main.version={candidate}"
    return info


def derive_lock(production_bytes: bytes, *, platform: str, hyphae_sha: str,
                hyphae_binary_sha256: str, version: str | None = None,
                unsigned_sha256: str | None = None) -> bytes:
    """Schema 1: change only source_sha and one platform digest.

    Schema 2: change only source.commit, version, assets.<platform>.sha256 and
    reproducible.unsigned_sha256.<platform>; the signing block stays untouched.
    """
    require(sha256(production_bytes) == PRODUCTION_LOCK_SHA256, "production-lock-hash")
    original = parse_json(production_bytes, "production-lock-json")
    schema = lock_schema(original)
    require(platform in (PLATFORM_KEYS if schema == 1 else LOCK2_PLATFORMS), "unsupported-platform")
    require(re.fullmatch(r"[0-9a-f]{40}", hyphae_sha) is not None, "hyphae-source-sha")
    require(re.fullmatch(r"[0-9a-f]{64}", hyphae_binary_sha256) is not None, "hyphae-binary-sha")
    derived = json.loads(json.dumps(original))
    if schema == 1:
        require(original.get("source_sha") == PRODUCTION_HYPHAE_SHA, "production-lock-baseline")
        binaries = original.get("binaries")
        require(type(binaries) is dict and set(binaries) == PLATFORM_KEYS, "production-lock-platform-set")
        derived["source_sha"] = hyphae_sha
        derived["binaries"][platform] = hyphae_binary_sha256
    else:
        unsigned = hyphae_binary_sha256 if unsigned_sha256 is None else unsigned_sha256
        require(re.fullmatch(r"[0-9a-f]{64}", unsigned) is not None, "hyphae-binary-sha")
        require(isinstance(version, str) and re.fullmatch(r"v?[0-9A-Za-z][0-9A-Za-z.+-]{0,63}", version) is not None
                and version != original.get("version"), "candidate-version")
        validate_lock2(original)
        derived["source"]["commit"] = hyphae_sha
        derived["version"] = version
        derived["assets"][platform]["sha256"] = hyphae_binary_sha256
        derived["reproducible"]["unsigned_sha256"][platform] = unsigned
        require(derived["signing"] == original["signing"], "derived-lock-signing-changed")
    result = (json.dumps(derived, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode()
    return result


def validate_derived_lock(production_bytes: bytes, derived_bytes: bytes, *, platform: str,
                          hyphae_sha: str, hyphae_binary_sha256: str,
                          declared_production_sha256: str, declared_derived_sha256: str,
                          version: str | None = None) -> None:
    require(sha256(production_bytes) == PRODUCTION_LOCK_SHA256 == declared_production_sha256,
            "production-lock-provenance")
    require(sha256(derived_bytes) == declared_derived_sha256, "derived-lock-hash")
    expected = derive_lock(production_bytes, platform=platform, hyphae_sha=hyphae_sha,
                           hyphae_binary_sha256=hyphae_binary_sha256, version=version)
    require(derived_bytes == expected, "derived-lock-diff")


def make_manifest(*, mode: str, platform: str, hyphae_sha: str, agent24_sha: str,
                  production_lock_sha256: str, derived_lock_sha256: str,
                  hashes: dict[str, str], candidate_version: str | None = None) -> dict[str, Any]:
    require(mode in ("candidate", "pinned-production"), "validation-mode")
    require(platform in PLATFORM_KEYS, "unsupported-platform")
    require(set(hashes) == set(ARTIFACTS), "binary-set")
    require(all(re.fullmatch(r"[0-9a-f]{64}", h) for h in hashes.values()), "binary-hash")
    result: dict[str, Any] = {
        "schema": "agent24-hyphae-joint-ci-manifest/1",
        "validation_mode": mode,
        "platform": platform,
        "agent24_base_sha": AGENT24_BASE_SHA,
        "agent24_built_sha": agent24_sha,
        "hyphae_source_sha": hyphae_sha,
        "production_lock_sha256": production_lock_sha256,
        "derived_lock_sha256": derived_lock_sha256,
        "go_version": GO_VERSION,
        "go_recipe": GO_RECIPE,
        "binary_sha256": dict(sorted(hashes.items())),
    }
    if candidate_version is not None:
        result["candidate_version"] = candidate_version
    return result


def validate_manifest(path: Path, artifact_dir: Path, production_lock: Path) -> dict[str, Any]:
    try:
        directory_info = artifact_dir.lstat()
        parent_info = artifact_dir.parent.lstat()
    except OSError:
        raise GateError("artifact-directory-unreadable") from None
    require(stat.S_ISDIR(directory_info.st_mode) and not artifact_dir.is_symlink()
            and stat.S_ISDIR(parent_info.st_mode) and not artifact_dir.parent.is_symlink(),
            "artifact-directory-not-regular")
    expected_entries = set(ARTIFACTS) | {"hyphae.lock.json", "manifest.json"}
    try:
        entries = {item.name for item in artifact_dir.iterdir()}
    except OSError:
        raise GateError("artifact-directory-unreadable") from None
    require(entries == expected_entries and path.absolute() == (artifact_dir / "manifest.json").absolute(),
            "artifact-file-set")
    try:
        manifest_bytes = read_regular(path, 128 * 1024)
        manifest = parse_json(manifest_bytes, "manifest-json")
        prod = read_regular(production_lock, 64 * 1024)
        derived = read_regular(artifact_dir / "hyphae.lock.json", 64 * 1024)
    except OSError:
        raise GateError("manifest-input-unreadable") from None
    require(type(manifest) is dict and manifest.get("schema") == "agent24-hyphae-joint-ci-manifest/1", "manifest-schema")
    mode = manifest.get("validation_mode")
    require(mode in ("candidate", "pinned-production"), "manifest-mode")
    platform = manifest.get("platform")
    require(platform in PLATFORM_KEYS, "manifest-platform-invalid")
    hashes = manifest.get("binary_sha256")
    require(type(hashes) is dict and set(hashes) == set(ARTIFACTS), "manifest-binary-set")
    for name in ARTIFACTS:
        require(file_sha(artifact_dir / name) == hashes[name], "artifact-hash-mismatch")
    if mode == "candidate":
        validate_derived_lock(
            prod, derived, platform=platform, hyphae_sha=manifest.get("hyphae_source_sha"),
            hyphae_binary_sha256=hashes["hyphae"],
            declared_production_sha256=manifest.get("production_lock_sha256"),
            declared_derived_sha256=manifest.get("derived_lock_sha256"),
            version=manifest.get("candidate_version"),
        )
    else:
        require(sha256(prod) == PRODUCTION_LOCK_SHA256 == manifest.get("production_lock_sha256"),
                "production-lock-hash")
        require(derived == prod and manifest.get("derived_lock_sha256") == PRODUCTION_LOCK_SHA256,
                "production-lock-not-preserved")
        lock = parse_json(prod, "production-lock-json")
        schema = lock_schema(lock)
        if schema == 2:
            validate_lock2(lock)
            require(platform in LOCK2_PLATFORMS, "production-lock-platform-set")
        source, pinned = lock_view(lock, schema, platform)
        require(source == manifest.get("hyphae_source_sha"), "production-source-mismatch")
        require(pinned == hashes["hyphae"], "production-binary-mismatch")
    require(manifest.get("agent24_base_sha") == AGENT24_BASE_SHA, "manifest-agent24-base")
    require(manifest.get("agent24_built_sha") == AGENT24_BASE_SHA, "manifest-agent24-built-source")
    require(manifest.get("go_version") == GO_VERSION and manifest.get("go_recipe") == GO_RECIPE, "manifest-go-recipe")
    return manifest


def check_producer_outputs(manifest_path: Path, manifest: dict[str, Any], *,
                           expected_manifest_sha256: str, expected_platform: str,
                           expected_hyphae_sha: str,
                           expected_outputs: dict[str, Any] | None = None) -> None:
    require(expected_platform in PLATFORM_KEYS, "producer-platform-invalid")
    require(sha256(manifest_path.read_bytes()) == expected_manifest_sha256, "manifest-producer-hash")
    require(manifest.get("platform") == expected_platform, "manifest-platform")
    require(manifest.get("hyphae_source_sha") == expected_hyphae_sha, "manifest-source")
    if expected_outputs is not None:
        actual = {
            "validation_mode": manifest.get("validation_mode"),
            "platform": manifest.get("platform"),
            "hyphae_source_sha": manifest.get("hyphae_source_sha"),
            "production_lock_sha256": manifest.get("production_lock_sha256"),
            "derived_lock_sha256": manifest.get("derived_lock_sha256"),
            "binary_sha256": manifest.get("binary_sha256"),
            "manifest_sha256": sha256(manifest_path.read_bytes()),
        }
        require(actual == expected_outputs, "producer-output-mismatch")


def restore_executable_modes(artifact_dir: Path, manifest: dict[str, Any]) -> None:
    """Restore ZIP-lost executable bits only after independent hash validation."""
    hashes = manifest.get("binary_sha256")
    require(type(hashes) is dict and set(hashes) == set(ARTIFACTS), "manifest-binary-set")
    descriptors: list[tuple[int, str]] = []
    try:
        # Open and validate the entire fixed binary set before changing any
        # mode; a bad final artifact must not leave earlier files chmodded.
        for name in ARTIFACTS:
            path = artifact_dir / name
            try:
                info = path.lstat()
                require(stat.S_ISREG(info.st_mode) and not path.is_symlink() and info.st_nlink == 1,
                        "artifact-not-regular")
                descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
            except OSError:
                raise GateError("artifact-open-failed") from None
            descriptors.append((descriptor, name))
            opened = os.fstat(descriptor)
            if not stat.S_ISREG(opened.st_mode) or opened.st_nlink != 1:
                raise GateError("artifact-not-regular")
            if hash_descriptor(descriptor) != hashes[name]:
                raise GateError("artifact-hash-mismatch")
        for descriptor, _ in descriptors:
            os.fchmod(descriptor, 0o755)
            require(os.fstat(descriptor).st_mode & 0o111 == 0o111, "artifact-mode-restore-failed")
        for descriptor, name in descriptors:
            require(hash_descriptor(descriptor) == hashes[name], "artifact-changed-during-mode-restore")
    finally:
        for descriptor, _ in descriptors:
            os.close(descriptor)


def verify_consumer_bundle(manifest_path: Path, artifact_dir: Path, production_lock: Path, *,
                           expected_manifest_sha256: str, expected_platform: str,
                           expected_hyphae_sha: str, expected_outputs: dict[str, Any]) -> dict[str, Any]:
    manifest = validate_manifest(manifest_path, artifact_dir, production_lock)
    check_producer_outputs(manifest_path, manifest,
                           expected_manifest_sha256=expected_manifest_sha256,
                           expected_platform=expected_platform,
                           expected_hyphae_sha=expected_hyphae_sha,
                           expected_outputs=expected_outputs)
    for name in ARTIFACTS:
        verify_binary_platform(artifact_dir / name, expected_platform)
    restore_executable_modes(artifact_dir, manifest)
    return manifest


def normalize_stage(row: dict[str, Any], stage: str, strict: bool) -> dict[str, Any]:
    """One stage record. PASS, SKIPPED (with reason) or the control stage; never a blurred mix."""
    outcome = row.get("result")
    if row.get("kind") == "control":
        # Positive control: PASS only when the signature gate was observed rejecting the unsigned candidate.
        require(strict and stage == CONTROL_STAGE and outcome in (None, "PASS"), "control-unexpected")
        reason_code = row.get("reason_code")
        require(row.get("expected") == "rejected" and row.get("observed") == "rejected"
                and reason_code == "signature_rejected", "control-not-rejected")
        return {"stage": stage, "result": "PASS", "kind": "control", "expected": "rejected",
                "observed": "rejected", "reason_code": reason_code}
    if outcome == "SKIPPED":
        reason = row.get("reason")
        require(isinstance(reason, str) and reason.strip() != "", "skipped-reason-missing")
        require(strict and reason == SKIP_REASON, "skipped-reason-invalid")
        return {"stage": stage, "result": "SKIPPED", "reason": reason}
    require(outcome in (None, "PASS", "BLOCKED"), "runner-evidence-result")
    require(not strict or outcome == "BLOCKED" or stage in DARWIN_CANDIDATE_PASS_STAGES,
            "stage-not-skipped-in-unsigned-candidate")
    return {"stage": stage, "result": "BLOCKED" if outcome == "BLOCKED" else "PASS"}


def normalized_evidence(path: Path, manifest: dict[str, Any], *, lock_schema_id: int = 1) -> dict[str, Any]:
    """Drop all runner details except safe stage labels and outcome metadata.

    ``lock_schema_id`` 2 + darwin candidate = unsigned candidate: only download/hash and the
    positive control may PASS, every other stage must be SKIPPED, and the control is mandatory.
    """
    strict = lock_schema_id == 2 and manifest["validation_mode"] == "candidate" and manifest["platform"] == "darwin-arm64"
    raw = parse_json(path.read_bytes(), "runner-evidence-json")
    require(type(raw) is dict and raw.get("result") in ("PASS", "FAIL", "BLOCKED"), "runner-evidence-shape")
    assertions = raw.get("assertions")
    require(type(assertions) is list, "runner-evidence-stages")
    stages = []
    for row in assertions:
        require(type(row) is dict and isinstance(row.get("stage"), str), "runner-evidence-stage")
        stage = row["stage"]
        require(re.fullmatch(r"[a-z0-9-]{1,100}", stage) is not None, "runner-evidence-stage-label")
        stages.append(normalize_stage(row, stage, strict))
    controls = [item for item in stages if item.get("kind") == "control"]
    require(len(controls) == (1 if strict else 0) and len({item["stage"] for item in stages}) == len(stages),
            "control-stage-missing" if strict else "control-unexpected")
    require(not strict or (not any(item["result"] == "BLOCKED" for item in stages)
                           and {i["stage"] for i in stages if i["result"] == "PASS"} == DARWIN_CANDIDATE_PASS_STAGES
                           and {i["stage"] for i in stages if i["result"] == "SKIPPED"} == STAGE_NAMES),
            "unsigned-candidate-evidence-incomplete")
    # A full run (schema 2, everything but the unsigned darwin candidate) that reports PASS must have run all ten stages.
    require(strict or lock_schema_id != 2 or raw["result"] != "PASS"
            or {i["stage"] for i in stages if i["result"] == "PASS"} == STAGE_NAMES, "full-run-stage-set-incomplete")
    summary = {"passed": sum(1 for item in stages if item["result"] == "PASS"),
               "skipped": sum(1 for item in stages if item["result"] == "SKIPPED"),
               "blocked": sum(1 for item in stages if item["result"] == "BLOCKED")}
    def safe_label(value: Any) -> str | None:
        return value if isinstance(value, str) and re.fullmatch(r"[A-Za-z0-9_-]{1,100}", value) else None

    def safe_cleanup(value: Any) -> dict[str, Any] | None:
        if type(value) is not dict:
            return None
        safe: dict[str, Any] = {}
        for key in ("stage", "category", "errno"):
            item = value.get(key)
            if key == "errno" and type(item) is int:
                safe[key] = item
            elif key != "errno" and safe_label(item):
                safe[key] = item
        return safe or None

    result: dict[str, Any] = {
        "schema": "agent24-hyphae-joint-ci-evidence/1",
        "validation_mode": manifest["validation_mode"],
        "platform": manifest["platform"],
        "hyphae_source_sha": manifest["hyphae_source_sha"],
        "agent24_source_sha": manifest["agent24_built_sha"],
        "production_lock_sha256": manifest["production_lock_sha256"],
        "derived_lock_sha256": manifest["derived_lock_sha256"],
        "binary_sha256": manifest["binary_sha256"],
        # A run with skipped stages is never a clean PASS.
        "result": "PASS_WITH_SKIPPED" if raw["result"] == "PASS" and summary["skipped"] else raw["result"],
        "stages": stages,
        "summary": summary,
    }
    for field in ("failure", "primary_failure"):
        label = safe_label(raw.get(field))
        if label:
            result[field] = label
    cleanup = safe_cleanup(raw.get("cleanup_failure"))
    if cleanup:
        result["cleanup_failure"] = cleanup
    executions = raw.get("executions")
    if type(executions) is list:
        safe_executions = []
        for row in executions:
            if type(row) is not dict:
                continue
            safe_row: dict[str, Any] = {}
            for key in ("stage", "cleanup", "failure", "primary_failure"):
                label = safe_label(row.get(key))
                if label:
                    safe_row[key] = label
            for key in ("exit", "stdout_bytes", "stderr_bytes"):
                if type(row.get(key)) is int:
                    safe_row[key] = row[key]
            if row.get("output_redacted") is True:
                safe_row["output_redacted"] = True
            nested_cleanup = safe_cleanup(row.get("cleanup_failure"))
            if nested_cleanup:
                safe_row["cleanup_failure"] = nested_cleanup
            if safe_row:
                safe_executions.append(safe_row)
        result["executions"] = safe_executions
    return result


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    derive = commands.add_parser("derive-lock")
    derive.add_argument("--production-lock", type=Path, required=True)
    derive.add_argument("--platform", choices=sorted(PLATFORM_KEYS), required=True)
    derive.add_argument("--hyphae-sha", required=True)
    derive.add_argument("--hyphae-sha256", required=True)
    derive.add_argument("--version")
    derive.add_argument("--output", type=Path, required=True)
    verify = commands.add_parser("verify-candidate")
    verify.add_argument("--manifest", type=Path, required=True)
    verify.add_argument("--artifact-dir", type=Path, required=True)
    verify.add_argument("--production-lock", type=Path, required=True)
    verify.add_argument("--expected-manifest-sha256", required=True)
    verify.add_argument("--expected-platform", choices=sorted(PLATFORM_KEYS), required=True)
    verify.add_argument("--expected-hyphae-sha", required=True)
    verify.add_argument("--expected-producer-outputs", required=True)
    manifest_cmd = commands.add_parser("write-manifest")
    manifest_cmd.add_argument("--mode", choices=("candidate", "pinned-production"), required=True)
    manifest_cmd.add_argument("--platform", choices=sorted(PLATFORM_KEYS), required=True)
    manifest_cmd.add_argument("--hyphae-sha", required=True)
    manifest_cmd.add_argument("--artifact-dir", type=Path, required=True)
    manifest_cmd.add_argument("--production-lock", type=Path, required=True)
    manifest_cmd.add_argument("--candidate-version")
    manifest_cmd.add_argument("--output", type=Path, required=True)
    normalize = commands.add_parser("normalize-evidence")
    normalize.add_argument("--evidence", type=Path, required=True)
    normalize.add_argument("--manifest", type=Path, required=True)
    normalize.add_argument("--output", type=Path, required=True)
    info = commands.add_parser("release-info")
    info.add_argument("--production-lock", type=Path, required=True)
    info.add_argument("--platform", choices=sorted(LOCK2_PLATFORMS), required=True)
    info.add_argument("--hyphae-sha")
    check = commands.add_parser("check-file-sha256")
    check.add_argument("--file", type=Path, required=True)
    check.add_argument("--expected", required=True)
    verify_platform = commands.add_parser("verify-platform")
    verify_platform.add_argument("--platform", choices=sorted(PLATFORM_KEYS), required=True)
    verify_platform.add_argument("--binary", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        if args.command == "derive-lock":
            result = derive_lock(args.production_lock.read_bytes(), platform=args.platform,
                                 hyphae_sha=args.hyphae_sha, hyphae_binary_sha256=args.hyphae_sha256, version=args.version)
            args.output.write_bytes(result)
            return 0
        if args.command == "write-manifest":
            prod = args.production_lock.read_bytes()
            derived = (args.artifact_dir / "hyphae.lock.json").read_bytes()
            require(sha256(prod) == PRODUCTION_LOCK_SHA256, "production-lock-hash")
            hashes = {name: file_sha(args.artifact_dir / name) for name in ARTIFACTS}
            manifest = make_manifest(
                mode=args.mode, platform=args.platform, hyphae_sha=args.hyphae_sha,
                agent24_sha=AGENT24_BASE_SHA, production_lock_sha256=sha256(prod),
                derived_lock_sha256=sha256(derived), hashes=hashes,
                candidate_version=args.candidate_version)
            args.output.write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n", encoding="utf-8")
            return 0
        if args.command == "normalize-evidence":
            manifest = parse_json(args.manifest.read_bytes(), "manifest-json")
            lock = read_regular(args.manifest.parent / "hyphae.lock.json", 64 * 1024)
            require(sha256(lock) == manifest.get("derived_lock_sha256"), "derived-lock-hash")
            safe = normalized_evidence(args.evidence, manifest,
                                       lock_schema_id=lock_schema(parse_json(lock, "derived-lock-json")))
            args.output.write_text(json.dumps(safe, sort_keys=True, indent=2) + "\n", encoding="utf-8")
            return 0 if safe["result"] in ("PASS", "PASS_WITH_SKIPPED") and not safe["summary"]["blocked"] else 1
        if args.command == "release-info":
            for key, value in release_info(read_regular(args.production_lock, 64 * 1024), platform=args.platform,
                                           hyphae_sha=args.hyphae_sha).items():
                print(f"{key}={value}")
            return 0
        if args.command == "check-file-sha256":
            require(re.fullmatch(r"[0-9a-f]{64}", args.expected) is not None and file_sha(args.file) == args.expected,
                    "file-sha256-mismatch")
            return 0
        if args.command == "verify-platform":
            verify_binary_platform(args.binary, args.platform)
            return 0
        manifest = verify_consumer_bundle(
            args.manifest, args.artifact_dir, args.production_lock,
            expected_manifest_sha256=args.expected_manifest_sha256,
            expected_platform=args.expected_platform,
            expected_hyphae_sha=args.expected_hyphae_sha,
            expected_outputs=parse_json(args.expected_producer_outputs.encode(), "producer-output-json"),
        )
        print(json.dumps({"validation_mode": manifest["validation_mode"], "platform": manifest["platform"],
                          "derived_lock_sha256": manifest["derived_lock_sha256"]}, sort_keys=True))
        return 0
    except (GateError, OSError) as error:
        print(f"joint CI gate failed: {error if isinstance(error, GateError) else 'input-unreadable'}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
