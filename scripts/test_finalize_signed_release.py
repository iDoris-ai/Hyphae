"""Tests for finalize_signed_release.py using a synthetic bundle and a fake codesign/spctl runner."""

from __future__ import annotations

import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import build_release_artifacts as build  # noqa: E402
import finalize_signed_release as fin  # noqa: E402

TAG = "v9.9.9-test"
MACHO = fin.MACHO_ARM64 + b"signed-cli"
GOOD_DETAILS = f"Identifier=hyphae\nCodeDirectory v=20500 size=1 flags=0x10000(runtime) hashes=1\nTeamIdentifier={fin.TEAM_ID}\n"


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


SPCTL_OK = "x: accepted\nsource=Notarized Developer ID\n"


class FakeRunner:
    def __init__(self, *, fail: str = "", details: str = GOOD_DETAILS, spctl: str = SPCTL_OK) -> None:
        self.calls: list[list[str]] = []
        self.fail, self.details, self.spctl = fail, details, spctl

    def __call__(self, args: list[str]) -> tuple[int, str, str]:
        self.calls.append(args)
        if self.fail == args[0]:
            return 1, "", "controlled failure"
        if args[0] == "spctl":
            return 0, "", self.spctl
        return 0, "", (self.details if "-dv" in args else "")


class FinalizeTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory(prefix="hyphae-finalize-test-")
        self.base = Path(self.temp.name)
        self.source = self.base / "source"
        for path in ("cmd/hyphae", "cmd/hyphae-relay"):
            (self.source / path).mkdir(parents=True)
            (self.source / path / "main.go").write_text("package main\n")
        (self.source / "LICENSE").write_text("license\n")
        (self.source / "NOTICE").write_text("notice\n")
        env = {**os.environ, "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@e.invalid", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@e.invalid"}
        subprocess.run(["git", "init", "-q", str(self.source)], check=True)
        subprocess.run(["git", "-C", str(self.source), "add", "."], check=True)
        subprocess.run(["git", "-C", str(self.source), "commit", "-q", "-m", "x"], check=True, env=env)
        self.commit = subprocess.check_output(["git", "-C", str(self.source), "rev-parse", "HEAD"], text=True).strip()
        self.unsigned = self.base / "unsigned"
        self.make_unsigned()
        self.signed = self.base / "signed"
        self.signed.mkdir()
        (self.signed / "hyphae-darwin-arm64").write_bytes(MACHO)
        self.out = self.base / "final"

    def tearDown(self) -> None:
        self.temp.cleanup()

    def make_unsigned(self) -> None:
        self.unsigned.mkdir()
        records, hashes = {}, {}
        for key, suffix in (("darwin-arm64", "darwin-arm64"), ("linux-x64", "linux-amd64")):
            cli, relay = f"hyphae-{suffix}", f"hyphae-relay-{suffix}"
            (self.unsigned / cli).write_bytes(b"unsigned-cli-" + suffix.encode())
            (self.unsigned / relay).write_bytes(b"unsigned-relay-" + suffix.encode())
            archive = f"hyphae-{suffix}.tar.gz"
            build.write_archive(self.unsigned / archive, self.source, self.unsigned / cli, self.unsigned / relay)
            records[key] = {
                "os": suffix.split("-")[0], "arch": suffix.split("-")[1],
                "binaries": {
                    "hyphae": {"filename": cli, "sha256": build.sha256_file(self.unsigned / cli)},
                    "hyphae-relay": {"filename": relay, "sha256": build.sha256_file(self.unsigned / relay)},
                },
                "archive": {"filename": archive, "sha256": build.sha256_file(self.unsigned / archive)},
            }
            for name in (cli, relay, archive):
                hashes[name] = build.sha256_file(self.unsigned / name)
        manifest = {"schema": 1, "release_tag": TAG, "publication_status": "intended; not published by this tool",
                    "source": {"sha": self.commit, "clean": True}, "go": build.GO_VERSION, "recipe": build.RECIPE,
                    "platforms": records, "intended_urls": {"release": "r", "assets": {}}}
        build.write_json(self.unsigned / "artifact-manifest.json", manifest)
        build.write_json(self.unsigned / "hyphae.lock.json", {"schema": 1})
        hashes["artifact-manifest.json"] = build.sha256_file(self.unsigned / "artifact-manifest.json")
        hashes["hyphae.lock.json"] = build.sha256_file(self.unsigned / "hyphae.lock.json")
        (self.unsigned / "SHA256SUMS").write_text("".join(f"{d}  {n}\n" for n, d in sorted(hashes.items())))

    def run_main(self, runner: FakeRunner | None = None, *extra: str, platform: str = "darwin") -> tuple[int, str]:
        argv = ["--unsigned-bundle", str(self.unsigned), "--signed-dir", str(self.signed),
                "--source-dir", str(self.source), "--output", str(self.out), *extra]
        err = io.StringIO()
        with mock.patch.object(sys, "platform", platform), contextlib.redirect_stderr(err), contextlib.redirect_stdout(io.StringIO()):
            code = fin.main(argv, fin.MacVerifier(runner or FakeRunner()))
        return code, err.getvalue()

    def read(self, name: str) -> dict:
        return json.loads((self.out / name).read_text())

    def assert_no_output(self, code: int) -> None:
        self.assertNotEqual(code, 0)
        self.assertFalse(self.out.exists())
        self.assertEqual([p.name for p in self.base.iterdir() if p.name.startswith(".hyphae-final-")], [])

    def test_happy_path_contents(self) -> None:
        code, err = self.run_main(FakeRunner())
        self.assertEqual(code, 0, err)
        signed_sha = sha(MACHO)
        unsigned_cli = sha(b"unsigned-cli-darwin-arm64")
        linux_sha = sha(b"unsigned-cli-linux-amd64")
        lock = self.read("hyphae.lock.json")
        self.assertEqual(set(lock), {"schema", "id", "version", "source", "contract", "release_url_base", "assets", "signing", "reproducible", "stop_grace_ms", "provides", "requires", "assurance"})
        self.assertEqual(lock["schema"], 2)
        self.assertEqual(lock["id"], "hyphae")
        self.assertEqual(lock["version"], TAG)
        self.assertEqual(lock["source"], {"repo": "https://github.com/iDoris-ai/Hyphae", "commit": self.commit})
        self.assertEqual(lock["contract"], {"name": "hyphae-cli", "major": 1, "min_minor": 0, "tested": "1.0"})
        self.assertEqual(lock["release_url_base"], "https://github.com/iDoris-ai/Hyphae/releases/download")
        self.assertEqual(lock["assets"], {
            "darwin-arm64": {"name": "hyphae-darwin-arm64", "sha256": signed_sha, "files": ["hyphae"], "executables": ["hyphae"]},
            "linux-x64": {"name": "hyphae-linux-amd64", "sha256": linux_sha, "files": ["hyphae"], "executables": ["hyphae"]}})
        self.assertEqual(lock["signing"], {"macos_team_id": "HYAP444FN9", "notarized": True})
        self.assertEqual(lock["reproducible"], {"toolchain": "go1.26.4", "recipe": build.RECIPE,
                                                 "unsigned_sha256": {"darwin-arm64": unsigned_cli, "linux-x64": linux_sha}})
        self.assertEqual(lock["stop_grace_ms"], 15000)
        self.assertEqual(lock["provides"], [{"id": i} for i in ("nostr-dm", "nostr-group", "nostr-identity", "nostr-relay")])
        self.assertEqual(lock["requires"], {"kernel": ["events"], "operations": []})
        self.assertEqual(lock["assurance"], [])
        self.assertNotIn("null", (self.out / "hyphae.lock.json").read_text())
        manifest = self.read("artifact-manifest.json")
        self.assertEqual(manifest["schema"], 1)
        self.assertIs(manifest["signature_verified"], True)
        self.assertEqual(manifest["publication_status"], "signed; not published by this tool")
        cli = manifest["platforms"]["darwin-arm64"]["binaries"]["hyphae"]
        self.assertEqual(cli, {"filename": "hyphae-darwin-arm64", "sha256": signed_sha, "unsigned_sha256": unsigned_cli})
        # relay was not signed: unsigned bytes pass through
        self.assertEqual((self.out / "hyphae-relay-darwin-arm64").read_bytes(), b"unsigned-relay-darwin-arm64")
        self.assertEqual(
            manifest["intended_urls"]["assets"]["hyphae-darwin-arm64"],
            f"https://github.com/iDoris-ai/hyphae/releases/download/{TAG}/hyphae-darwin-arm64",
        )
        # linux pass-through, byte for byte
        for name in ("hyphae-linux-amd64", "hyphae-relay-linux-amd64", "hyphae-linux-amd64.tar.gz"):
            self.assertEqual((self.out / name).read_bytes(), (self.unsigned / name).read_bytes())
        # archive deterministic and contains signed bytes
        with tarfile.open(self.out / "hyphae-darwin-arm64.tar.gz") as archive:
            self.assertEqual(archive.extractfile("hyphae").read(), MACHO)
        again = self.base / "again"
        build.write_archive(again, self.source, self.out / "hyphae-darwin-arm64", self.out / "hyphae-relay-darwin-arm64")
        self.assertEqual(again.read_bytes(), (self.out / "hyphae-darwin-arm64.tar.gz").read_bytes())
        # SHA256SUMS lists every other file, sorted, and matches
        lines = (self.out / "SHA256SUMS").read_text().splitlines()
        sums = {line.split("  ", 1)[1]: line.split("  ", 1)[0] for line in lines}
        self.assertEqual(set(sums), {p.name for p in self.out.iterdir()} - {"SHA256SUMS"})
        self.assertEqual([line.split("  ", 1)[1] for line in lines], sorted(sums))
        for name, digest in sums.items():
            self.assertEqual(build.sha256_file(self.out / name), digest)
        for name in ("hyphae-darwin-arm64", "hyphae-relay-darwin-arm64", "hyphae-linux-amd64", "hyphae-relay-linux-amd64"):
            self.assertEqual((self.out / name).stat().st_mode & 0o777, 0o755, name)
        self.assertEqual(manifest["signed_binaries"], ["hyphae-darwin-arm64"])

    def test_spctl_uses_install_assess_type(self) -> None:
        runner = FakeRunner()
        self.assertEqual(self.run_main(runner)[0], 0)
        spctl = [c for c in runner.calls if c[0] == "spctl"]
        self.assertEqual(len(spctl), 1)
        self.assertEqual(spctl[0][:5], ["spctl", "--assess", "--type", "install", "-vv"])
        self.assertNotIn("execute", spctl[0])
        self.assertEqual(runner.calls[0][:3], ["codesign", "--verify", "--strict"])
        self.assertTrue(runner.calls[0][3].endswith("/hyphae-darwin-arm64"))

    def test_signed_relay_is_used_and_verified(self) -> None:
        (self.signed / "hyphae-relay-darwin-arm64").write_bytes(fin.MACHO_ARM64 + b"signed-relay")
        runner = FakeRunner()
        self.assertEqual(self.run_main(runner)[0], 0)
        self.assertEqual(len([c for c in runner.calls if c[0] == "spctl"]), 2)
        self.assertEqual((self.out / "hyphae-relay-darwin-arm64").read_bytes(), fin.MACHO_ARM64 + b"signed-relay")

    def test_refuses_existing_output(self) -> None:
        self.out.mkdir()
        (self.out / "keep").write_text("x")
        code, err = self.run_main()
        self.assertNotEqual(code, 0)
        self.assertIn("nonexistent directory", err)
        self.assertEqual((self.out / "keep").read_text(), "x")

    def test_refuses_extra_or_missing_signed_files(self) -> None:
        (self.signed / "hyphae-linux-amd64").write_bytes(MACHO)  # looks valid; must still be refused
        code, err = self.run_main()
        self.assert_no_output(code)
        self.assertIn("optionally", err)
        (self.signed / "hyphae-linux-amd64").unlink()
        (self.signed / "hyphae-darwin-arm64").unlink()
        self.assert_no_output(self.run_main()[0])

    def test_refuses_signed_identical_to_unsigned(self) -> None:
        (self.signed / "hyphae-darwin-arm64").write_bytes((self.unsigned / "hyphae-darwin-arm64").read_bytes())
        # make the identical file look like Mach-O so only the identity guard can reject it
        unsigned_path = self.unsigned / "hyphae-darwin-arm64"
        unsigned_path.write_bytes(MACHO)
        manifest = json.loads((self.unsigned / "artifact-manifest.json").read_text())
        manifest["platforms"]["darwin-arm64"]["binaries"]["hyphae"]["sha256"] = build.sha256_file(unsigned_path)
        build.write_json(self.unsigned / "artifact-manifest.json", manifest)
        (self.signed / "hyphae-darwin-arm64").write_bytes(MACHO)
        code, err = self.run_main()
        self.assert_no_output(code)
        self.assertIn("identical", err)

    def test_signed_input_swapped_after_copy_is_not_what_gets_verified(self) -> None:
        # The verifier must see the staged copy, never the mutable signed-dir path.
        runner = FakeRunner()
        self.assertEqual(self.run_main(runner)[0], 0)
        paths = {c[-1] for c in runner.calls}
        self.assertTrue(all(str(self.signed) not in p and str(self.signed.resolve()) not in p for p in paths), paths)

    def test_refuses_archive_with_wrong_binary(self) -> None:
        wrong = self.base / "wrong"
        wrong.write_bytes(b"other")
        build.write_archive(self.unsigned / "hyphae-linux-amd64.tar.gz", self.source, wrong, self.unsigned / "hyphae-relay-linux-amd64")
        manifest = json.loads((self.unsigned / "artifact-manifest.json").read_text())
        manifest["platforms"]["linux-x64"]["archive"]["sha256"] = build.sha256_file(self.unsigned / "hyphae-linux-amd64.tar.gz")
        build.write_json(self.unsigned / "artifact-manifest.json", manifest)
        code, err = self.run_main()
        self.assert_no_output(code)
        self.assertIn("does not contain the recorded", err)

    def test_refuses_non_macho(self) -> None:
        (self.signed / "hyphae-darwin-arm64").write_bytes(b"#!/bin/sh\n")
        code, err = self.run_main()
        self.assert_no_output(code)
        self.assertIn("Mach-O", err)

    def test_refuses_tampered_unsigned_bundle(self) -> None:
        (self.unsigned / "hyphae-linux-amd64").write_bytes(b"tampered")
        code, err = self.run_main()
        self.assert_no_output(code)
        self.assertIn("recorded sha256", err)

    def test_refuses_extra_file_in_unsigned_bundle(self) -> None:
        (self.unsigned / "stray").write_bytes(b"x")
        code, err = self.run_main()
        self.assert_no_output(code)
        self.assertIn("extra files", err)

    def test_verification_failures_leave_nothing(self) -> None:
        cases = {
            "bad codesign": FakeRunner(fail="codesign"),
            "wrong team": FakeRunner(details=GOOD_DETAILS.replace(fin.TEAM_ID, "OTHERTEAM1")),
            "no runtime": FakeRunner(details=GOOD_DETAILS.replace("(runtime)", "(none)")),
            "spctl fail": FakeRunner(fail="spctl"),
            "not notarized": FakeRunner(spctl="x: accepted\nsource=Developer ID\n"),
        }
        for label, runner in cases.items():
            with self.subTest(label):
                code, _ = self.run_main(runner)
                self.assert_no_output(code)

    def test_refuses_non_macos_without_escape_hatch(self) -> None:
        code, err = self.run_main(None, platform="linux")
        self.assert_no_output(code)
        self.assertIn("needs macOS", err)

    def test_escape_hatch_marks_unverified(self) -> None:
        (self.signed / "hyphae-darwin-arm64").write_bytes(b"not macho, skipped checks")
        runner = FakeRunner()
        code, err = self.run_main(runner, "--no-verify-signature", platform="linux")
        self.assertEqual(code, 0, err)
        self.assertIn("WARNING", err)
        self.assertEqual(runner.calls, [])
        self.assertIs(self.read("artifact-manifest.json")["signature_verified"], False)
        self.assertIs(self.read("hyphae.lock.json")["signing"]["notarized"], False)

    def test_contract_constants_match_go(self) -> None:
        go_file = Path(__file__).resolve().parents[1] / "internal" / "contract" / "contract.go"
        if not go_file.exists():
            self.skipTest("internal/contract/contract.go not present on this base (PR #338)")
        text = go_file.read_text()
        tested = re.search(r'(?i)tested\w*\s*=\s*"([^"]+)"', text)
        self.assertIsNotNone(tested)
        self.assertEqual(tested.group(1), fin.CONTRACT_TESTED)
        self.assertEqual(set(re.findall(r'"(nostr-[a-z]+)"', text)), set(fin.PROVIDES))


if __name__ == "__main__":
    unittest.main()
