"""Schema 2 lock, skipped stages and the unsigned-candidate positive control."""
from __future__ import annotations

import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from scripts import joint_ci

FIXTURE = Path(__file__).with_name("fixtures") / "hyphae.lock.v0.30.0.json"
FIXTURE_SHA = "55c4d493dacf60ecbfc4f42571aa5f140ab80c30dad4013acf6340eab8e2e1cf"
CANDIDATE = {"hyphae_sha": "a" * 40, "hyphae_binary_sha256": "b" * 64, "version": "v0.31.0-ci"}


def dump(value: object) -> bytes:
    return (json.dumps(value, sort_keys=True, indent=2) + "\n").encode()


class Schema2Base(unittest.TestCase):
    def setUp(self) -> None:
        self.saved = (joint_ci.PRODUCTION_LOCK_SHA256, joint_ci.PRODUCTION_HYPHAE_SHA)
        self.prod = FIXTURE.read_bytes()
        self.lock = json.loads(self.prod)
        joint_ci.PRODUCTION_LOCK_SHA256 = hashlib.sha256(self.prod).hexdigest()
        joint_ci.PRODUCTION_HYPHAE_SHA = self.lock["source"]["commit"]

    def tearDown(self) -> None:
        joint_ci.PRODUCTION_LOCK_SHA256, joint_ci.PRODUCTION_HYPHAE_SHA = self.saved

    def rebase(self, lock: dict) -> bytes:
        raw = dump(lock)
        joint_ci.PRODUCTION_LOCK_SHA256 = hashlib.sha256(raw).hexdigest()
        return raw


class Schema2LockTests(Schema2Base):
    def test_fixture_is_the_real_v0_30_0_release_asset(self):
        self.assertEqual(hashlib.sha256(self.prod).hexdigest(), FIXTURE_SHA)

    def test_derive_replaces_only_the_four_candidate_fields(self):
        for platform in ("darwin-arm64", "linux-x64"):
            derived = json.loads(joint_ci.derive_lock(self.prod, platform=platform, **CANDIDATE))
            expected = copy.deepcopy(self.lock)
            expected["source"]["commit"] = "a" * 40
            expected["version"] = "v0.31.0-ci"
            expected["assets"][platform]["sha256"] = "b" * 64
            expected["reproducible"]["unsigned_sha256"][platform] = "b" * 64
            self.assertEqual(derived, expected)
            self.assertEqual(derived["signing"], self.lock["signing"])

    def test_derived_candidate_must_match_byte_for_byte(self):
        derived = joint_ci.derive_lock(self.prod, platform="darwin-arm64", **CANDIDATE)
        def check(raw: bytes) -> None:
            joint_ci.validate_derived_lock(
                self.prod, raw, platform="darwin-arm64", hyphae_sha="a" * 40, hyphae_binary_sha256="b" * 64,
                declared_production_sha256=joint_ci.PRODUCTION_LOCK_SHA256,
                declared_derived_sha256=hashlib.sha256(raw).hexdigest(), version="v0.31.0-ci")
        check(derived)
        def mutate(fn) -> bytes:
            value = json.loads(derived)
            fn(value)
            return dump(value)
        cases = {
            "signing changed": lambda v: v["signing"].update(notarized=False),
            "team changed": lambda v: v["signing"].update(macos_team_id="XXXXXXXXXX"),
            "commit not replaced": lambda v: v["source"].update(commit=self.lock["source"]["commit"]),
            "version not replaced": lambda v: v.update(version="v0.30.0"),
            "asset hash not replaced": lambda v: v["assets"]["darwin-arm64"].update(
                sha256=self.lock["assets"]["darwin-arm64"]["sha256"]),
            "unsigned hash not replaced": lambda v: v["reproducible"]["unsigned_sha256"].update(
                {"darwin-arm64": self.lock["reproducible"]["unsigned_sha256"]["darwin-arm64"]}),
            "other platform touched": lambda v: v["assets"]["linux-x64"].update(sha256="c" * 64),
            "extra key": lambda v: v.update(extra=1),
        }
        for name, fn in cases.items():
            with self.subTest(name), self.assertRaisesRegex(joint_ci.GateError, "derived-lock-diff"):
                check(mutate(fn))

    def test_candidate_version_is_required_and_must_change(self):
        for bad in (None, "v0.30.0", "", "bad version!"):
            with self.subTest(bad), self.assertRaisesRegex(joint_ci.GateError, "candidate-version"):
                joint_ci.derive_lock(self.prod, platform="linux-x64", hyphae_sha="a" * 40,
                                     hyphae_binary_sha256="b" * 64, version=bad)

    def test_production_lock_strictness(self):
        def broken(fn) -> bytes:
            value = copy.deepcopy(self.lock)
            fn(value)
            return self.rebase(value)
        cases = {
            "production-lock-signing": [lambda v: v["signing"].update(macos_team_id="OTHER"),
                                        lambda v: v["signing"].update(notarized=False),
                                        lambda v: v["signing"].update(notarized="true")],
            "production-lock-keys": [lambda v: v.update(extra=1), lambda v: v.pop("assurance")],
            "production-lock-platform-set": [lambda v: v["assets"].update({"darwin-x64": v["assets"]["linux-x64"]}),
                                             lambda v: v["reproducible"]["unsigned_sha256"].update({"linux-arm64": None}),
                                             lambda v: v["assets"].pop("linux-x64")],
            "production-lock-asset-hash": [lambda v: v["assets"]["linux-x64"].update(sha256=None),
                                           lambda v: v["assets"]["linux-x64"].update(sha256="ABC")],
            "production-lock-asset": [lambda v: v["assets"]["linux-x64"].update(extra=1),
                                      lambda v: v["assets"]["linux-x64"].update(files=None),
                                      lambda v: v["assets"]["linux-x64"].update(name=1)],
            "production-lock-source": [lambda v: v["source"].update(extra=1),
                                       lambda v: v["source"].update(repo=123)],
            "production-lock-baseline": [lambda v: v["source"].update(commit="d" * 40)],
            "production-lock-linux-unsigned-mismatch": [
                lambda v: v["reproducible"]["unsigned_sha256"].update({"linux-x64": "e" * 64})],
            "production-lock-reproducible": [lambda v: v["reproducible"].update(toolchain="go1.0")],
            "production-lock-schema-unsupported": [lambda v: v.update(schema=3), lambda v: v.update(schema=True),
                                                   lambda v: v.update(schema="2"), lambda v: v.pop("schema")],
        }
        for label, mutations in cases.items():
            for index, fn in enumerate(mutations):
                with self.subTest(label, index=index), self.assertRaisesRegex(joint_ci.GateError, label):
                    joint_ci.derive_lock(broken(fn), platform="linux-x64", **CANDIDATE)

    def test_baseline_hash_still_binds_schema2_bytes(self):
        joint_ci.PRODUCTION_LOCK_SHA256 = "0" * 64
        with self.assertRaisesRegex(joint_ci.GateError, "production-lock-hash"):
            joint_ci.derive_lock(self.prod, platform="linux-x64", **CANDIDATE)

    def test_schema2_rejects_schema1_only_platforms(self):
        with self.assertRaisesRegex(joint_ci.GateError, "unsupported-platform"):
            joint_ci.derive_lock(self.prod, platform="darwin-x64", **CANDIDATE)

    def test_pinned_production_binds_manifest_to_assets(self):
        pinned = self.lock["assets"]["linux-x64"]["sha256"]
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            art = root / "a"
            art.mkdir()
            for name in joint_ci.ARTIFACTS:
                (art / name).write_bytes(joint_ci.__dict__["sha256"](name.encode()).encode())
            (art / "hyphae.lock.json").write_bytes(self.prod)
            def manifest(source: str, digest: str) -> dict:
                hashes = {n: joint_ci.file_sha(art / n) for n in joint_ci.ARTIFACTS}
                hashes["hyphae"] = digest
                return {"schema": "agent24-hyphae-joint-ci-manifest/1", "validation_mode": "pinned-production",
                        "platform": "linux-x64", "hyphae_source_sha": source,
                        "production_lock_sha256": FIXTURE_SHA, "derived_lock_sha256": FIXTURE_SHA,
                        "binary_sha256": hashes, "agent24_base_sha": joint_ci.AGENT24_BASE_SHA,
                        "agent24_built_sha": joint_ci.AGENT24_BASE_SHA,
                        "go_version": joint_ci.GO_VERSION, "go_recipe": joint_ci.GO_RECIPE}
            prod_path = root / "prod.json"
            prod_path.write_bytes(self.prod)
            (art / "hyphae").write_bytes(b"x")
            digest = joint_ci.file_sha(art / "hyphae")
            # digest of the test binary differs from the lock's pinned asset hash -> mismatch is fail-closed
            (art / "manifest.json").write_text(json.dumps(manifest(self.lock["source"]["commit"], digest)))
            with self.assertRaisesRegex(joint_ci.GateError, "production-binary-mismatch"):
                joint_ci.validate_manifest(art / "manifest.json", art, prod_path)
            self.assertNotEqual(digest, pinned)
            # Positive path: a lock whose linux asset equals the binary digest is accepted.
            matching = copy.deepcopy(self.lock)
            matching["assets"]["linux-x64"]["sha256"] = digest
            matching["reproducible"]["unsigned_sha256"]["linux-x64"] = digest
            raw = self.rebase(matching)
            prod_path.write_bytes(raw)
            (art / "hyphae.lock.json").write_bytes(raw)
            ok = manifest(self.lock["source"]["commit"], digest)
            ok["production_lock_sha256"] = ok["derived_lock_sha256"] = joint_ci.PRODUCTION_LOCK_SHA256
            (art / "manifest.json").write_text(json.dumps(ok))
            self.assertEqual(joint_ci.validate_manifest(art / "manifest.json", art, prod_path)["platform"], "linux-x64")
            prod_path.write_bytes(self.prod)
            joint_ci.PRODUCTION_LOCK_SHA256 = FIXTURE_SHA
            (art / "hyphae.lock.json").write_bytes(self.prod)
            (art / "manifest.json").write_text(json.dumps(manifest("f" * 40, digest)))
            with self.assertRaisesRegex(joint_ci.GateError, "production-source-mismatch"):
                joint_ci.validate_manifest(art / "manifest.json", art, prod_path)


class EvidenceTests(Schema2Base):
    def manifest(self, mode="candidate", platform="darwin-arm64") -> dict:
        return {"validation_mode": mode, "platform": platform, "hyphae_source_sha": "a" * 40,
                "agent24_built_sha": "b" * 40, "production_lock_sha256": "c" * 64,
                "derived_lock_sha256": "d" * 64, "binary_sha256": {n: "e" * 64 for n in joint_ci.ARTIFACTS}}

    def normalize(self, rows: list, *, schema=2, **manifest_args) -> dict:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "raw.json"
            path.write_text(json.dumps({"result": "PASS", "assertions": rows}))
            return joint_ci.normalized_evidence(path, self.manifest(**manifest_args), lock_schema_id=schema)

    def control(self, **override) -> dict:
        row = {"stage": joint_ci.CONTROL_STAGE, "kind": "control", "expected": "rejected",
               "observed": "rejected", "reason_code": "signature_rejected"}
        return {**row, **override}

    def skipped(self, name: str, **override) -> dict:
        return {"stage": name, "result": "SKIPPED", "reason": joint_ci.SKIP_REASON, **override}

    def good_rows(self) -> list:
        return ([{"stage": "download-and-hash"}, {"stage": joint_ci.PRECHECK_STAGE}]
                + [self.skipped(name) for name in sorted(joint_ci.STAGE_NAMES)] + [self.control()])

    def test_darwin_candidate_reports_skipped_separately_and_is_not_clean(self):
        safe = self.normalize(self.good_rows())
        self.assertEqual(safe["summary"], {"passed": 3, "skipped": 10, "blocked": 0})
        self.assertEqual(safe["result"], "PASS_WITH_SKIPPED")
        self.assertEqual(safe["stages"][2]["reason"], "unsigned candidate; signing verified in pinned-production")

    def test_skipped_without_reason_or_with_other_reason_is_rejected(self):
        for reason in (None, "", "   ", "because"):
            row = self.skipped("start-daemon", reason=reason)
            if reason is None:
                row.pop("reason")
            expected = "skipped-reason-missing" if not reason or not str(reason).strip() else "skipped-reason-invalid"
            with self.subTest(reason), self.assertRaisesRegex(joint_ci.GateError, expected):
                self.normalize([row, self.control()])

    def test_skipped_outside_unsigned_darwin_candidate_is_rejected(self):
        rows = [self.skipped("start-daemon")]
        for kwargs in ({"mode": "pinned-production"}, {"platform": "linux-x64"}):
            with self.subTest(kwargs), self.assertRaisesRegex(joint_ci.GateError, "skipped-reason-invalid"):
                self.normalize(rows, **kwargs)
        with self.assertRaisesRegex(joint_ci.GateError, "skipped-reason-invalid"):
            self.normalize(rows, schema=1)

    def test_control_passes_only_with_observed_rejection(self):
        for override in ({"observed": "accepted"}, {"observed": "unknown"}, {"reason_code": "other"},
                         {"expected": "accepted"}, {"result": "FAIL"}, {"observed": None}):
            with self.subTest(override), self.assertRaises(joint_ci.GateError):
                self.normalize([self.control(**override)])

    def test_missing_or_duplicate_control_in_darwin_candidate_is_rejected(self):
        with self.assertRaisesRegex(joint_ci.GateError, "control-stage-missing"):
            self.normalize([{"stage": "download-and-hash"}, self.skipped("start-daemon")])
        with self.assertRaisesRegex(joint_ci.GateError, "control-stage-missing"):
            self.normalize([self.control(), self.control()])

    def test_unsigned_candidate_evidence_must_be_complete_and_unblocked(self):
        incomplete = {
            "control only": [self.control()],
            "no skipped": [{"stage": "download-and-hash"}, {"stage": joint_ci.PRECHECK_STAGE}, self.control()],
            "no download": self.good_rows()[1:],
            "no precheck": self.good_rows()[:1] + self.good_rows()[2:],
            "blocked": self.good_rows() + [{"stage": "x", "result": "BLOCKED"}],
            "real stage dropped, one SKIPPED left": [r for r in self.good_rows() if r["stage"] != "config-restart-generation"],
            "unknown skipped stage added": self.good_rows() + [self.skipped("invented-stage")],
            "real stage renamed": [dict(r, stage="renamed") if r["stage"] == "config-restart-generation" else r
                                   for r in self.good_rows()],
        }
        for name, rows in incomplete.items():
            with self.subTest(name), self.assertRaisesRegex(joint_ci.GateError, "unsigned-candidate-evidence-incomplete"):
                self.normalize(rows)

    def test_control_elsewhere_is_unexpected(self):
        for kwargs in ({"mode": "pinned-production"}, {"platform": "linux-x64"}, {"schema": 1}):
            with self.subTest(kwargs), self.assertRaisesRegex(joint_ci.GateError, "control-unexpected"):
                self.normalize([self.control()], **kwargs)

    def test_unsigned_candidate_cannot_report_later_stages_as_pass(self):
        with self.assertRaisesRegex(joint_ci.GateError, "stage-not-skipped-in-unsigned-candidate"):
            self.normalize([{"stage": "start-daemon"}, self.control()])

    def test_schema1_and_pinned_evidence_unchanged_and_clean(self):
        for kwargs in ({"schema": 1}, {"mode": "pinned-production"}, {"platform": "linux-x64"}):
            safe = self.normalize([{"stage": "start-daemon"}, {"stage": "x", "result": "BLOCKED"}], **kwargs)
            self.assertEqual(safe["summary"], {"passed": 1, "skipped": 0, "blocked": 1})
            self.assertEqual(safe["result"], "PASS")

    def test_unknown_stage_result_is_not_silently_pass(self):
        with self.assertRaisesRegex(joint_ci.GateError, "runner-evidence-result"):
            self.normalize([{"stage": "start-daemon", "result": "FAIL"}], schema=1)


class CliTests(Schema2Base):
    def test_normalize_evidence_reads_lock_schema_beside_manifest_and_enforces_control(self):
        derived = joint_ci.derive_lock(self.prod, platform="darwin-arm64", **CANDIDATE)
        manifest = {"validation_mode": "candidate", "platform": "darwin-arm64", "hyphae_source_sha": "a" * 40,
                    "agent24_built_sha": "b" * 40, "production_lock_sha256": FIXTURE_SHA,
                    "derived_lock_sha256": hashlib.sha256(derived).hexdigest(),
                    "binary_sha256": {n: "e" * 64 for n in joint_ci.ARTIFACTS}}
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "hyphae.lock.json").write_bytes(derived)
            (root / "manifest.json").write_text(json.dumps(manifest))
            out = root / "out.json"
            def run(rows: list) -> int:
                (root / "raw.json").write_text(json.dumps({"result": "PASS", "assertions": rows}))
                return joint_ci.main(["normalize-evidence", "--evidence", str(root / "raw.json"),
                                      "--manifest", str(root / "manifest.json"), "--output", str(out)])
            control = {"stage": joint_ci.CONTROL_STAGE, "kind": "control", "expected": "rejected",
                       "observed": "rejected", "reason_code": "signature_rejected"}
            good = ([{"stage": "download-and-hash"}, {"stage": joint_ci.PRECHECK_STAGE}]
                    + [{"stage": n, "result": "SKIPPED", "reason": joint_ci.SKIP_REASON} for n in sorted(joint_ci.STAGE_NAMES)]
                    + [control])
            self.assertEqual(run(good), 0)
            self.assertEqual(run(good[:-1]), 1)  # control removed
            self.assertEqual(run([control]), 1)  # control alone is incomplete
            self.assertEqual(json.loads(out.read_text())["result"], "PASS_WITH_SKIPPED")
            self.assertEqual(run([{"stage": "download-and-hash"}]), 1)  # no control -> fail closed
            # Linux-style evidence: a BLOCKED stage next to an overall PASS must still exit 1 (review L1).
            manifest["platform"] = "linux-x64"
            (root / "manifest.json").write_text(json.dumps(manifest))
            self.assertEqual(run([{"stage": "a"}, {"stage": "b", "result": "BLOCKED"}]), 1)
            self.assertEqual(run([{"stage": "a"}]), 0)
            manifest["platform"] = "darwin-arm64"
            (root / "manifest.json").write_text(json.dumps(manifest))
            (root / "hyphae.lock.json").write_bytes(derived + b" ")
            self.assertEqual(run(good), 1)  # lock bytes no longer match manifest hash


class ReleaseInfoTests(Schema2Base):
    def info(self, **kwargs) -> dict:
        return joint_ci.release_info(self.prod, platform="darwin-arm64", **kwargs)

    def test_values_come_from_the_lock(self):
        info = self.info(hyphae_sha="a" * 40)
        self.assertEqual(info["ldflags"], "-buildid= -X main.version=v0.30.0")
        self.assertEqual(info["candidate_ldflags"], "-buildid= -X main.version=v0.30.0-joint.aaaaaaaaaaaa")
        self.assertEqual(info["download_url"],
                         "https://github.com/iDoris-ai/Hyphae/releases/download/v0.30.0/hyphae-darwin-arm64")
        self.assertEqual(info["asset_sha256"], self.lock["assets"]["darwin-arm64"]["sha256"])
        self.assertEqual(info["unsigned_sha256"], self.lock["reproducible"]["unsigned_sha256"]["darwin-arm64"])
        linux = joint_ci.release_info(self.prod, platform="linux-x64")
        self.assertEqual(linux["asset_sha256"], linux["unsigned_sha256"])
        self.assertNotIn("candidate_version", linux)

    def test_candidate_version_is_accepted_by_derive_lock(self):
        candidate = self.info(hyphae_sha="a" * 40)["candidate_version"]
        joint_ci.derive_lock(self.prod, platform="darwin-arm64", hyphae_sha="a" * 40,
                             hyphae_binary_sha256="b" * 64, version=candidate)

    def test_lock_text_that_does_not_match_the_recipe_is_rejected(self):
        for mutate, label in (
            (lambda l: l["reproducible"].update(recipe=l["reproducible"]["recipe"].replace("v0.30.0", "v9.9.9")),
             "production-lock-recipe"),
            (lambda l: l["reproducible"].update(recipe=l["reproducible"]["recipe"].replace("-trimpath", "")),
             "production-lock-recipe"),
            (lambda l: l.update(release_url_base="http://example.com/releases"), "production-lock-release-url"),
            (lambda l: l["assets"]["darwin-arm64"].update(name="../evil"), "production-lock-asset"),
            (lambda l: l.update(version="v0.30.0\nx=1"), "production-lock-identity"),
        ):
            lock = copy.deepcopy(self.lock)
            mutate(lock)
            with self.subTest(label), self.assertRaisesRegex(joint_ci.GateError, label):
                joint_ci.release_info(self.rebase(lock), platform="darwin-arm64")

    def test_wrong_bytes_schema_platform_and_sha_are_rejected(self):
        with self.assertRaisesRegex(joint_ci.GateError, "production-lock-hash"):
            joint_ci.release_info(self.prod + b" ", platform="darwin-arm64")
        with self.assertRaisesRegex(joint_ci.GateError, "unsupported-platform"):
            joint_ci.release_info(self.prod, platform="linux-arm64")
        with self.assertRaisesRegex(joint_ci.GateError, "hyphae-source-sha"):
            self.info(hyphae_sha="zz")
        with self.assertRaisesRegex(joint_ci.GateError, "production-lock-schema-unsupported"):
            joint_ci.release_info(self.rebase({"schema": 1}), platform="linux-x64")

    def test_check_file_sha256_cli(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "f"
            path.write_bytes(b"signed bytes")
            digest = hashlib.sha256(b"signed bytes").hexdigest()
            self.assertEqual(joint_ci.main(["check-file-sha256", "--file", str(path), "--expected", digest]), 0)
            for expected in ("0" * 64, digest.upper(), "x"):
                self.assertEqual(joint_ci.main(["check-file-sha256", "--file", str(path), "--expected", expected]), 1)


class BaselineTests(unittest.TestCase):
    """The tracked baseline must describe the schema 2 production lock the fixture is a copy of."""

    def test_baseline_pins_the_schema2_release_lock(self):
        baseline = json.loads(joint_ci.BASELINE_CONFIG_PATH.read_text())
        lock = json.loads(FIXTURE.read_bytes())
        self.assertEqual(baseline["schema"], 1)
        self.assertEqual(baseline["agent24_base_sha"], "e8b58246c5ee3a62b2583e27fc32989e3f6cbed7")
        self.assertEqual(baseline["production_lock_sha256"], FIXTURE_SHA)
        self.assertEqual(baseline["production_hyphae_sha"], lock["source"]["commit"])
        self.assertEqual("go" + baseline["go_version"], lock["reproducible"]["toolchain"])
        self.assertEqual(baseline["production_lock_path"], "rust/crates/agent24-comm/hyphae.lock.json")
        self.assertEqual((joint_ci.AGENT24_BASE_SHA, joint_ci.PRODUCTION_LOCK_SHA256, joint_ci.PRODUCTION_HYPHAE_SHA),
                         (baseline["agent24_base_sha"], FIXTURE_SHA, baseline["production_hyphae_sha"]))


if __name__ == "__main__":
    unittest.main()
