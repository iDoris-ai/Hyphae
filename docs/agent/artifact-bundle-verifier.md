# Verify a CI artifact bundle

`python3 scripts/verify_artifact_bundle.py --bundle-dir DIR --production-lock LOCK --platform all`
checks an R1 bundle against the fixed Hyphae source, Go recipe, Agent24 production
lock, checksums, archives, and known production binary hashes. The production lock
must be supplied separately; the verifier does not trust the copy inside the bundle.

For a runtime job, use `--platform darwin-arm64` or `--platform linux-x64` and
provide a new `--extract-dir`. The verifier checks the actual host, then extracts
the verified CLI and relay into that directory. It never publishes a release.

The test suite uses synthetic binaries and a synthetic lock through the verifier's
internal validation interface. It tests parsing, corruption rejection, archive
safety, and extraction behavior; it does not claim that production hashes or a
real Go build have passed. The CLI has no test-baseline override and remains pinned
to the production lock and binary hashes.
