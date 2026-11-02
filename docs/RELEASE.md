# Hyphae release procedure

Signing and notarization credentials (Apple Developer ID certificate, notarytool profile) stay on the
signer's machine. This repository, its scripts and the coordinator never handle them: no script here
calls `codesign --sign` or `notarytool`.

## 1. Build the unsigned bundle twice and compare

From a clean checkout of the release commit (Go `go1.26.4` installed locally):

```sh
python3 scripts/build_release_artifacts.py --source-dir . --source-sha <full-sha> --release-tag vX.Y.Z --stamp-version --output /tmp/hyphae-a
python3 scripts/build_release_artifacts.py --source-dir . --source-sha <full-sha> --release-tag vX.Y.Z --stamp-version --output /tmp/hyphae-b
diff /tmp/hyphae-a/SHA256SUMS /tmp/hyphae-b/SHA256SUMS   # must be identical
```

Release builds pass `--stamp-version`, which builds the CLI with `-X main.version=<tag>` and records the concrete-tag recipe in the manifest and lock (default builds, used for the locked baseline CI, are unchanged). `finalize_signed_release.py` refuses a bundle whose recipe is not the stamped one for its tag, and copies that recipe into `hyphae.lock.json`. So `hyphae contract` reports `binary_version` = the tag.
A different tag produces different CLI bytes by design.

## 2. Create the draft release with the raw unsigned assets

```sh
gh release create vX.Y.Z --repo iDoris-ai/Hyphae --draft --title vX.Y.Z /tmp/hyphae-a/*
```

## 3. Hand off to the signer

Give the signer `hyphae-darwin-arm64` (and optionally `hyphae-relay-darwin-arm64`) from the bundle.
The signer runs, on their own machine, `codesign --force --options runtime --timestamp` with the
Developer ID identity (team `HYAP444FN9`), notarizes the binary (a bare Mach-O cannot be stapled; that
is fine, Gatekeeper checks online), and returns the SIGNED RAW BINARIES under the same file names in
one directory. Nothing else may be in that directory.

## 4. Finalize

On macOS, with the source checkout at the release commit:

```sh
python3 scripts/finalize_signed_release.py --unsigned-bundle /tmp/hyphae-a --signed-dir <signed-dir> \
  --source-dir . --output /tmp/hyphae-final
```

The tool fails closed and leaves no output unless: the unsigned bundle matches its manifest exactly;
each signed binary is an arm64 Mach-O that differs from the unsigned one; `codesign --verify --strict`
passes; `codesign -dv` shows `TeamIdentifier=HYAP444FN9` and the `runtime` flag; and
`spctl --assess --type install -vv` succeeds and reports `source=Notarized Developer ID`; the checks run on the tool's private staged copy of each signed binary, not on the input directory. The assess type is `install`, not `execute`: a bare,
notarized, un-stapled CLI binary is rejected by `--type execute` ("does not seem to be an app") but
accepted by `--type install` (`source=Notarized Developer ID`). That was the consumer's (Agent24) measurement and was confirmed by the first signed release (v0.30.0), where
`finalize_signed_release.py` ran this command for real; see the observed output at the end of this file.

It writes the re-packed `hyphae-darwin-arm64.tar.gz`, linux assets unchanged, `hyphae.lock.json`
(schema 2), an updated `artifact-manifest.json` (`signature_verified: true`) and a new `SHA256SUMS`.
A hidden `--no-verify-signature` exists for tests only; its output is marked `signature_verified: false`
and `notarized: false` and must never be published.

## 5. Upload and publish

**Only publish after `finalize_signed_release.py` has succeeded and the draft holds exactly its output.** Until then the draft still contains the raw UNSIGNED macOS binaries from step 2; `gh release edit --draft=false` on that draft would publish unsigned assets. After uploading, download the draft's assets and compare them byte for byte with the finalize output before un-drafting.

```sh
gh release upload vX.Y.Z --repo iDoris-ai/Hyphae --clobber /tmp/hyphae-final/*
gh release edit vX.Y.Z --repo iDoris-ai/Hyphae --draft=false
```

## 6. Verify the published assets

```sh
gh release download vX.Y.Z --repo iDoris-ai/Hyphae --dir /tmp/hyphae-check
(cd /tmp/hyphae-check && shasum -a 256 -c SHA256SUMS)
for f in hyphae-darwin-arm64 hyphae-relay-darwin-arm64; do
  codesign --verify --strict /tmp/hyphae-check/$f
  codesign -dv --verbose=4 /tmp/hyphae-check/$f 2>&1 | grep -q 'TeamIdentifier=HYAP444FN9' || echo "WRONG TEAM: $f"
  codesign -dv --verbose=4 /tmp/hyphae-check/$f 2>&1 | grep -q 'flags=0x10000(runtime)' || echo "NO HARDENED RUNTIME: $f"
  spctl --assess --type install -vv /tmp/hyphae-check/$f 2>&1 | grep -q 'source=Notarized Developer ID' || echo "NOT NOTARIZED: $f"
done
```

No output from the loop other than what `codesign --verify` prints on success (nothing) means all checks passed. (Skip `hyphae-relay-darwin-arm64` if only the CLI was signed.)

The linux asset sha256 in `hyphae.lock.json` must equal `reproducible.unsigned_sha256.linux-x64`
(linux is never signed).

## Trust model and known limits of `finalize_signed_release.py`

- There is no content binding between the signed and the unsigned bytes: Mach-O signing changes the file, and `codesign --remove-signature` does not restore the unsigned hash. The trust for the macOS asset rests on the signer's machine and on the Team ID, Hardened Runtime and notarization checks above. `reproducible.unsigned_sha256` records the unsigned hash so a third party can compare it with their own rebuild, but it is not proof that the signed file was built from the same source.
- The tool is run by a maintainer on their own machine; its inputs (unsigned bundle, signed directory) are treated as trusted. Hardening against a tampered input bundle is deliberately not attempted. Three known gaps from review of #341, all low: the staged copy of an unsigned relay is not re-hashed against the verified manifest hash; the unsigned bundle's own `SHA256SUMS` is not cross-checked against its manifest; a crafted manifest could give a binary and an archive the same file name. Do not run the tool on a bundle you did not build yourself.
- If only the CLI is signed, the relay inside `hyphae-darwin-arm64.tar.gz` and the raw `hyphae-relay-darwin-arm64` are unsigned; `signed_binaries` in `artifact-manifest.json` lists exactly which files are signed.

## 7. Telling downstream consumers (Agent24)

Copy every hash you send from `SHA256SUMS` **downloaded from the published release** (step 6), never from a build or staging directory: the unsigned bundle and the final bundle have different `hyphae-darwin-arm64.tar.gz` hashes and mixing them up has happened once (v0.30.0). The lock to consume is the published `hyphae.lock.json`, byte for byte; send its sha256 rather than retyping fields.

## Observed output of the first signed release (v0.30.0)

For reference when checking a later release, this is what the real commands printed for `hyphae-darwin-arm64` from the published v0.30.0 assets:

```
$ codesign --verify --strict --verbose=2 hyphae-darwin-arm64
hyphae-darwin-arm64: valid on disk
hyphae-darwin-arm64: satisfies its Designated Requirement
$ codesign -dv --verbose=4 hyphae-darwin-arm64        (excerpt)
Identifier=hyphae-darwin-arm64
Format=Mach-O thin (arm64)
CodeDirectory v=20500 flags=0x10000(runtime)
Authority=Developer ID Application: Huifeng Jiao (HYAP444FN9)
TeamIdentifier=HYAP444FN9
$ spctl --assess --type install -vv hyphae-darwin-arm64
hyphae-darwin-arm64: accepted
source=Notarized Developer ID
origin=Developer ID Application: Huifeng Jiao (HYAP444FN9)
```

`hyphae-relay-darwin-arm64` printed the same with `Identifier=hyphae-relay-darwin-arm64`. This confirms the consumer's finding that `--type install` accepts a notarized, un-stapled bare binary; it was exercised by `finalize_signed_release.py` itself during that release.
