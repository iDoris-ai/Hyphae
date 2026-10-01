# Locked communication artifact CI

`.github/workflows/comm-artifacts.yml` builds the fixed a4 Hyphae commit with Go
1.26.4, verifies the resulting bundle against the separately checked out Agent24
production lock, and runs the downloaded bundle on Linux x64 and macOS arm64. Each
runtime job checks its host and revalidates the archive and executable hashes before
starting the real CLI/relay round-one test.

The bundle and the sanitized round-one logs are short-lived workflow artifacts tied
to the run and attempt. They are not a GitHub Release and do not provide permanent
download URLs. The Linux relay hash comes from the successful build verification and
is compared again after download; the macOS relay must also match its historical
production hash.

A successful cross-build alone does not prove a target can run the binaries. Both
runtime matrix jobs must finish successfully for the workflow to pass. GitHub-hosted
runners must permit local loopback listeners for the relay test.
