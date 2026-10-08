"""Unit tests for CI provenance and candidate lock validation."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

from scripts import joint_ci


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def binary_fixture(platform: str, suffix: bytes = b"fixture") -> bytes:
    header = bytearray(64)
    if platform == "darwin-arm64":
        header[:4] = b"\xcf\xfa\xed\xfe"
        header[4:8] = (0x0100000C).to_bytes(4, "little")
    elif platform == "linux-x64":
        header[:4] = b"\x7fELF"
        header[4:6] = bytes((2, 1))
        header[18:20] = (62).to_bytes(2, "little")
    else:
        raise AssertionError(f"unexpected test platform: {platform}")
    return bytes(header) + suffix


def production_lock() -> bytes:
    return json.dumps({
        "schema": 1,
        "source_sha": "671c584f9e9eb807a15968e2aa42fd7507e178b8",
        "go": "go1.26.4",
        "recipe": joint_ci.GO_RECIPE,
        "binaries": {
            "darwin-arm64": "1" * 64,
            "linux-x64": "2" * 64,
            "darwin-x64": None,
            "linux-arm64": None,
        },
    }, sort_keys=True, indent=2).encode() + b"\n"


class JointCIGateTests(unittest.TestCase):
    def setUp(self) -> None:
        self.old_sha = joint_ci.PRODUCTION_LOCK_SHA256
        self.prod = production_lock()
        joint_ci.PRODUCTION_LOCK_SHA256 = sha(self.prod)

    def tearDown(self) -> None:
        joint_ci.PRODUCTION_LOCK_SHA256 = self.old_sha

    def test_derive_changes_only_source_and_selected_platform_binary(self):
        derived = joint_ci.derive_lock(self.prod, platform="darwin-arm64",
                                       hyphae_sha="a" * 40, hyphae_binary_sha256="b" * 64)
        original = json.loads(self.prod)
        changed = json.loads(derived)
        self.assertEqual(changed["source_sha"], "a" * 40)
        self.assertEqual(changed["binaries"]["darwin-arm64"], "b" * 64)
        changed["source_sha"] = original["source_sha"]
        changed["binaries"]["darwin-arm64"] = original["binaries"]["darwin-arm64"]
        self.assertEqual(changed, original)

    def test_rejects_production_lock_drift(self):
        with self.assertRaisesRegex(joint_ci.GateError, "production-lock-hash"):
            joint_ci.derive_lock(self.prod + b" ", platform="linux-x64",
                                 hyphae_sha="a" * 40, hyphae_binary_sha256="b" * 64)

    def test_producer_output_binding_rejects_bad_source_platform_manifest_and_lock(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "manifest.json"
            manifest = {"platform": "linux-x64", "hyphae_source_sha": "a" * 40}
            path.write_text(json.dumps(manifest))
            digest = sha(path.read_bytes())
            with self.assertRaisesRegex(joint_ci.GateError, "manifest-source"):
                joint_ci.check_producer_outputs(path, manifest, expected_manifest_sha256=digest,
                                                expected_platform="linux-x64", expected_hyphae_sha="b" * 40)
            with self.assertRaisesRegex(joint_ci.GateError, "manifest-platform"):
                joint_ci.check_producer_outputs(path, manifest, expected_manifest_sha256=digest,
                                                expected_platform="darwin-arm64", expected_hyphae_sha="a" * 40)
            with self.assertRaisesRegex(joint_ci.GateError, "producer-platform-invalid"):
                joint_ci.check_producer_outputs(path, manifest, expected_manifest_sha256=digest,
                                                expected_platform="invalid", expected_hyphae_sha="a" * 40)
            with self.assertRaisesRegex(joint_ci.GateError, "manifest-producer-hash"):
                joint_ci.check_producer_outputs(path, manifest, expected_manifest_sha256="0" * 64,
                                                expected_platform="linux-x64", expected_hyphae_sha="a" * 40)
            derived = joint_ci.derive_lock(self.prod, platform="linux-x64", hyphae_sha="a" * 40,
                                           hyphae_binary_sha256="b" * 64)
            with self.assertRaisesRegex(joint_ci.GateError, "production-lock-provenance"):
                joint_ci.validate_derived_lock(self.prod + b"\n", derived, platform="linux-x64",
                                               hyphae_sha="a" * 40, hyphae_binary_sha256="b" * 64,
                                               declared_production_sha256=sha(self.prod),
                                               declared_derived_sha256=sha(derived))

    def test_wrong_platform_producer_output_fails_closed_for_both_targets(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "manifest.json"
            for actual_platform, expected_platform in (
                ("linux-x64", "darwin-arm64"),
                ("darwin-arm64", "linux-x64"),
            ):
                manifest = {"platform": actual_platform, "hyphae_source_sha": "a" * 40}
                path.write_text(json.dumps(manifest))
                with self.subTest(actual=actual_platform, expected=expected_platform):
                    with self.assertRaisesRegex(joint_ci.GateError, "manifest-platform"):
                        joint_ci.check_producer_outputs(
                            path, manifest, expected_manifest_sha256=sha(path.read_bytes()),
                            expected_platform=expected_platform, expected_hyphae_sha="a" * 40)

    def test_binary_platform_gate_accepts_native_and_rejects_mixed_artifacts(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            linux = root / "linux-binary"
            linux.write_bytes(binary_fixture("linux-x64"))
            darwin = root / "darwin-binary"
            darwin.write_bytes(binary_fixture("darwin-arm64"))
            joint_ci.verify_binary_platform(linux, "linux-x64")
            joint_ci.verify_binary_platform(darwin, "darwin-arm64")
            with self.assertRaisesRegex(joint_ci.GateError, "artifact-platform-mismatch"):
                joint_ci.verify_binary_platform(linux, "darwin-arm64")
            with self.assertRaisesRegex(joint_ci.GateError, "artifact-platform-mismatch"):
                joint_ci.verify_binary_platform(darwin, "linux-x64")

    def test_producer_output_binary_or_lock_hash_mismatch_rejected(self):
        manifest = {"validation_mode": "candidate", "platform": "linux-x64",
                    "hyphae_source_sha": "a" * 40, "production_lock_sha256": "c" * 64,
                    "derived_lock_sha256": "d" * 64,
                    "binary_sha256": {name: "e" * 64 for name in joint_ci.ARTIFACTS}}
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "manifest.json"
            path.write_text(json.dumps(manifest))
            expected = {
                "validation_mode": "candidate", "platform": "linux-x64",
                "hyphae_source_sha": "a" * 40, "production_lock_sha256": "c" * 64,
                "derived_lock_sha256": "d" * 64, "binary_sha256": dict(manifest["binary_sha256"]),
                "manifest_sha256": sha(path.read_bytes()),
            }
            expected["binary_sha256"]["agent24"] = "f" * 64
            with self.assertRaisesRegex(joint_ci.GateError, "producer-output-mismatch"):
                joint_ci.check_producer_outputs(path, manifest, expected_manifest_sha256=sha(path.read_bytes()),
                                                expected_platform="linux-x64", expected_hyphae_sha="a" * 40,
                                                expected_outputs=expected)

    def test_manifest_and_artifacts_rehash_and_verify(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            artifact = root / "artifact"
            artifact.mkdir()
            hashes = {}
            for name in joint_ci.ARTIFACTS:
                content = binary_fixture("darwin-arm64", ("safe-" + name).encode())
                (artifact / name).write_bytes(content)
                hashes[name] = sha(content)
            derived = joint_ci.derive_lock(self.prod, platform="darwin-arm64",
                                           hyphae_sha="a" * 40, hyphae_binary_sha256=hashes["hyphae"])
            (artifact / "hyphae.lock.json").write_bytes(derived)
            manifest = joint_ci.make_manifest(
                mode="candidate", platform="darwin-arm64", hyphae_sha="a" * 40,
                agent24_sha=joint_ci.AGENT24_BASE_SHA,
                production_lock_sha256=sha(self.prod), derived_lock_sha256=sha(derived), hashes=hashes)
            manifest_path = artifact / "manifest.json"
            manifest_path.write_text(json.dumps(manifest))
            production_path = root / "production.lock.json"
            production_path.write_bytes(self.prod)
            outputs = {
                "validation_mode": manifest["validation_mode"], "platform": manifest["platform"],
                "hyphae_source_sha": manifest["hyphae_source_sha"],
                "production_lock_sha256": manifest["production_lock_sha256"],
                "derived_lock_sha256": manifest["derived_lock_sha256"],
                "binary_sha256": manifest["binary_sha256"],
                "manifest_sha256": sha(manifest_path.read_bytes()),
            }
            verified = joint_ci.verify_consumer_bundle(
                manifest_path, artifact, production_path,
                expected_manifest_sha256=outputs["manifest_sha256"], expected_platform="darwin-arm64",
                expected_hyphae_sha="a" * 40, expected_outputs=outputs)
            self.assertEqual(verified["validation_mode"], "candidate")
            self.assertTrue(all((artifact / name).stat().st_mode & 0o111 for name in joint_ci.ARTIFACTS))
            self.assertEqual({name: joint_ci.file_sha(artifact / name) for name in joint_ci.ARTIFACTS}, hashes)
            (artifact / "hyphae").write_bytes(b"tampered")
            with self.assertRaisesRegex(joint_ci.GateError, "artifact-hash-mismatch"):
                joint_ci.validate_manifest(manifest_path, artifact, production_path)

    def test_restore_modes_rejects_artifact_symlink_without_chmod(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            artifact = root / "artifact"
            artifact.mkdir()
            for name in joint_ci.ARTIFACTS:
                (artifact / name).write_bytes(name.encode())
            manifest = {"binary_sha256": {name: sha(name.encode()) for name in joint_ci.ARTIFACTS}}
            target = artifact / "hyphae"
            target.unlink()
            target.symlink_to(root / "outside")
            (root / "outside").write_bytes(b"outside")
            with mock.patch.object(joint_ci.os, "fchmod") as chmod:
                with self.assertRaisesRegex(joint_ci.GateError, "artifact-not-regular"):
                    joint_ci.restore_executable_modes(artifact, manifest)
                chmod.assert_not_called()

    def test_manifest_gate_rejects_artifact_or_parent_directory_symlink(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            artifact = root / "artifact"
            artifact.mkdir()
            artifact_link = root / "artifact-link"
            artifact_link.symlink_to(artifact, target_is_directory=True)
            with self.assertRaisesRegex(joint_ci.GateError, "artifact-directory-not-regular"):
                joint_ci.validate_manifest(artifact_link / "manifest.json", artifact_link,
                                           root / "production.lock.json")
            parent_link = root / "parent-link"
            parent_link.symlink_to(root, target_is_directory=True)
            linked_child = parent_link / "artifact"
            with self.assertRaisesRegex(joint_ci.GateError, "artifact-directory-not-regular"):
                joint_ci.validate_manifest(linked_child / "manifest.json", linked_child,
                                           root / "production.lock.json")

    def test_failed_manifest_or_hash_gate_never_changes_modes(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            artifact = root / "artifact"
            artifact.mkdir()
            hashes = {}
            for name in joint_ci.ARTIFACTS:
                content = ("binary-" + name).encode()
                path = artifact / name
                path.write_bytes(content)
                path.chmod(0o644)
                hashes[name] = sha(content)
            derived = joint_ci.derive_lock(self.prod, platform="linux-x64", hyphae_sha="a" * 40,
                                           hyphae_binary_sha256=hashes["hyphae"])
            (artifact / "hyphae.lock.json").write_bytes(derived)
            manifest = joint_ci.make_manifest(mode="candidate", platform="linux-x64", hyphae_sha="a" * 40,
                                              agent24_sha=joint_ci.AGENT24_BASE_SHA,
                                              production_lock_sha256=sha(self.prod),
                                              derived_lock_sha256=sha(derived), hashes=hashes)
            manifest_path = artifact / "manifest.json"
            manifest_path.write_text(json.dumps(manifest))
            prod_path = root / "production.lock.json"
            prod_path.write_bytes(self.prod)
            outputs = {
                "validation_mode": manifest["validation_mode"], "platform": manifest["platform"],
                "hyphae_source_sha": manifest["hyphae_source_sha"],
                "production_lock_sha256": manifest["production_lock_sha256"],
                "derived_lock_sha256": manifest["derived_lock_sha256"],
                "binary_sha256": manifest["binary_sha256"],
                "manifest_sha256": sha(manifest_path.read_bytes()),
            }
            def assert_no_exec_modes():
                self.assertTrue(all((artifact / name).stat().st_mode & 0o111 == 0
                                    for name in joint_ci.ARTIFACTS))
            with mock.patch.object(joint_ci.os, "fchmod") as chmod:
                with self.assertRaisesRegex(joint_ci.GateError, "manifest-producer-hash"):
                    joint_ci.verify_consumer_bundle(
                        manifest_path, artifact, prod_path, expected_manifest_sha256="0" * 64,
                        expected_platform="linux-x64", expected_hyphae_sha="a" * 40,
                        expected_outputs=outputs)
                chmod.assert_not_called()
                assert_no_exec_modes()
                wrong_outputs = dict(outputs)
                wrong_outputs["derived_lock_sha256"] = "0" * 64
                with self.assertRaisesRegex(joint_ci.GateError, "producer-output-mismatch"):
                    joint_ci.verify_consumer_bundle(
                        manifest_path, artifact, prod_path, expected_manifest_sha256=outputs["manifest_sha256"],
                        expected_platform="linux-x64", expected_hyphae_sha="a" * 40,
                        expected_outputs=wrong_outputs)
                chmod.assert_not_called()
                assert_no_exec_modes()
                # agent24d is last in ARTIFACTS; reject it before changing
                # permissions on any earlier binary.
                (artifact / "agent24d").write_bytes(b"altered")
                with self.assertRaisesRegex(joint_ci.GateError, "artifact-hash-mismatch"):
                    joint_ci.verify_consumer_bundle(
                        manifest_path, artifact, prod_path, expected_manifest_sha256=outputs["manifest_sha256"],
                        expected_platform="linux-x64", expected_hyphae_sha="a" * 40,
                        expected_outputs=outputs)
                chmod.assert_not_called()
                assert_no_exec_modes()

    def test_pinned_production_requires_exact_lock_bytes_cli_hash_and_complete_set(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            artifact = root / "artifact"
            artifact.mkdir()
            hashes = {name: sha(binary_fixture("linux-x64", name.encode()))
                      for name in joint_ci.ARTIFACTS}
            lock_value = json.loads(self.prod)
            lock_value["binaries"]["linux-x64"] = hashes["hyphae"]
            pinned_lock = json.dumps(lock_value, sort_keys=True, indent=2).encode() + b"\n"
            joint_ci.PRODUCTION_LOCK_SHA256 = sha(pinned_lock)
            self.prod = pinned_lock
            for name in joint_ci.ARTIFACTS:
                (artifact / name).write_bytes(binary_fixture("linux-x64", name.encode()))
            (artifact / "hyphae.lock.json").write_bytes(pinned_lock)
            manifest = joint_ci.make_manifest(
                mode="pinned-production", platform="linux-x64", hyphae_sha=lock_value["source_sha"],
                agent24_sha=joint_ci.AGENT24_BASE_SHA, production_lock_sha256=sha(pinned_lock),
                derived_lock_sha256=sha(pinned_lock), hashes=hashes)
            manifest_path = artifact / "manifest.json"
            manifest_path.write_text(json.dumps(manifest))
            production_path = root / "production.lock.json"
            production_path.write_bytes(pinned_lock)

            self.assertEqual(joint_ci.validate_manifest(manifest_path, artifact, production_path), manifest)

            (artifact / "hyphae.lock.json").write_bytes(pinned_lock + b" ")
            with self.assertRaisesRegex(joint_ci.GateError, "production-lock-not-preserved"):
                joint_ci.validate_manifest(manifest_path, artifact, production_path)
            (artifact / "hyphae.lock.json").write_bytes(pinned_lock)

            (artifact / "agent24").write_bytes(binary_fixture("linux-x64", b"tampered-cli"))
            with self.assertRaisesRegex(joint_ci.GateError, "artifact-hash-mismatch"):
                joint_ci.validate_manifest(manifest_path, artifact, production_path)
            (artifact / "agent24").write_bytes(binary_fixture("linux-x64", b"agent24"))

            (artifact / "relay").unlink()
            with self.assertRaisesRegex(joint_ci.GateError, "artifact-file-set"):
                joint_ci.validate_manifest(manifest_path, artifact, production_path)

    def test_post_mode_restore_hash_change_fails_consumer_before_runner(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            artifact = root / "artifact"
            artifact.mkdir()
            hashes = {}
            for name in joint_ci.ARTIFACTS:
                content = binary_fixture("linux-x64", name.encode())
                (artifact / name).write_bytes(content)
                hashes[name] = sha(content)
            derived = joint_ci.derive_lock(self.prod, platform="linux-x64", hyphae_sha="a" * 40,
                                           hyphae_binary_sha256=hashes["hyphae"])
            (artifact / "hyphae.lock.json").write_bytes(derived)
            manifest = joint_ci.make_manifest(
                mode="candidate", platform="linux-x64", hyphae_sha="a" * 40,
                agent24_sha=joint_ci.AGENT24_BASE_SHA, production_lock_sha256=sha(self.prod),
                derived_lock_sha256=sha(derived), hashes=hashes)
            manifest_path = artifact / "manifest.json"
            manifest_path.write_text(json.dumps(manifest))
            production_path = root / "production.lock.json"
            production_path.write_bytes(self.prod)
            outputs = {
                "validation_mode": manifest["validation_mode"], "platform": manifest["platform"],
                "hyphae_source_sha": manifest["hyphae_source_sha"],
                "production_lock_sha256": manifest["production_lock_sha256"],
                "derived_lock_sha256": manifest["derived_lock_sha256"],
                "binary_sha256": manifest["binary_sha256"],
                "manifest_sha256": sha(manifest_path.read_bytes()),
            }
            real_fchmod = joint_ci.os.fchmod
            calls = 0

            def chmod_then_tamper(descriptor, mode):
                nonlocal calls
                real_fchmod(descriptor, mode)
                calls += 1
                if calls == len(joint_ci.ARTIFACTS):
                    with (artifact / "agent24d").open("r+b") as stream:
                        stream.write(b"corrupt")

            with mock.patch.object(joint_ci.os, "fchmod", side_effect=chmod_then_tamper):
                with self.assertRaisesRegex(joint_ci.GateError, "artifact-changed-during-mode-restore"):
                    joint_ci.verify_consumer_bundle(
                        manifest_path, artifact, production_path,
                        expected_manifest_sha256=outputs["manifest_sha256"],
                        expected_platform="linux-x64", expected_hyphae_sha="a" * 40,
                        expected_outputs=outputs)
            self.assertEqual(calls, len(joint_ci.ARTIFACTS))
            self.assertTrue(all((artifact / name).stat().st_mode & 0o111 == 0o111
                                for name in joint_ci.ARTIFACTS))

    def test_rejects_unexpected_lock_field_change(self):
        derived = joint_ci.derive_lock(self.prod, platform="linux-x64",
                                       hyphae_sha="a" * 40, hyphae_binary_sha256="b" * 64)
        value = json.loads(derived)
        value["go"] = "go1.99.0"
        tampered = json.dumps(value, sort_keys=True, indent=2).encode() + b"\n"
        with self.assertRaisesRegex(joint_ci.GateError, "derived-lock-diff"):
            joint_ci.validate_derived_lock(self.prod, tampered, platform="linux-x64",
                                           hyphae_sha="a" * 40, hyphae_binary_sha256="b" * 64,
                                           declared_production_sha256=sha(self.prod),
                                           declared_derived_sha256=sha(tampered))

    def test_normalized_evidence_excludes_runner_secrets_and_raw_details(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "raw.json"
            path.write_text(json.dumps({
                "result": "PASS", "failure": None,
                "assertions": [{"stage": "isolated-identity-and-relay-setup", "secret": "never-export"}],
                "executions": [{"command": ["agent24", "--password", "never-export"]}],
                "primary_failure": "child-timeout",
                "cleanup_failure": {"stage": "terminate-group", "category": "permission-denied", "errno": 13,
                                    "message": "never-export"},
                "executions": [{"stage": "exec-child", "failure": "child-timeout",
                                "primary_failure": "child-timeout",
                                "cleanup_failure": {"stage": "term-signal", "category": "EPERM", "errno": 1,
                                                    "message": "never-export"},
                                "command": ["with-secret"], "stderr": "never-export"}],
            }))
            manifest = {"validation_mode": "candidate", "platform": "darwin-arm64",
                        "hyphae_source_sha": "a" * 40, "agent24_built_sha": "b" * 40,
                        "production_lock_sha256": "c" * 64, "derived_lock_sha256": "d" * 64,
                        "binary_sha256": {name: "e" * 64 for name in joint_ci.ARTIFACTS}}
            safe = joint_ci.normalized_evidence(path, manifest)
            self.assertNotIn("never-export", json.dumps(safe))
            self.assertEqual(safe["stages"], [{"stage": "isolated-identity-and-relay-setup", "result": "PASS"}])
            self.assertEqual(safe["primary_failure"], "child-timeout")
            self.assertEqual(safe["cleanup_failure"], {"stage": "terminate-group", "category": "permission-denied", "errno": 13})
            self.assertEqual(safe["executions"], [{"stage": "exec-child", "failure": "child-timeout",
                                                   "primary_failure": "child-timeout",
                                                   "cleanup_failure": {"stage": "term-signal", "category": "EPERM", "errno": 1}}])

    def test_workflow_has_no_fork_secrets_or_persisted_git_credentials(self):
        workflows = Path(__file__).parents[1] / ".github/workflows"
        workflow = (workflows / "joint-ci.yml").read_text()
        producer = (workflows / "joint-ci-producer.yml").read_text()
        self.assertNotIn("pull_request_target", workflow)
        self.assertNotIn("secrets.", workflow + producer)
        self.assertIn("permissions:\n  contents: read", workflow)
        self.assertGreaterEqual((workflow + producer).count("persist-credentials: false"), 5)

    def test_each_platform_has_independent_producer_outputs_and_scoped_evidence(self):
        workflows = Path(__file__).parents[1] / ".github" / "workflows"
        workflow = (workflows / "joint-ci.yml").read_text()
        producer = (workflows / "joint-ci-producer.yml").read_text()
        self.assertIn("produce_linux:", workflow)
        self.assertIn("produce_darwin:", workflow)
        self.assertIn("needs.produce_linux.outputs.candidate_outputs", workflow)
        self.assertIn("needs.produce_linux.outputs.production_outputs", workflow)
        self.assertIn("needs.produce_darwin.outputs.candidate_outputs", workflow)
        self.assertIn("needs.produce_darwin.outputs.production_outputs", workflow)
        self.assertIn("joint-bundles-${{ inputs.platform }}-${{ github.run_id }}", producer)
        self.assertIn("joint-bundles-${{ matrix.platform }}-${{ github.run_id }}", workflow)
        self.assertIn("joint-evidence-${{ matrix.mode }}-${{ matrix.platform }}-${{ github.run_id }}", workflow)
        self.assertIn("linux-x64:Linux:x86_64", producer)
        self.assertIn("darwin-arm64:Darwin:arm64", producer)
        self.assertIn("--expected-platform \"$TARGET_PLATFORM\"", workflow)


if __name__ == "__main__":
    unittest.main()
