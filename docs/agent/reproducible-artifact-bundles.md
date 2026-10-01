# Local reproducible artifact bundles

`scripts/build_release_artifacts.py` builds local release candidates from an explicit clean Git checkout. It does not create or publish a GitHub Release and does not change `install.sh`.

```bash
python3 scripts/build_release_artifacts.py \
  --source-dir /path/to/clean/hyphae-checkout \
  --source-sha a4aa606eb81d5c040d94c51cdf94553e646d8674 \
  --release-tag v0.26.1 \
  --output /tmp/hyphae-v0.26.1-artifacts
```

The source directory must be the root of a real Git checkout, its HEAD must match `--source-sha`, and tracked, untracked, and ignored paths must be clean. The output directory must not already exist and must not contain or be contained by the source checkout; its parent must already exist. Build staging, HOME, and GOCACHE are temporary directories beside the requested output and outside the source checkout. GOPATH is retained from the caller or defaults to the caller's normal `~/go` so the existing module cache can be read. Other Go build overrides are removed; `GOENV=off` and `GOWORK=off` are set.

The builder requires an already-installed Go 1.26.4. It runs `go version` with `GOTOOLCHAIN=local` first, so it will fail instead of downloading or switching toolchains. Cross-builds use `CGO_ENABLED=0`, `GOOS=darwin GOARCH=arm64` and `GOOS=linux GOARCH=amd64`, `-trimpath`, `-buildvcs=false`, and `-ldflags='-buildid='`. The CLI lock keeps the schema 1 platform keys consumed by Agent24: `darwin-arm64`, `linux-x64`, `darwin-x64`, and `linux-arm64`; the unsupported architecture hashes remain `null`.

Each archive contains `hyphae`, `hyphae-relay`, `LICENSE`, and `NOTICE`. Tar members are sorted and have fixed ownership, modes, and timestamps; gzip mtime is zero. `SHA256SUMS` covers all output files except itself. `artifact-manifest.json` records the source SHA, toolchain, recipes, binary and archive hashes, and intended Release/asset URLs. The manifest explicitly says the assets are intended and unpublished.

The standard-library black-box tests use a fake Go executable; they check orchestration, fixed environments, failures, source dirtiness, output protection, lock and manifest fields, hashes, and deterministic archives. They do not claim that a real Go build passed. Run them with:

```bash
python3 -m unittest scripts.test_build_release_artifacts -v
```
