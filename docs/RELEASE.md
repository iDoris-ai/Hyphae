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
accepted by `--type install` (`source=Notarized Developer ID`). That is the consumer's (Agent24)
measurement; it has not been reproduced in this repository without credentials, so the first signed
release is the first real exercise of this command.

It writes the re-packed `hyphae-darwin-arm64.tar.gz`, linux assets unchanged, `hyphae.lock.json`
(schema 2), an updated `artifact-manifest.json` (`signature_verified: true`) and a new `SHA256SUMS`.
A hidden `--no-verify-signature` exists for tests only; its output is marked `signature_verified: false`
and `notarized: false` and must never be published.

## 5. Upload and publish

```sh
gh release upload vX.Y.Z --repo iDoris-ai/Hyphae --clobber /tmp/hyphae-final/*
gh release edit vX.Y.Z --repo iDoris-ai/Hyphae --draft=false
```

## 6. Verify the published assets

```sh
gh release download vX.Y.Z --repo iDoris-ai/Hyphae --dir /tmp/hyphae-check
(cd /tmp/hyphae-check && shasum -a 256 -c SHA256SUMS)
codesign --verify --strict /tmp/hyphae-check/hyphae-darwin-arm64
codesign -dv --verbose=4 /tmp/hyphae-check/hyphae-darwin-arm64 2>&1 | grep -E 'TeamIdentifier|flags'
spctl --assess --type install -vv /tmp/hyphae-check/hyphae-darwin-arm64
```

The linux asset sha256 in `hyphae.lock.json` must equal `reproducible.unsigned_sha256.linux-x64`
(linux is never signed).

## Trust model and known limits of `finalize_signed_release.py`

- There is no content binding between the signed and the unsigned bytes: Mach-O signing changes the file, and `codesign --remove-signature` does not restore the unsigned hash. The trust for the macOS asset rests on the signer's machine and on the Team ID, Hardened Runtime and notarization checks above. `reproducible.unsigned_sha256` records the unsigned hash so a third party can compare it with their own rebuild, but it is not proof that the signed file was built from the same source.
- The tool is run by a maintainer on their own machine; its inputs (unsigned bundle, signed directory) are treated as trusted. Hardening against a tampered input bundle is deliberately not attempted. Three known gaps from review of #341, all low: the staged copy of an unsigned relay is not re-hashed against the verified manifest hash; the unsigned bundle's own `SHA256SUMS` is not cross-checked against its manifest; a crafted manifest could give a binary and an archive the same file name. Do not run the tool on a bundle you did not build yourself.
- If only the CLI is signed, the relay inside `hyphae-darwin-arm64.tar.gz` and the raw `hyphae-relay-darwin-arm64` are unsigned; `signed_binaries` in `artifact-manifest.json` lists exactly which files are signed.
