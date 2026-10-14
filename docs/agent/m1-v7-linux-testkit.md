# M1 V7 Linux legacy-artifact + strace testkit (tests/scripts/docs only)

状态 / Status: **testkit scaffold only — NOT V7 closure, NOT M1 closure, no runtime activation.**
Base under test `9230cce44a2486a94fab40f95de5ab3099a4dd61` ships only the *passive*
`internal/deployment` validator (PR1, **unaccepted**); there is no launcher / gate yet.
Authority: [rev7 contract](m1-group-fanout-contract-20261008.md) §3.6.1 and §3.7 **V7**.
Production code changes in this delivery: **0** (`git diff 9230cce -- cmd internal pkg` is empty).

## What is delivered

| File | Purpose |
| --- | --- |
| `scripts/v7_linux_kit.py` | Portable stdlib-only harness: `build`, `probe`, `retire`, `harness`, `windows`, `all` |
| `scripts/test_v7_linux_kit.py` | Unit tests for trace parsing, mutation classification, group-id scan, snapshot diff and fail-closed exits |
| `docs/agent/m1-v7-linux-testkit-evidence-20261014.json` | Sanitized Cloud Linux run output (paths shown as `$WORK`) |

Exit codes: `0` all executed checks pass and nothing skipped · `1` a check failed ·
`2` usage/env error · `3` **NOTIMPLEMENTED / SKIP present**. `all` currently always
exits `3` because the launcher-dependent sequence and the O3/D6 windows are not
implementable at this base. **A `3` is not green.**

## Frozen legacy artifacts (real builds, never the current binary relabelled)

`build` runs `git archive <sha>` (source tar hashed), extracts into a temp dir and builds
`CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags='-buildid= -X main.version=legacy-<sha>' ./cmd/hyphae`.
The version stamp carries the full source SHA, so `--version` output plus the `execve`
path and binary sha256 bind *exec identity → binary hash → source SHA*.

| Role (rev7 §0 table) | Source SHA | Tree | Source tar sha256 | Linux amd64 binary sha256 |
| --- | --- | --- | --- | --- |
| exact base | `699b36f74c6b3802b93768d670229d7b2d3b6cff` | `5e15f3cb16f76d3a1090865dda1f61c047b165d3` | `41eca0cd…2064e9` | `50c07a0dca82a6d2859c855cb5565e3e1d5b72aecc934bbfa7ab17d0cca57b77` |
| approved #165 (S6) | `c0a54111f78f357c6ae2772f06f9a3cc4fe7c63b` | `7a29378211dddc5c3b9e487b220522ae358902bb` | `d53ad2db…3a5c` | `994f484dab3020b3d1656c89de464807342f786266ad6ef2827b10b91cb528d1` |
| approved #166 (S5b) | `7861d029b5ccc2959b80a184a390ec132ddd025f` | `5fde217fafbd97831f8a6b905b512992d895083d` | `ac637e52…5933f` | `868a8f0c33b32d825c47d35a10ea3269df0b5fb268d00ced04fd0a040a07607a` |
| forward target (base under test) | `9230cce44a2486a94fab40f95de5ab3099a4dd61` | `37ba82ed3a13ad56d3215b4eb49253e488a6bb68` | `6c82bdad…a07d` | `8223e217adc195e75e34066daa5695ac8fd6855396deb62512c8856569318d76` |

Full hashes are in the evidence JSON. Two consecutive builds on the same host produced identical
binary hashes (same-host reproducibility only; cross-host reproducibility is **unverified**).
Toolchain: module `go 1.26.0` → `GOTOOLCHAIN=auto` selected go1.26.0 (host default go1.24.4).

## Commands (portable; run from a repo checkout)

```sh
git fetch origin 699b36f74c6b3802b93768d670229d7b2d3b6cff c0a54111f78f357c6ae2772f06f9a3cc4fe7c63b 7861d029b5ccc2959b80a184a390ec132ddd025f
python3 scripts/test_v7_linux_kit.py -v                          # unit tests, no strace needed
STRACE=/path/to/strace python3 scripts/v7_linux_kit.py all \
    --repo . --work /tmp/v7kit-work --new-sha <base-under-test-sha>   # expect exit 3 until F3 exists
python3 scripts/v7_linux_kit.py windows                           # exit 3: plan only
python3 scripts/v7_linux_kit.py harness --work /tmp/v7kit-work \
    --hyphae-bin <new hyphae> --hyphae-source-sha <40hex> \
    --launcher-bin <launcher> --launcher-source-sha <40hex>       # exit 3 until an accepted F3 launcher protocol exists
```

The kit never uses sudo, never installs packages, never changes permissions outside its own
temp tree, never reads the real user HOME, never touches system services or production paths,
and needs no credentials. If `strace` is absent it prints `SKIP` and exits `3`.
On the Cloud Linux run `strace` 6.13 was **not installed**: the Debian trixie `.deb`
(`strace_6.13+ds-1_amd64.deb`, sha256 `cae290b6…af47511`) was unpacked into a scratch
directory outside the repo and passed via `STRACE=`; it traced only its own children,
nonprivileged (uid 1000, no ptrace capability).

## What each stage proves (and does not)

1. **Private owned test HOME** — every probe gets a fresh `mkdtemp` HOME (asserted uid-owned, `0700`,
   not inside the real HOME); `HOME`/`XDG_*`/`TMPDIR` point inside it; env is cleared to `PATH=/usr/bin:/bin`.
2. **Tracer self-check (positive control)** — `strace -ff` on `/bin/sh -c 'mkdir; : >; mv; rm; rmdir'` must show
   exec, `open(O_CREAT)`, `rename*`, `unlink*` and ≥2 traced pids, otherwise a clean legacy trace would mean nothing.
3. **Read-only version/help sandbox** — each legacy binary runs only `--version` / `--help` in an *empty* private HOME
   under `strace -ff -e trace=execve,execveat,open*,creat,rename*,unlink*,mkdir*,rmdir,link*,symlink*`.
   Asserts: `execve` of the exact absolute artifact path, sha256 unchanged before/after and equal to the build record,
   version stamp present, zero successful mutating syscalls anywhere, zero touches of the HOME / store names,
   empty HOME snapshot diff, zero group IDs in stdout/stderr. Known-child `/proc/<pid>/fd` is sampled while each
   harness-owned child (and its descendants via `/proc/<pid>/task/*/children`) is alive.
   **Old programs are never opened against any fixture or live store.**
4. **Legacy retirement exercise** — per artifact and per surface (`path`, `alias`, `function`, bash `hash` cache,
   `absolute` installed path), a *pre* positive control (surface resolves to and `execve`s the legacy hash) and a
   *post* run after retirement (`rm` PATH copy + `hash -r`, `unalias`, `unset -f`, `hash -r`, absolute path replaced by a
   **test-only stub**). Post asserts no successful `execve` of any file whose sha256 is a legacy hash; PATH/alias/
   function/cache resolve to nothing (`127`); the absolute stub forwards **only** `--version`/`--help` to the fixed new
   binary (forward new-metadata probe: new hash + `forward-<sha>` stamp observed) and refuses `--legacy-version` with
   `NOTIMPLEMENTED` exit `3`. The stub is *not* a launcher and invents no production gate.
5. **Harness skeleton** — parameters: final hyphae binary + source SHA, launcher binary + source SHA. Sequence
   `close → quiesce-1 → oneway-marker → quiesce-2 → maintenance`. Quiescence waits **only real harness-owned
   children** (Popen handles + their descendants), never scans or kills foreign processes; a synthetic self-test child
   holding a file open in the private HOME proves the wait + `/proc/fd` observation. The managed marker is
   specified as **one-way and NON-activating** and is **not** old-writer exclusion (rev7 §3.6.1: marker/flock cannot
   exclude old binaries). With no accepted launcher protocol every step reports `NOTIMPLEMENTED` and the exit is `3`.
6. **O3/D6 windows** — `windows` emits the preserved plan and exits `3`.

## Cloud Linux actual run (2026-10-14)

- hostname: `grok-bot-vm-307756233`
- uname: `Linux grok-bot-vm-307756233 6.12.94+ #1 SMP PREEMPT_DYNAMIC Mon Oct 12 19:59:53 UTC 2026 x86_64 GNU/Linux`
- go: host `go1.24.4 linux/amd64`; build toolchain auto-selected `go1.26.0 linux/amd64`
- strace: 6.13 (user-space unpacked, see above)

| Command | Exit | Result |
| --- | --- | --- |
| `python3 scripts/test_v7_linux_kit.py -v` | 0 | 9/9 unit tests |
| `STRACE=… python3 scripts/v7_linux_kit.py all --repo . --work $WORK --new-sha 9230cce…` | **3** | probe 7/7 rows pass (1 tracer self-check + 3 SHAs × version/help); retire 30/30 rows pass (3 SHAs × 5 surfaces × pre/post); harness 5/5 steps NOTIMPLEMENTED; windows NOTIMPLEMENTED |
| `go test ./internal/deployment/ -count=1` | 0 | base passive validator unit tests (unchanged code) |

## Coverage matrix vs rev7 V7 — honest status

| V7 requirement | Status here |
| --- | --- |
| Real exact base / #165 / #166 Linux artifacts with source + binary identity | **covered** (built, hashed, exec-identity traced) |
| Nonprivileged `strace -ff` exec/open/rename/unlink audit; known child `/proc/fd` | **covered** for read-only probes and retirement exercise |
| PATH / alias / function / cache / absolute-path retirement + forward-only read-only probe | **covered in private HOME with test stub**; launcher-mediated forwarding **UNVERIFIED** |
| Legacy-version request refused | stub-level only (exit 3); **launcher refusal code UNVERIFIED** |
| Snapshot diff, zero group IDs in messages | **covered** for HOME file snapshots; SQLite logical snapshot **UNVERIFIED** (no store opened by design) |
| Unmanaged DM compatibility preserved | preserved by zero production diff; **DM runtime regression not exercised here** |
| Per registered entry (one-shot DM enqueue, clear, cleanup, TUI/daemon restart, automation, group callback) | **UNVERIFIED** — needs F3 entry inventory + launcher |
| Windows: activation / O3 committed before T4 / D6 committed before T2′ (incl. SIGKILL) using P-O3, P-BUDGET-9/10, P-MISSING, P-EXHAUSTED-*, `legacy-165-group-nil-relays` | **UNVERIFIED / NOTIMPLEMENTED** — needs F3/F4 writers and launcher; old programs must never be opened against these fixtures |
| close → quiesce → one-way marker → second quiesce → maintenance | skeleton only, **NOTIMPLEMENTED** |
| Fail-closed injections: unknown entry, owner/hash/HOME mismatch, auto-restart not disabled, interrupted upgrade, rollback request | **UNVERIFIED** (passive predicate unit tests exist in `internal/deployment` but are not OS proof) |
| systemd/launchd/cron surfaces, macOS | **out of scope** (no system services touched; Linux only) |
| Arbitrary copied same-UID/same-HOME executable | outside rev7 support model; **never claimed** |

Next: coordinator cross-model acceptance; an accepted F3 launcher protocol is required before the
harness sequence and windows can run. Nothing here activates M1, S7 or V7.
