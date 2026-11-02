# Hyphae release procedure

Signing and notarization credentials (Apple Developer ID certificate, notarytool profile) stay on the
signer's machine. This repository, its scripts and the coordinator never handle them: no script here
calls `codesign --sign` or `notarytool`.

## 1. Build the unsigned bundle twice and compare

From a clean checkout of the release commit (Go `go1.26.4` installed locally):

```sh
python3 scripts/build_release_artifacts.py --source-dir . --source-sha <full-sha> --release-tag vX.Y.Z --output /tmp/hyphae-a
python3 scripts/build_release_artifacts.py --source-dir . --source-sha <full-sha> --release-tag vX.Y.Z --output /tmp/hyphae-b
diff /tmp/hyphae-a/SHA256SUMS /tmp/hyphae-b/SHA256SUMS   # must be identical
```

The CLI is built with `-X main.version=<tag>`, so `hyphae contract` reports `binary_version` = the tag.
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
