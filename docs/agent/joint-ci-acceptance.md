# Agent24 × Hyphae joint CI acceptance

Status: workflow and provenance helper are under development on `codex/joint-ci-handoff-20261007`; no Actions run has exercised this workflow yet. Local unit/YAML validation is not ten-stage CLI/relay acceptance.

## Independent inputs

| Input | Pinned value | Meaning |
|---|---|---|
| Agent24 build source | `fc862cf3f765f3e59686e816aea6fa4792f10da2` | Public, last merged-source CLI acceptance source |
| Production lock SHA-256 | `a83b7a586b1e19693d4abbbe4d1c737cf9fb5d6e2f63b7d8ebc6a252e1032ffd` | Exact bytes in that Agent24 commit |
| Production lock `source_sha` | `671c584f9e9eb807a15968e2aa42fd7507e178b8` | Hyphae source for pinned-production baseline |
| Go toolchain | `go1.26.4` | Fixed release build recipe |
| Candidate source | triggering `github.sha` | PR/push source under test; must not replace the production input |

The workflow checks out public Agent24 at the pinned full SHA with only `contents: read` and `persist-credentials: false`. It never requests repository secrets and uses `pull_request`, not `pull_request_target`; candidate code runs only in ordinary, unprivileged jobs. No fork PR token is made available to the build or runner.

## Two distinct validation modes

`pinned-production` builds Hyphae at the fixed production `source_sha`, verifies the original lock SHA, builds Agent24 from the pinned source with the unmodified lock, then passes that same lock and its original SHA to the unchanged strict runner. This protects the historical production baseline.

`candidate` builds Hyphae from the triggering source SHA. It keeps the production lock bytes as an independent input, then derives a temporary lock by changing only `source_sha` and the current Linux target’s Hyphae binary SHA-256. Agent24 is copied into a temporary build tree and built with that derived lock embedded; the Agent24 repository and production lock are never edited. The producer manifest binds the fixed Agent24 base/build SHA, candidate Hyphae SHA, original production lock SHA, derived lock SHA, Go version/recipe, selected platform, and all four executable hashes.

The consumer downloads only a same-run producer artifact, independently checks the manifest hash against the producer job output, re-hashes each executable, rechecks the original lock bytes and exact allowed lock diff, and verifies platform and source against the triggering event. Because Actions ZIP transport does not preserve executable bits, only after all producer-output, lock, and content-hash gates pass, the consumer restores mode `0755` on the four fixed, regular, non-symlink binaries through already-verified file descriptors and rechecks content hashes. No mode change occurs unless the entire binary set passes validation. It then invokes the existing strict runner with the derived lock SHA. The strict runner’s `check_inputs` source/hash gates are not patched, bypassed, or monkeypatched. Candidate results are labeled `validation_mode: candidate`; they are never represented as production-lock acceptance.

## Evidence and workflow gates

The runner writes its full diagnostic evidence only under the ephemeral runner temp directory. The upload step emits a normalized allowlist: validation mode, platform, source SHAs, lock/binary hashes, final result, stage labels, and (when available) structured `primary_failure` and cleanup failure `{stage, category, errno}`. It drops raw process commands, output, HTTP details, HOME/database paths, passwords, bearer tokens, and unrecognized fields. The producer artifact contains only the four executable inputs, derived/original lock copy, and manifest; it does not contain raw runner evidence or a HOME.

The reusable workflow is called by the ordinary CI workflow and also has scheduled/manual entry points. It runs both pinned-production and candidate modes on Linux x64, including on Dependabot PRs (there are no path filters that can skip dependency changes). The main CI stable `ci-ok` job requires `test`, joint acceptance, and the macOS cleanup stress job to have the literal result `success`; skipped, neutral, failed, or cancelled jobs fail the stable gate. The cleanup stress repeats the pipe-holding descendant regression 50 times under Python 3.14.7 on macOS and depends on the separately owned #130 fixture/runner repair.

## Limits and next steps

- The scheduled workflow checks the pinned baseline and the Hyphae source at the workflow’s triggering commit. It does not automatically detect a new commit in the separate Agent24 repository. An Agent24 lock/source change must update this repository’s tracked pin configuration or explicitly dispatch a reviewed run; do not claim cross-repository automation that is not present.
- The current local implementation is Linux-only for the ten-stage workflow; macOS coverage is the independent cleanup stress. A local Darwin arm64 producer/consumer run is useful additional evidence but does not substitute for Actions.
- Do not say that candidate PR code passed joint acceptance until its exact SHA has a successful consumer job. Unit tests and YAML parsing validate the gate mechanics only.
- #130 owns the existing runner/test implementation. Keep future changes to the locked-source helper, workflow, and documentation separated from those files.
