"""Black-box and fixture tests for verify_artifact_bundle.py."""

from __future__ import annotations

import gzip
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("verify_artifact_bundle.py")
spec = importlib.util.spec_from_file_location("verify_artifact_bundle", SCRIPT)
verifier = importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(verifier)

SOURCE = "a4aa606eb81d5c040d94c51cdf94553e646d8674"
GO = "go1.26.4"
RECIPE = verifier.RECIPE
RELAY_RECIPE = verifier.RELAY_RECIPE
PLATFORMS = verifier.PLATFORMS


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def write_archive(path: Path, members: dict[str, bytes], *, unsafe: str | None = None) -> None:
    with path.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as gz:
            with tarfile.open(fileobj=gz, mode="w|", format=tarfile.USTAR_FORMAT) as archive:
                for name in sorted(members):
                    if (unsafe in ("symlink", "hardlink") and name == "hyphae") or (unsafe == "traversal" and name == "LICENSE"):
                        continue
                    info = tarfile.TarInfo(name)
                    info.size = len(members[name])
                    info.mtime = info.uid = info.gid = 0
                    info.uname = info.gname = ""
                    info.mode = verifier.ARCHIVE_MEMBERS[name]
                    archive.addfile(info, __import__("io").BytesIO(members[name]))
                if unsafe in ("symlink", "hardlink"):
                    link = tarfile.TarInfo("hyphae")
                    link.type = tarfile.SYMTYPE if unsafe == "symlink" else tarfile.LNKTYPE
                    link.linkname = "hyphae-relay"
                    archive.addfile(link)
                elif unsafe == "traversal":
                    entry = tarfile.TarInfo("../LICENSE")
                    entry.size = 1
                    archive.addfile(entry, __import__("io").BytesIO(b"x"))
                elif unsafe == "duplicate":
                    duplicate = tarfile.TarInfo("hyphae")
                    duplicate.size = 1
                    duplicate.mode = 0o755
                    archive.addfile(duplicate, __import__("io").BytesIO(b"x"))


def rewrite_sums(directory: Path) -> None:
    rows = []
    for path in sorted(directory.iterdir()):
        if path.name != "SHA256SUMS":
            rows.append(f"{sha(path.read_bytes())}  {path.name}\n")
    (directory / "SHA256SUMS").write_text("".join(rows), encoding="ascii")


def refresh_bundle_metadata(directory: Path, *, field: str | None = None, value=None, archive_platform: str | None = None) -> None:
    manifest_path = directory / "artifact-manifest.json"
    manifest = json.loads(manifest_path.read_text())
    if field is not None:
        manifest[field] = value
    if archive_platform is not None:
        record = manifest["platforms"][archive_platform]
        archive = directory / record["archive"]["filename"]
        record["archive"]["sha256"] = sha(archive.read_bytes())
    manifest_path.write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n")
    rewrite_sums(directory)


class BundleFixture:
    def __init__(self, root: Path):
        self.root = root
        self.bundle = root / "bundle"
        self.bundle.mkdir()
        self.lock = {
            "schema": 1,
            "source_sha": SOURCE,
            "go": GO,
            "recipe": RECIPE,
            "binaries": {
                "darwin-arm64": "0" * 64,
                "linux-x64": "0" * 64,
                "darwin-x64": None,
                "linux-arm64": None,
            },
        }
        self.lock_path = root / "production-lock.json"
        self.records: dict[str, dict] = {}
        checksums: dict[str, str] = {}
        raw_data: dict[str, dict[str, bytes]] = {}
        for key, platform in PLATFORMS.items():
            suffix = platform["suffix"]
            cli = f"fixture-cli-{key}".encode()
            relay = f"fixture-relay-{key}".encode()
            self.lock["binaries"][key] = sha(cli)
            raw_data[key] = {"hyphae": cli, "hyphae-relay": relay}
            names = {"hyphae": f"hyphae-{suffix}", "hyphae-relay": f"hyphae-relay-{suffix}"}
            binaries = {}
            for kind, filename in names.items():
                data = raw_data[key][kind]
                (self.bundle / filename).write_bytes(data)
                (self.bundle / filename).chmod(0o755)
                checksums[filename] = sha(data)
                binaries[kind] = {"filename": filename, "sha256": sha(data)}
            archive_name = f"hyphae-{suffix}.tar.gz"
            archive_path = self.bundle / archive_name
            write_archive(archive_path, {"LICENSE": b"license", "NOTICE": b"notice", "hyphae": cli, "hyphae-relay": relay})
            checksums[archive_name] = sha(archive_path.read_bytes())
            self.records[key] = {
                "os": platform["os"], "arch": platform["arch"], "binaries": binaries,
                "archive": {"filename": archive_name, "sha256": checksums[archive_name]},
            }
        self.lock_bytes = (json.dumps(self.lock, sort_keys=True, indent=2) + "\n").encode()
        self.lock_path.write_bytes(self.lock_bytes)
        (self.bundle / "hyphae.lock.json").write_bytes(self.lock_bytes)
        checksums["hyphae.lock.json"] = sha(self.lock_bytes)
        intended_base = "https://github.com/iDoris-ai/hyphae/releases/download/test-tag"
        manifest = {
            "schema": 1,
            "release_tag": "test-tag",
            "publication_status": "intended; not published by this tool",
            "source": {"sha": SOURCE, "clean": True},
            "go": GO,
            "recipe": RECIPE,
            "relay_recipe": RELAY_RECIPE,
            "platforms": self.records,
            "intended_urls": {
                "release": "https://github.com/iDoris-ai/hyphae/releases/tag/test-tag",
                "assets": {name: f"{intended_base}/{name}" for name in sorted(verifier.artifact_names())},
            },
        }
        manifest_bytes = (json.dumps(manifest, sort_keys=True, indent=2) + "\n").encode()
        (self.bundle / "artifact-manifest.json").write_bytes(manifest_bytes)
        checksums["artifact-manifest.json"] = sha(manifest_bytes)
        rewrite_sums(self.bundle)
        self.expected = {
            "expected_lock_sha256": sha(self.lock_bytes),
            "expected_lock": self.lock,
            "expected_darwin_relay_sha256": None,
        }

    def verify(self):
        return verifier.verify_bundle(self.bundle, self.lock_path, **self.expected)


class VerifyArtifactBundleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.fixture = BundleFixture(Path(self.temp.name))

    def test_valid_fixture_bundle_checks_hashes_and_archive_members(self):
        result = self.fixture.verify()
        self.assertEqual(result["source_sha"], SOURCE)
        self.assertFalse(result["published"])
        self.assertEqual(set(result["platforms"]), set(PLATFORMS))

    def test_manifest_tampering_fails_even_when_sums_are_rewritten(self):
        path = self.fixture.bundle / "artifact-manifest.json"
        manifest = json.loads(path.read_text())
        manifest["go"] = "go1.27.1"
        path.write_text(json.dumps(manifest))
        rewrite_sums(self.fixture.bundle)
        with self.assertRaisesRegex(verifier.VerifyError, "manifest-go"):
            self.fixture.verify()

    def test_manifest_source_recipe_platform_and_missing_fields_are_checked(self):
        cases = (("source", {"sha": "0" * 40, "clean": True}, "manifest-source"),
                 ("recipe", "untrusted command", "manifest-recipe"),
                 ("platforms", {}, "manifest-platform-set"))
        for field, value, expected in cases:
            with self.subTest(field=field):
                self.fixture = BundleFixture(Path(tempfile.mkdtemp(dir=self.temp.name)))
                refresh_bundle_metadata(self.fixture.bundle, field=field, value=value)
                with self.assertRaisesRegex(verifier.VerifyError, expected):
                    self.fixture.verify()

    def test_archive_or_raw_hash_tampering_fails(self):
        raw = self.fixture.bundle / "hyphae-darwin-arm64"
        raw.write_bytes(b"changed")
        manifest_path = self.fixture.bundle / "artifact-manifest.json"
        manifest = json.loads(manifest_path.read_text())
        manifest["platforms"]["darwin-arm64"]["binaries"]["hyphae"]["sha256"] = sha(b"changed")
        manifest_path.write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n")
        rewrite_sums(self.fixture.bundle)
        with self.assertRaisesRegex(verifier.VerifyError, "production-cli-sha256"):
            self.fixture.verify()

    def test_missing_archive_member_is_rejected_after_hashes_are_consistent(self):
        archive = self.fixture.bundle / "hyphae-darwin-arm64.tar.gz"
        write_archive(archive, {"LICENSE": b"license", "NOTICE": b"notice", "hyphae": b"fixture-cli-darwin-arm64"})
        refresh_bundle_metadata(self.fixture.bundle, archive_platform="darwin-arm64")
        with self.assertRaisesRegex(verifier.VerifyError, "archive-member-set"):
            self.fixture.verify()

    def test_archive_rejects_symlink_and_path_traversal_members(self):
        archive = self.fixture.bundle / "hyphae-darwin-arm64.tar.gz"
        members = {"LICENSE": b"license", "NOTICE": b"notice", "hyphae": b"fixture-cli-darwin-arm64", "hyphae-relay": b"fixture-relay-darwin-arm64"}
        for kind, expected in (("symlink", "archive-member-type"), ("hardlink", "archive-member-type"), ("traversal", "archive-member-name"), ("duplicate", "archive-member-name")):
            write_archive(archive, members, unsafe=kind)
            refresh_bundle_metadata(self.fixture.bundle, archive_platform="darwin-arm64")
            with self.assertRaisesRegex(verifier.VerifyError, expected):
                self.fixture.verify()
            # Restore the canonical archive for the next malformed form.
            write_archive(archive, members)
            rewrite_sums(self.fixture.bundle)

    def test_extra_and_symlink_bundle_entries_are_rejected(self):
        extra = self.fixture.bundle / "ignored.txt"
        extra.write_text("x")
        with self.assertRaisesRegex(verifier.VerifyError, "bundle-file-set"):
            self.fixture.verify()
        extra.unlink()
        raw = self.fixture.bundle / "hyphae-linux-amd64"
        contents = raw.read_bytes()
        raw.unlink()
        raw.symlink_to(self.fixture.bundle / "hyphae-relay-linux-amd64")
        with self.assertRaises(verifier.VerifyError):
            self.fixture.verify()
        raw.unlink()
        raw.write_bytes(contents)
        raw.chmod(0o755)

    def test_lock_mismatch_is_rejected_before_bundle_acceptance(self):
        self.fixture.lock_path.write_text("{}")
        with self.assertRaisesRegex(verifier.VerifyError, "production-lock-sha256"):
            self.fixture.verify()

    def test_cli_uses_fixed_production_lock_and_never_fixture_override(self):
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "--bundle-dir", str(self.fixture.bundle), "--production-lock", str(self.fixture.lock_path), "--platform", "all"],
            capture_output=True, text=True, check=False,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("production-lock-sha256", result.stderr)

    def test_extract_refuses_existing_destination_without_modifying_it(self):
        output = Path(self.temp.name) / "already-there"
        output.mkdir()
        sentinel = output / "sentinel"
        sentinel.write_text("keep")
        with self.assertRaisesRegex(verifier.VerifyError, "extract-output-exists"):
            verifier.extract_binaries(self.fixture.bundle, "darwin-arm64", output, {
                "hyphae": "hyphae-darwin-arm64", "hyphae-relay": "hyphae-relay-darwin-arm64",
            }, {"hyphae": (sha(b"fixture-cli-darwin-arm64"), len(b"fixture-cli-darwin-arm64")),
                "hyphae-relay": (sha(b"fixture-relay-darwin-arm64"), len(b"fixture-relay-darwin-arm64"))})
        self.assertEqual(sentinel.read_text(), "keep")

    def test_successful_extract_matches_validated_hashes(self):
        result = self.fixture.verify()
        output = Path(self.temp.name) / "extracted"
        extracted = verifier.extract_binaries(
            self.fixture.bundle, "darwin-arm64", output,
            {"hyphae": "hyphae-darwin-arm64", "hyphae-relay": "hyphae-relay-darwin-arm64"},
            result["_raw_files"]["darwin-arm64"],
        )
        self.assertEqual((extracted / "hyphae").read_bytes(), b"fixture-cli-darwin-arm64")
        self.assertEqual((extracted / "hyphae-relay").read_bytes(), b"fixture-relay-darwin-arm64")
        self.assertEqual((extracted / "hyphae").stat().st_mode & 0o777, 0o755)

    def test_extract_rechecks_validated_bytes_and_rejects_replacement_link(self):
        output = Path(self.temp.name) / "extract"
        validated = {"hyphae": (sha(b"fixture-cli-darwin-arm64"), len(b"fixture-cli-darwin-arm64")),
                     "hyphae-relay": (sha(b"fixture-relay-darwin-arm64"), len(b"fixture-relay-darwin-arm64"))}
        raw = self.fixture.bundle / "hyphae-darwin-arm64"
        raw.unlink()
        raw.write_bytes(b"X" * len(b"fixture-cli-darwin-arm64"))
        with self.assertRaises(verifier.VerifyError):
            verifier.extract_binaries(self.fixture.bundle, "darwin-arm64", output, {
                "hyphae": raw.name, "hyphae-relay": "hyphae-relay-darwin-arm64",
            }, validated)
        self.assertFalse(output.exists())

    def test_extract_rejects_symlink_replacement_after_validation(self):
        output = Path(self.temp.name) / "extract-symlink"
        raw = self.fixture.bundle / "hyphae-darwin-arm64"
        raw.unlink()
        raw.symlink_to(self.fixture.bundle / "hyphae-relay-darwin-arm64")
        expected = {"hyphae": (sha(b"fixture-cli-darwin-arm64"), len(b"fixture-cli-darwin-arm64")),
                    "hyphae-relay": (sha(b"fixture-relay-darwin-arm64"), len(b"fixture-relay-darwin-arm64"))}
        with self.assertRaises(verifier.VerifyError):
            verifier.extract_binaries(self.fixture.bundle, "darwin-arm64", output, {
                "hyphae": raw.name, "hyphae-relay": "hyphae-relay-darwin-arm64",
            }, expected)
        self.assertFalse(output.exists())

    def test_archive_reader_bounds_decompressed_headers_and_members(self):
        class Expanded:
            def read(self, count=-1):
                return b"x" * (count if count >= 0 else 1)
        reader = verifier.BoundedReader(Expanded(), 32)
        self.assertEqual(len(reader.read(32)), 32)
        with self.assertRaisesRegex(verifier.VerifyError, "archive-expanded-budget"):
            reader.read(1)


if __name__ == "__main__":
    unittest.main()
