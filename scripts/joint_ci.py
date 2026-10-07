#!/usr/bin/env python3
"""Fail-closed provenance helpers for the Agent24 × Hyphae CI gate.

This module handles only the narrow lock/manifest boundary. It never invokes
build tools or the joint runner; callers must pass already built artifacts.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import stat
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


class GateError(Exception):
    """Stable, non-sensitive gate error."""


def require(ok: bool, label: str) -> None:
    if not ok:
        raise GateError(label)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def file_sha(path: Path) -> str:
    try:
        info = path.lstat()
        require(stat.S_ISREG(info.st_mode) and not path.is_symlink(), "artifact-not-regular")
        return sha256(path.read_bytes())
    except OSError:
        raise GateError("artifact-unreadable") from None


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


def derive_lock(production_bytes: bytes, *, platform: str, hyphae_sha: str,
                hyphae_binary_sha256: str) -> bytes:
    """Change only source_sha and one current-platform binary digest."""
    require(sha256(production_bytes) == PRODUCTION_LOCK_SHA256, "production-lock-hash")
    require(platform in PLATFORM_KEYS, "unsupported-platform")
    require(re.fullmatch(r"[0-9a-f]{40}", hyphae_sha) is not None, "hyphae-source-sha")
    require(re.fullmatch(r"[0-9a-f]{64}", hyphae_binary_sha256) is not None, "hyphae-binary-sha")
    original = parse_json(production_bytes, "production-lock-json")
    require(type(original) is dict and original.get("source_sha") == PRODUCTION_HYPHAE_SHA, "production-lock-baseline")
    binaries = original.get("binaries")
    require(type(binaries) is dict and set(binaries) == PLATFORM_KEYS, "production-lock-platform-set")
    derived = json.loads(json.dumps(original))
    derived["source_sha"] = hyphae_sha
    derived["binaries"][platform] = hyphae_binary_sha256
    result = (json.dumps(derived, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode()
    return result


def validate_derived_lock(production_bytes: bytes, derived_bytes: bytes, *, platform: str,
                          hyphae_sha: str, hyphae_binary_sha256: str,
                          declared_production_sha256: str, declared_derived_sha256: str) -> None:
    require(sha256(production_bytes) == PRODUCTION_LOCK_SHA256 == declared_production_sha256,
            "production-lock-provenance")
    require(sha256(derived_bytes) == declared_derived_sha256, "derived-lock-hash")
    expected = derive_lock(production_bytes, platform=platform, hyphae_sha=hyphae_sha,
                           hyphae_binary_sha256=hyphae_binary_sha256)
    require(derived_bytes == expected, "derived-lock-diff")


def make_manifest(*, mode: str, platform: str, hyphae_sha: str, agent24_sha: str,
                  production_lock_sha256: str, derived_lock_sha256: str,
                  hashes: dict[str, str]) -> dict[str, Any]:
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
    return result


def validate_manifest(path: Path, artifact_dir: Path, production_lock: Path) -> dict[str, Any]:
    try:
        manifest_bytes = path.read_bytes()
        manifest = parse_json(manifest_bytes, "manifest-json")
        prod = production_lock.read_bytes()
        derived = (artifact_dir / "hyphae.lock.json").read_bytes()
    except OSError:
        raise GateError("manifest-input-unreadable") from None
    require(type(manifest) is dict and manifest.get("schema") == "agent24-hyphae-joint-ci-manifest/1", "manifest-schema")
    mode = manifest.get("validation_mode")
    require(mode in ("candidate", "pinned-production"), "manifest-mode")
    platform = manifest.get("platform")
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
        )
    else:
        require(sha256(prod) == PRODUCTION_LOCK_SHA256 == manifest.get("production_lock_sha256"),
                "production-lock-hash")
        require(derived == prod and manifest.get("derived_lock_sha256") == PRODUCTION_LOCK_SHA256,
                "production-lock-not-preserved")
        lock = parse_json(prod, "production-lock-json")
        require(lock.get("source_sha") == manifest.get("hyphae_source_sha"), "production-source-mismatch")
        require(lock.get("binaries", {}).get(platform) == hashes["hyphae"], "production-binary-mismatch")
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


def normalized_evidence(path: Path, manifest: dict[str, Any]) -> dict[str, Any]:
    """Drop all runner details except safe stage labels and outcome metadata."""
    raw = parse_json(path.read_bytes(), "runner-evidence-json")
    require(type(raw) is dict and raw.get("result") in ("PASS", "FAIL", "BLOCKED"), "runner-evidence-shape")
    assertions = raw.get("assertions")
    require(type(assertions) is list, "runner-evidence-stages")
    stages = []
    for row in assertions:
        require(type(row) is dict and isinstance(row.get("stage"), str), "runner-evidence-stage")
        stage = row["stage"]
        require(re.fullmatch(r"[a-z0-9-]{1,100}", stage) is not None, "runner-evidence-stage-label")
        stages.append({"stage": stage, "result": "BLOCKED" if row.get("result") == "BLOCKED" else "PASS"})
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
        "result": raw["result"],
        "stages": stages,
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
    manifest_cmd.add_argument("--output", type=Path, required=True)
    normalize = commands.add_parser("normalize-evidence")
    normalize.add_argument("--evidence", type=Path, required=True)
    normalize.add_argument("--manifest", type=Path, required=True)
    normalize.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        if args.command == "derive-lock":
            result = derive_lock(args.production_lock.read_bytes(), platform=args.platform,
                                 hyphae_sha=args.hyphae_sha, hyphae_binary_sha256=args.hyphae_sha256)
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
                derived_lock_sha256=sha256(derived), hashes=hashes)
            args.output.write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n", encoding="utf-8")
            return 0
        if args.command == "normalize-evidence":
            manifest = parse_json(args.manifest.read_bytes(), "manifest-json")
            safe = normalized_evidence(args.evidence, manifest)
            args.output.write_text(json.dumps(safe, sort_keys=True, indent=2) + "\n", encoding="utf-8")
            return 0 if safe["result"] == "PASS" else 1
        manifest = validate_manifest(args.manifest, args.artifact_dir, args.production_lock)
        check_producer_outputs(args.manifest, manifest,
                               expected_manifest_sha256=args.expected_manifest_sha256,
                               expected_platform=args.expected_platform,
                               expected_hyphae_sha=args.expected_hyphae_sha,
                               expected_outputs=parse_json(args.expected_producer_outputs.encode(), "producer-output-json"))
        print(json.dumps({"validation_mode": manifest["validation_mode"], "platform": manifest["platform"],
                          "derived_lock_sha256": manifest["derived_lock_sha256"]}, sort_keys=True))
        return 0
    except (GateError, OSError) as error:
        print(f"joint CI gate failed: {error if isinstance(error, GateError) else 'input-unreadable'}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
