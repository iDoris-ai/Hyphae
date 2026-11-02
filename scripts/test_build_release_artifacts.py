"""Black-box tests for build_release_artifacts.py using a fake Go executable."""

from __future__ import annotations

import gzip
import importlib.util
import shutil
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("build_release_artifacts.py").resolve()
EXPECTED_RECIPE = (
    "GOTOOLCHAIN=go1.26.4 CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build "
    "-trimpath -buildvcs=false -ldflags='-buildid=' -o hyphae ./cmd/hyphae"
)


class ArtifactBuilderTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory(prefix="hyphae-artifact-test-")
        self.base = Path(self.temp.name)
        self.source = self.base / "source"
        self.fake_bin = self.base / "fake-bin"
        self.fake_bin.mkdir()
        self.log = self.base / "go-commands.jsonl"
        self.mutated_file = self.source / "generated-during-build.go"
        self.fake_go = self.fake_bin / "go"
        self._write_fake_go()
        self.commit = self.make_source()

    def tearDown(self) -> None:
        self.temp.cleanup()

    def _write_fake_go(self, *, version: str = "go1.26.4", fail_target: str = "", mutate: bool = False) -> None:
        script = f"""#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
log = pathlib.Path({str(self.log)!r})
row = {{"args": args, "env": {{k: os.environ.get(k) for k in (
    "GOTOOLCHAIN", "GOENV", "GOWORK", "CGO_ENABLED", "GOOS", "GOARCH",
    "PATH", "GOPATH", "GOCACHE", "GOFLAGS", "GOEXPERIMENT", "GOROOT", "HOME"
) }}, "private_dirs_exist": {{"HOME": pathlib.Path(os.environ.get("HOME", "/missing")).is_dir(),
    "GOCACHE": pathlib.Path(os.environ.get("GOCACHE", "/missing")).is_dir()}}}}
with log.open("a", encoding="utf-8") as f:
    f.write(json.dumps(row, sort_keys=True) + "\\n")
if args == ["version"]:
    print("go version {version} fake/test")
    raise SystemExit(0)
if {fail_target!r} and args[-1] == {fail_target!r}:
    print("controlled fake Go failure", file=sys.stderr)
    raise SystemExit(9)
if {mutate!r}:
    pathlib.Path({str(self.mutated_file)!r}).write_text("// injected\\n", encoding="utf-8")
target = args[-1]
output = pathlib.Path(args[args.index("-o") + 1])
output.parent.mkdir(parents=True, exist_ok=True)
output.write_bytes((os.environ["GOOS"] + "/" + os.environ["GOARCH"] + ":" + target + ":" + " ".join(a for a in args if a.startswith("-ldflags"))).encode())
"""
        self.fake_go.write_text(script, encoding="utf-8")
        self.fake_go.chmod(0o755)

    def make_source(self) -> str:
        for path in ("cmd/hyphae", "cmd/hyphae-relay"):
            (self.source / path).mkdir(parents=True)
            (self.source / path / "main.go").write_text("package main\n", encoding="utf-8")
        (self.source / "LICENSE").write_text("license fixture\n", encoding="utf-8")
        (self.source / "NOTICE").write_text("notice fixture\n", encoding="utf-8")
        subprocess.run(["git", "init", "-q", str(self.source)], check=True)
        subprocess.run(["git", "-C", str(self.source), "add", "."], check=True)
        env = os.environ.copy()
        env.update(
            {
                "GIT_AUTHOR_NAME": "Artifact Fixture",
                "GIT_AUTHOR_EMAIL": "fixture@example.invalid",
                "GIT_COMMITTER_NAME": "Artifact Fixture",
                "GIT_COMMITTER_EMAIL": "fixture@example.invalid",
            }
        )
        subprocess.run(["git", "-C", str(self.source), "commit", "-q", "-m", "fixture"], check=True, env=env)
        return subprocess.check_output(["git", "-C", str(self.source), "rev-parse", "HEAD"], text=True).strip()

    def environment(self) -> dict[str, str]:
        env = os.environ.copy()
        env.update(
            {
                "PATH": f"{self.fake_bin}{os.pathsep}{env['PATH']}",
                "GOPATH": str(self.base / "gopath"),
                "HOME": str(self.base / "hostile-home"),
                "GOFLAGS": "-tags=unapproved",
                "GOEXPERIMENT": "badexperiment",
                "GOROOT": "/invalid/goroot",
                "GOENV": str(self.base / "hostile-go-env"),
                "GOWORK": str(self.base / "hostile-go.work"),
                "FAKE_GO_LOG": str(self.log),
            }
        )
        env.pop("GOCACHE", None)
        return env

    def run_builder(
        self,
        output: Path,
        *,
        source_sha: str | None = None,
        env: dict[str, str] | None = None,
        tag: str = "v9.8.7-test",
        extra: tuple[str, ...] = (),
    ) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                sys.executable,
                str(SCRIPT),
                "--source-dir",
                str(self.source),
                "--source-sha",
                source_sha or self.commit,
                "--output",
                str(output),
                "--release-tag",
                tag,
                *extra,
            ],
            check=False,
            capture_output=True,
            text=True,
            env=env or self.environment(),
        )

    def log_rows(self) -> list[dict[str, object]]:
        if not self.log.exists():
            return []
        return [json.loads(line) for line in self.log.read_text(encoding="utf-8").splitlines()]

    def test_builds_two_platforms_and_writes_complete_deterministic_bundle(self) -> None:
        output_one = self.base / "bundle-one"
        output_two = self.base / "bundle-two"
        first = self.run_builder(output_one)
        self.assertEqual(first.returncode, 0, first.stderr)
        first_bytes = {path.name: path.read_bytes() for path in output_one.iterdir()}
        second = self.run_builder(output_two)
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(first_bytes, {path.name: path.read_bytes() for path in output_two.iterdir()})

        expected_names = {
            "hyphae-darwin-arm64",
            "hyphae-relay-darwin-arm64",
            "hyphae-darwin-arm64.tar.gz",
            "hyphae-linux-amd64",
            "hyphae-relay-linux-amd64",
            "hyphae-linux-amd64.tar.gz",
            "artifact-manifest.json",
            "hyphae.lock.json",
            "SHA256SUMS",
        }
        self.assertEqual({path.name for path in output_one.iterdir()}, expected_names)
        for name in ("hyphae-darwin-arm64", "hyphae-relay-darwin-arm64", "hyphae-linux-amd64", "hyphae-relay-linux-amd64"):
            self.assertEqual((output_one / name).stat().st_mode & 0o777, 0o755)
        manifest = json.loads((output_one / "artifact-manifest.json").read_text(encoding="utf-8"))
        self.assertEqual(manifest["source"], {"clean": True, "sha": self.commit})
        self.assertEqual(manifest["go"], "go1.26.4")
        self.assertEqual(manifest["recipe"], EXPECTED_RECIPE)
        self.assertEqual(manifest["publication_status"], "intended; not published by this tool")
        self.assertIn("/releases/download/v9.8.7-test/", manifest["intended_urls"]["assets"]["hyphae-linux-amd64.tar.gz"])
        self.assertEqual(set(manifest["intended_urls"]["assets"]), expected_names)

        lock = json.loads((output_one / "hyphae.lock.json").read_text(encoding="utf-8"))
        self.assertEqual(lock["schema"], 1)
        self.assertEqual(lock["source_sha"], self.commit)
        self.assertEqual(lock["go"], "go1.26.4")
        self.assertEqual(lock["recipe"], EXPECTED_RECIPE)
        self.assertEqual(set(lock["binaries"]), {"darwin-arm64", "linux-x64", "darwin-x64", "linux-arm64"})
        self.assertIsNone(lock["binaries"]["darwin-x64"])
        self.assertIsNone(lock["binaries"]["linux-arm64"])

        for platform in ("darwin-arm64", "linux-amd64"):
            archive_path = output_one / f"hyphae-{platform}.tar.gz"
            raw = archive_path.read_bytes()
            self.assertEqual(int.from_bytes(raw[4:8], "little"), 0, "gzip mtime must be zero")
            with tarfile.open(archive_path, "r:gz") as archive:
                members = archive.getmembers()
                self.assertEqual([item.name for item in members], ["LICENSE", "NOTICE", "hyphae", "hyphae-relay"])
                self.assertTrue(all(item.mtime == 0 and item.uid == 0 and item.gid == 0 for item in members))
                self.assertEqual([item.mode for item in members], [0o644, 0o644, 0o755, 0o755])

        sums = {}
        for line in (output_one / "SHA256SUMS").read_text(encoding="ascii").splitlines():
            digest, name = line.split("  ", 1)
            sums[name] = digest
        self.assertEqual(set(sums), expected_names - {"SHA256SUMS"})
        for name, digest in sums.items():
            self.assertEqual(hashlib.sha256((output_one / name).read_bytes()).hexdigest(), digest)
        for record in manifest["platforms"].values():
            for artifact in [*record["binaries"].values(), record["archive"]]:
                self.assertEqual(hashlib.sha256((output_one / artifact["filename"]).read_bytes()).hexdigest(), artifact["sha256"])

        builds = [row for row in self.log_rows() if row["args"][0] == "build"]
        self.assertEqual(len(builds), 8)
        self.assertEqual(
            {row["args"][-1] for row in builds},
            {"./cmd/hyphae", "./cmd/hyphae-relay"},
        )
        for row in builds:
            destination = Path(row["args"][row["args"].index("-o") + 1])
            self.assertTrue(destination.is_absolute())
            self.assertFalse(destination.is_relative_to(self.source))
        versions = [row for row in self.log_rows() if row["args"] == ["version"]]
        self.assertEqual(len(versions), 2)
        self.assertTrue(all(row["env"]["GOTOOLCHAIN"] == "local" for row in versions))
        for row in builds:
            args = row["args"]
            env = row["env"]
            self.assertEqual(args[1:4], ["-trimpath", "-buildvcs=false", "-ldflags=-buildid="])
            self.assertEqual(env["GOTOOLCHAIN"], "go1.26.4")
            self.assertEqual(env["GOENV"], "off")
            self.assertEqual(env["GOWORK"], "off")
            self.assertEqual(env["CGO_ENABLED"], "0")
            self.assertIn(env["GOOS"] + "/" + env["GOARCH"], {"darwin/arm64", "linux/amd64"})
            self.assertEqual(env["GOPATH"], str(self.base / "gopath"))
            private_home = Path(env["HOME"])
            private_cache = Path(env["GOCACHE"])
            self.assertEqual(private_home.name, "home")
            self.assertEqual(private_cache.name, "go-cache")
            self.assertEqual(private_home.parent, private_cache.parent)
            self.assertFalse(private_home.is_relative_to(self.source))
            self.assertFalse(private_cache.is_relative_to(self.source))
            self.assertTrue(row["private_dirs_exist"]["HOME"])
            self.assertTrue(row["private_dirs_exist"]["GOCACHE"])
            self.assertNotEqual(env["HOME"], self.environment()["HOME"])
            for forbidden in ("GOFLAGS", "GOEXPERIMENT", "GOROOT"):
                self.assertIsNone(env[forbidden])
        self.assertEqual(self.commit, subprocess.check_output(["git", "-C", str(self.source), "rev-parse", "HEAD"], text=True).strip())

    def test_default_recipe_is_the_locked_baseline_recipe(self) -> None:
        spec = importlib.util.spec_from_file_location("verify_artifact_bundle", SCRIPT.with_name("verify_artifact_bundle.py"))
        verifier = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(verifier)
        builder = self.load_builder()
        self.assertEqual(builder.RECIPE, EXPECTED_RECIPE)
        self.assertEqual(builder.RECIPE, verifier.RECIPE)
        self.assertEqual(builder.cli_recipe(), verifier.RECIPE)
        self.assertEqual(builder.RELAY_RECIPE, verifier.RELAY_RECIPE)
        output = self.base / "default"
        self.assertEqual(self.run_builder(output).returncode, 0)
        for name in ("artifact-manifest.json", "hyphae.lock.json"):
            self.assertEqual(json.loads((output / name).read_text())["recipe"], verifier.RECIPE)
        self.assertNotIn(b"main.version", (output / "hyphae-linux-amd64").read_bytes())

    def load_builder(self):
        spec = importlib.util.spec_from_file_location("build_release_artifacts", SCRIPT)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module

    def test_stamp_version_is_opt_in_and_changes_only_the_cli(self) -> None:
        default, one, two = self.base / "d", self.base / "t1", self.base / "t2"
        self.assertEqual(self.run_builder(default, tag="v1.0.0").returncode, 0)
        self.assertEqual(self.run_builder(one, tag="v1.0.0", extra=("--stamp-version",)).returncode, 0)
        self.assertEqual(self.run_builder(two, tag="v1.0.1", extra=("--stamp-version",)).returncode, 0)
        for suffix in ("darwin-arm64", "linux-amd64"):
            cli = f"hyphae-{suffix}"
            relay = f"hyphae-relay-{suffix}"
            self.assertNotEqual((default / cli).read_bytes(), (one / cli).read_bytes())
            self.assertNotEqual((one / cli).read_bytes(), (two / cli).read_bytes())
            self.assertEqual((default / relay).read_bytes(), (one / relay).read_bytes())
            self.assertEqual((one / relay).read_bytes(), (two / relay).read_bytes())
        stamped_recipe = EXPECTED_RECIPE.replace("-ldflags='-buildid='", "-ldflags='-buildid= -X main.version=v1.0.0'")
        for name in ("artifact-manifest.json", "hyphae.lock.json"):
            self.assertEqual(json.loads((one / name).read_text())["recipe"], stamped_recipe)
            self.assertEqual(json.loads((default / name).read_text())["recipe"], EXPECTED_RECIPE)
        self.assertNotIn("<release_tag>", stamped_recipe)
        builds = [r["args"] for r in self.log_rows() if r["args"][0] == "build" and r["args"][-1] == "./cmd/hyphae"]
        self.assertIn("-ldflags=-buildid= -X main.version=v1.0.0", [a[3] for a in builds])
        self.assertIn("-ldflags=-buildid=", [a[3] for a in builds])
        stamped_relay = [r["args"] for r in self.log_rows() if r["args"][-1] == "./cmd/hyphae-relay"]
        self.assertTrue(all(a[3] == "-ldflags=-buildid=" for a in stamped_relay))

    def test_unsafe_tags_cannot_reach_linker_flags(self) -> None:
        module = self.load_builder()
        for bad in ("v1 -X main.other=x", "v1 -s", "v1 x", "v1'x", 'v1"x', "v1\\x", "v1=x", "", "-v1", "v1\nx", "v1\tx"):
            with self.subTest(tag=bad):
                with self.assertRaises(module.BuildError):
                    module.version_ldflags(bad)
                with self.assertRaises(module.BuildError):
                    module.cli_recipe(bad)
        self.assertEqual(module.version_ldflags("v1.2.3-rc.1_x"), "-ldflags=-buildid= -X main.version=v1.2.3-rc.1_x")
        refused = self.run_builder(self.base / "bad-tag", tag="v1 -s", extra=("--stamp-version",))
        self.assertNotEqual(refused.returncode, 0)
        self.assertEqual(self.log_rows(), [])

    def test_rejects_source_sha_mismatch_before_go_and_output_creation(self) -> None:
        output = self.base / "no-output"
        result = self.run_builder(output, source_sha="0" * 40)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("source HEAD mismatch", result.stderr)
        self.assertFalse(output.exists())
        self.assertEqual(self.log_rows(), [])

    def test_rejects_tracked_untracked_and_ignored_build_inputs(self) -> None:
        cases = (
            ("cmd/hyphae/main.go", "package main // changed\n", "tracked"),
            ("cmd/hyphae/extra.go", "package main\n", "untracked"),
            ("cmd/hyphae/ignored.go", "package main\n", "ignored"),
            ("go.work", "go 1.26.4\n", "ignored"),
        )
        for index, (name, content, state) in enumerate(cases):
            with self.subTest(name=name):
                path = self.source / name
                original = path.read_bytes() if state == "tracked" else None
                if state == "ignored":
                    gitignore = self.source / ".gitignore"
                    prior = gitignore.read_text(encoding="utf-8") if gitignore.exists() else ""
                    gitignore.write_text(prior + f"/{name}\n", encoding="utf-8")
                    subprocess.run(["git", "-C", str(self.source), "add", ".gitignore"], check=True)
                    env = os.environ.copy()
                    env.update(
                        {
                            "GIT_AUTHOR_NAME": "Artifact Fixture",
                            "GIT_AUTHOR_EMAIL": "fixture@example.invalid",
                            "GIT_COMMITTER_NAME": "Artifact Fixture",
                            "GIT_COMMITTER_EMAIL": "fixture@example.invalid",
                        }
                    )
                    subprocess.run(["git", "-C", str(self.source), "commit", "-q", "-m", "ignore workspace"], check=True, env=env)
                    self.commit = subprocess.check_output(["git", "-C", str(self.source), "rev-parse", "HEAD"], text=True).strip()
                path.write_text(content, encoding="utf-8")
                output = self.base / f"rejected-{index}"
                result = self.run_builder(output)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("tracked or untracked changes", result.stderr)
                self.assertFalse(output.exists())
                if state == "tracked":
                    path.write_bytes(original or b"")
                else:
                    path.unlink()

    def test_rejects_output_inside_checkout(self) -> None:
        output = self.source / "candidate"
        result = self.run_builder(output)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must not overlap", result.stderr)
        self.assertFalse(output.exists())
        self.assertEqual(self.log_rows(), [])

    def test_existing_output_is_never_modified(self) -> None:
        output = self.base / "existing"
        output.mkdir()
        marker = output / "keep.txt"
        marker.write_text("user data", encoding="utf-8")
        result = self.run_builder(output)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(marker.read_text(encoding="utf-8"), "user data")
        self.assertEqual(self.log_rows(), [])

    def test_refuses_wrong_local_toolchain_without_switch_or_download(self) -> None:
        for index, version in enumerate(("go1.27.1", "go1.26.4-custom")):
            with self.subTest(version=version):
                self._write_fake_go(version=version)
                output = self.base / f"wrong-go-{index}"
                result = self.run_builder(output)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("refusing toolchain download/switch", result.stderr)
                self.assertFalse(output.exists())
                rows = self.log_rows()
                self.assertEqual(rows[0]["args"], ["version"])
                self.assertEqual(rows[0]["env"]["GOTOOLCHAIN"], "local")
                self.log.unlink()

    def test_build_failure_and_source_mutation_fail_without_publishing(self) -> None:
        self._write_fake_go(fail_target="./cmd/hyphae-relay")
        failed_output = self.base / "build-failed"
        failed = self.run_builder(failed_output)
        self.assertNotEqual(failed.returncode, 0)
        self.assertIn("controlled fake Go failure", failed.stderr)
        self.assertFalse(failed_output.exists())

        self.log.unlink()
        self._write_fake_go(mutate=True)
        mutated_output = self.base / "source-mutated"
        mutated = self.run_builder(mutated_output)
        self.assertNotEqual(mutated.returncode, 0)
        self.assertIn("tracked or untracked changes", mutated.stderr)
        self.assertFalse(mutated_output.exists())


def find_go() -> Path | None:
    """go1.26.4 from PATH first, then the module-cache toolchain for this OS/arch."""
    for candidate in (shutil.which("go"),):
        if candidate and subprocess.run([candidate, "version"], capture_output=True, text=True, env={**os.environ, "GOTOOLCHAIN": "local"}).stdout.split()[2:3] == ["go1.26.4"]:
            return Path(candidate).parent
    goos = {"darwin": "darwin", "linux": "linux"}.get(sys.platform, sys.platform)
    machine = {"x86_64": "amd64", "AMD64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(os.uname().machine, os.uname().machine)
    cached = Path.home() / f"go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.4.{goos}-{machine}/bin"
    return cached if (cached / "go").exists() else None


GO_BIN = find_go()


@unittest.skipUnless(GO_BIN, "go1.26.4 not on PATH and no cached toolchain for this OS/arch; stamping is only fake-tested")
class RealGoStampTests(unittest.TestCase):
    def test_real_build_stamps_version_only_when_asked(self) -> None:
        with tempfile.TemporaryDirectory(prefix="hyphae-realgo-") as raw:
            base = Path(raw)
            source = base / "src"
            for target in ("hyphae", "hyphae-relay"):
                (source / "cmd" / target).mkdir(parents=True)
            (source / "go.mod").write_text("module example.invalid/m\n\ngo 1.26\n")
            (source / "cmd/hyphae/main.go").write_text('package main\n\nimport "fmt"\n\nvar version = "dev"\n\nfunc main() { fmt.Print(version) }\n')
            (source / "cmd/hyphae-relay/main.go").write_text("package main\n\nfunc main() {}\n")
            (source / "LICENSE").write_text("l\n")
            (source / "NOTICE").write_text("n\n")
            env = {**os.environ, "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@e.invalid", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@e.invalid"}
            subprocess.run(["git", "init", "-q", str(source)], check=True)
            subprocess.run(["git", "-C", str(source), "add", "."], check=True)
            subprocess.run(["git", "-C", str(source), "commit", "-q", "-m", "x"], check=True, env=env)
            sha = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
            env["PATH"] = f"{GO_BIN}{os.pathsep}{env['PATH']}"

            def build(name: str, tag: str, *extra: str) -> Path:
                out = base / name
                subprocess.run([sys.executable, str(SCRIPT), "--source-dir", str(source), "--source-sha", sha,
                                "--output", str(out), "--release-tag", tag, *extra], check=True, env=env, capture_output=True)
                return out

            d1, d2, d3 = build("d1", "v1.2.3"), build("d2", "v1.2.3"), build("d3", "v1.2.4")
            self.assertEqual((d1 / "SHA256SUMS").read_bytes(), (d2 / "SHA256SUMS").read_bytes())
            self.assertEqual((d1 / "hyphae-linux-amd64").read_bytes(), (d3 / "hyphae-linux-amd64").read_bytes())  # default ignores the tag
            a1, a2, b = build("a1", "v1.2.3", "--stamp-version"), build("a2", "v1.2.3", "--stamp-version"), build("b", "v1.2.4", "--stamp-version")
            self.assertEqual((a1 / "SHA256SUMS").read_bytes(), (a2 / "SHA256SUMS").read_bytes())
            self.assertNotEqual((a1 / "hyphae-linux-amd64").read_bytes(), (d1 / "hyphae-linux-amd64").read_bytes())
            self.assertNotEqual((a1 / "hyphae-linux-amd64").read_bytes(), (b / "hyphae-linux-amd64").read_bytes())
            self.assertIn(b"v1.2.3", (a1 / "hyphae-linux-amd64").read_bytes())
            self.assertNotIn(b"v1.2.3", (d1 / "hyphae-linux-amd64").read_bytes())
            self.assertEqual((a1 / "hyphae-relay-linux-amd64").read_bytes(), (b / "hyphae-relay-linux-amd64").read_bytes())
            self.assertEqual((a1 / "hyphae-relay-linux-amd64").read_bytes(), (d1 / "hyphae-relay-linux-amd64").read_bytes())
            if sys.platform == "linux":  # run the stamped binary: it must report the tag
                run = subprocess.run([str(a1 / "hyphae-linux-amd64")], capture_output=True, text=True)
                self.assertEqual(run.stdout, "v1.2.3")


if __name__ == "__main__":
    unittest.main()
