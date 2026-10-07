"""Unit tests for CI provenance and candidate lock validation."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from scripts import joint_ci


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


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
                content = ("safe-" + name).encode()
                (artifact / name).write_bytes(content)
                hashes[name] = sha(content)
            derived = joint_ci.derive_lock(self.prod, platform="darwin-arm64",
                                           hyphae_sha="a" * 40, hyphae_binary_sha256=hashes["hyphae"])
            (artifact / "hyphae.lock.json").write_bytes(derived)
            manifest = joint_ci.make_manifest(
                mode="candidate", platform="darwin-arm64", hyphae_sha="a" * 40,
                agent24_sha=joint_ci.AGENT24_BASE_SHA,
                production_lock_sha256=sha(self.prod), derived_lock_sha256=sha(derived), hashes=hashes)
            manifest_path = root / "manifest.json"
            manifest_path.write_text(json.dumps(manifest))
            production_path = root / "production.lock.json"
            production_path.write_bytes(self.prod)
            self.assertEqual(joint_ci.validate_manifest(manifest_path, artifact, production_path)["validation_mode"], "candidate")
            (artifact / "hyphae").write_bytes(b"tampered")
            with self.assertRaisesRegex(joint_ci.GateError, "artifact-hash-mismatch"):
                joint_ci.validate_manifest(manifest_path, artifact, production_path)

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
        workflow = (Path(__file__).parents[1] / ".github/workflows/joint-ci.yml").read_text()
        self.assertNotIn("pull_request_target", workflow)
        self.assertNotIn("secrets.", workflow)
        self.assertIn("permissions:\n  contents: read", workflow)
        self.assertGreaterEqual(workflow.count("persist-credentials: false"), 4)


if __name__ == "__main__":
    unittest.main()
