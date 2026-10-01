"""Tests for fail-closed values passed between artifact workflow jobs."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import zipfile

from scripts import artifact_ci
from scripts.test_verify_artifact_bundle import BundleFixture

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / ".github" / "workflows" / "comm-artifacts.yml"
LOCK_FIXTURE = ROOT / "scripts" / "fixtures" / "comm-round1" / "hyphae.lock.json"


class ArtifactCIHelpersTests(unittest.TestCase):
    def test_lock_checker_accepts_pinned_fixture_and_rejects_mutated_input(self):
        artifact_ci.check_lock(LOCK_FIXTURE)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "lock.json"
            path.write_bytes(LOCK_FIXTURE.read_bytes() + b" ")
            result = subprocess.run(
                [sys.executable, "scripts/artifact_ci.py", "check-lock", "--path", str(path)],
                cwd=ROOT, capture_output=True, text=True, check=False,
            )
            self.assertEqual(result.returncode, 1)
            self.assertIn("production-lock-sha256", result.stderr)

    def write_verification(self, path: Path, *, platform: str = "linux-x64", digest: str = "1" * 64, extras: dict | None = None):
        data = {
            "source_sha": artifact_ci.SOURCE_SHA,
            "go": artifact_ci.GO_VERSION,
            "recipe": artifact_ci.RECIPE,
            "published": False,
            "platforms": {
                "linux-x64": {"relay_sha256": digest},
                "darwin-arm64": {"relay_sha256": artifact_ci.DARWIN_RELAY_SHA256},
            },
        }
        if extras:
            data.update(extras)
        path.write_text(json.dumps({"ok": True, "data": data}), encoding="utf-8")

    def test_relay_value_is_read_from_successful_verified_bundle(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "verified.json"
            self.write_verification(path)
            self.assertEqual(artifact_ci.relay_hash(path, "linux-x64"), "1" * 64)
            self.assertEqual(artifact_ci.relay_hash(path, "darwin-arm64"), artifact_ci.DARWIN_RELAY_SHA256)

    def test_relay_value_rejects_wrong_baseline_missing_platform_and_malformed_hash(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "verified.json"
            for data, platform, message in (
                ({"source_sha": "0" * 40}, "linux-x64", "verification-baseline"),
                ({"platforms": {"linux-x64": {"relay_sha256": "1" * 64}}}, "linux-x64", "verification-platform-set"),
                ({"platforms": {"linux-x64": {"relay_sha256": "1" * 64}, "darwin-arm64": {"relay_sha256": "2" * 64}}}, "darwin-arm64", "darwin-relay-baseline"),
            ):
                self.write_verification(path, extras=data)
                with self.subTest(message=message), self.assertRaisesRegex(artifact_ci.InputError, message):
                    artifact_ci.relay_hash(path, platform)
            path.write_text('{"ok":true,"ok":false,"data":{}}', encoding="utf-8")
            with self.assertRaisesRegex(artifact_ci.InputError, "duplicate-json-key"):
                artifact_ci.relay_hash(path, "linux-x64")

    def test_download_mode_restore_is_narrow_then_r2a_checks_content(self):
        with tempfile.TemporaryDirectory() as directory:
            fixture = BundleFixture(Path(directory))
            transfer = Path(directory) / "actions-artifact.zip"
            with zipfile.ZipFile(transfer, "w", compression=zipfile.ZIP_DEFLATED) as archive:
                for item in fixture.bundle.iterdir():
                    archive.write(item, arcname=item.name)
            downloaded = Path(directory) / "downloaded-bundle"
            downloaded.mkdir()
            with zipfile.ZipFile(transfer) as archive:
                archive.extractall(downloaded)
            fixture.bundle = downloaded
            raw = [path for path in fixture.bundle.iterdir() if path.name.startswith("hyphae-") and not path.name.endswith(".tar.gz")]
            self.assertEqual(len(raw), 4)
            self.assertTrue(all(path.stat().st_mode & 0o777 == 0o644 for path in raw))
            with self.assertRaisesRegex(artifact_ci.verifier.VerifyError, "raw-binary-mode"):
                artifact_ci.verifier.verify_bundle(fixture.bundle, fixture.lock_path, **fixture.expected)
            artifact_ci.prepare_download(fixture.bundle)
            self.assertTrue(all(path.stat().st_mode & 0o777 == 0o755 for path in raw))
            artifact_ci.verifier.verify_bundle(fixture.bundle, fixture.lock_path, **fixture.expected)

            (fixture.bundle / "hyphae-linux-amd64").write_bytes(b"tampered artifact")
            artifact_ci.prepare_download(fixture.bundle)
            with self.assertRaises(artifact_ci.verifier.VerifyError):
                artifact_ci.verifier.verify_bundle(fixture.bundle, fixture.lock_path, **fixture.expected)

    def test_download_mode_restore_rejects_links_and_leaves_other_modes_untouched(self):
        with tempfile.TemporaryDirectory() as directory:
            fixture = BundleFixture(Path(directory))
            raw = fixture.bundle / "hyphae-darwin-arm64"
            target = fixture.bundle / "hyphae-relay-darwin-arm64"
            raw.unlink()
            raw.symlink_to(target)
            target.chmod(0o644)
            with self.assertRaisesRegex(artifact_ci.InputError, "raw-binary-open-failed"):
                artifact_ci.prepare_download(fixture.bundle)
            self.assertEqual(target.stat().st_mode & 0o777, 0o644)

    def test_download_mode_restore_rejects_fifo_without_blocking(self):
        if not hasattr(__import__("os"), "mkfifo"):
            self.skipTest("mkfifo is unavailable")
        with tempfile.TemporaryDirectory() as directory:
            fixture = BundleFixture(Path(directory))
            raw = fixture.bundle / "hyphae-darwin-arm64"
            raw.unlink()
            __import__("os").mkfifo(raw)
            result = subprocess.run(
                [sys.executable, "scripts/artifact_ci.py", "prepare-download", "--bundle-dir", str(fixture.bundle)],
                cwd=ROOT, capture_output=True, text=True, check=False, timeout=2,
            )
            self.assertEqual(result.returncode, 1)
            self.assertIn("raw-binary-type-or-size", result.stderr)

    def test_workflow_is_read_only_pinned_and_runs_two_real_hosts(self):
        text = WORKFLOW.read_text(encoding="utf-8")
        for token in (
            "pull_request:", "push:", "workflow_dispatch:",
            "permissions:\n  contents: read", "timeout-minutes:",
            "3d3c42e5aac5ba805825da76410c181273ba90b1",
            "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e",
            "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
            "3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
            "digest-mismatch: error", "runner: ubuntu-24.04", "runner: macos-15",
            "platform: linux-x64", "platform: darwin-arm64", "set -euo pipefail",
            "scripts/test_comm_round1.py", "--expected-relay-sha256",
            "persist-credentials: false", "submodules: false",
        ):
            with self.subTest(token=token):
                self.assertIn(token, text)
        self.assertNotIn("schedule:", text)
        self.assertNotIn("actions: write", text)
        self.assertNotIn("contents: write", text)
        self.assertNotIn("softprops/action-gh-release", text)
        self.assertNotIn("gh release", text)
        self.assertNotIn("release upload", text.lower())


if __name__ == "__main__":
    unittest.main()
