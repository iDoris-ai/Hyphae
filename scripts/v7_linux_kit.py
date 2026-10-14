#!/usr/bin/env python3
"""V7 Linux legacy-artifact + nonprivileged strace testkit (tests/scripts only).

THIS IS NOT A V7 GATE AND NOT V7 CLOSURE.  Authority: rev7 contract
docs/agent/m1-group-fanout-contract-20261008.md section 3.6.1 / 3.7 V7.

What it does, all inside a private, harness-owned test HOME (mkdtemp, 0700):
  build    git-archive the exact frozen SHAs and build real Linux binaries
  probe    read-only --version/--help exec of each artifact under strace -ff,
           with exec identity, mutation audit and HOME snapshot diff
  retire   legacy PATH / alias / function / hash-cache / absolute-installed
           path exercise: positive control, retirement, then a forward-only
           read-only metadata probe of the new binary via a test stub
  harness  close -> quiesce -> one-way marker -> SECOND quiesce -> maintenance
           skeleton; every launcher-dependent step reports NOTIMPLEMENTED and
           the command exits nonzero (never green without a real launcher)
  windows  O3/T4 and D6/T2' V7 windows: plan only, NOTIMPLEMENTED (nonzero)
  all      build + probe + retire + harness + windows, writes summary.json

Hard rules enforced by the code:
  * never uses sudo, never installs, never chmods anything outside its own
    temp tree, never reads the real user HOME, never touches system services;
  * never opens an old program against a new/live fixture (old binaries only
    get --version/--help in an empty private HOME);
  * never fabricates a historical artifact from the current binary;
  * a missing launcher is NOTIMPLEMENTED (exit 3), never success.
Exit codes: 0 all executed checks passed and nothing skipped; 1 a check
failed; 2 usage/environment error; 3 NOTIMPLEMENTED/SKIP present.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
from pathlib import Path

EXIT_OK, EXIT_FAIL, EXIT_USAGE, EXIT_NOTIMPLEMENTED = 0, 1, 2, 3

# rev7 contract section "实现状态与精确基线" / impl plan section 0.
FROZEN = {
    "699b36f74c6b3802b93768d670229d7b2d3b6cff": "exact-base",
    "c0a54111f78f357c6ae2772f06f9a3cc4fe7c63b": "approved-165-S6",
    "7861d029b5ccc2959b80a184a390ec132ddd025f": "approved-166-S5b",
}
# Shared raw example group id used by docs/agent/m1-outbox-result-witness-schema.md
# (P-O3 / P-MISSING / P-EXHAUSTED-*).  Messages must contain ZERO group ids.
FIXTURE_GROUP_IDS = ("82bd2972cccd0403b59cdaa4412c6664",)
GROUP_ID_RE = re.compile(r"group[ _-]?id\W{0,4}[0-9a-f]{32}", re.I)
STORE_NAMES = ("outbox.json", "outbox.lock", "messages.db", "messages.db-wal",
               "messages.db-shm", "daemon.lock", "groupchat")
TRACE_SET = ("execve,execveat,open,openat,openat2,creat,rename,renameat,renameat2,"
             "unlink,unlinkat,mkdir,mkdirat,rmdir,link,linkat,symlink,symlinkat")
MUTATING = {"creat", "rename", "renameat", "renameat2", "unlink", "unlinkat",
            "mkdir", "mkdirat", "rmdir", "link", "linkat", "symlink", "symlinkat"}
WRITE_FLAGS = ("O_WRONLY", "O_RDWR", "O_CREAT", "O_TRUNC", "O_APPEND")
SAFE_ENV_PATH = "/usr/bin:/bin"


def sha256_file(p: Path) -> str:
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def run(cmd, **kw) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, capture_output=True, text=True, **kw)


# ---------------------------------------------------------------- private HOME
def private_home(work: Path, tag: str) -> Path:
    real_home = os.environ.get("HOME", "")
    base = work / "homes"
    base.mkdir(parents=True, exist_ok=True)
    h = Path(tempfile.mkdtemp(prefix=f"{tag}-", dir=base))  # mkdtemp => 0700
    st = h.stat()
    if st.st_uid != os.getuid() or stat.S_IMODE(st.st_mode) != 0o700:
        raise SystemExit(f"private HOME not owned/0700: {h}")
    if real_home and (h == Path(real_home) or Path(real_home) in h.parents):
        raise SystemExit("refusing: private HOME would live inside the real user HOME")
    return h


def sandbox_env(home: Path, path: str = SAFE_ENV_PATH) -> dict:
    return {"HOME": str(home), "PATH": path, "LANG": "C", "LC_ALL": "C",
            "XDG_CONFIG_HOME": str(home / ".config"), "XDG_DATA_HOME": str(home / ".local/share"),
            "XDG_CACHE_HOME": str(home / ".cache"), "XDG_STATE_HOME": str(home / ".local/state"),
            "TMPDIR": str(home / "tmp"), "HYPHAE_OUTPUT": "", "NO_COLOR": "1"}


def snapshot(root: Path) -> dict:
    snap = {}
    for p in sorted(root.rglob("*")):
        rel = str(p.relative_to(root))
        st = p.lstat()
        ent = {"mode": oct(stat.S_IMODE(st.st_mode)), "type": "d" if p.is_dir() else ("l" if p.is_symlink() else "f")}
        if ent["type"] == "f":
            ent["size"] = st.st_size
            ent["sha256"] = sha256_file(p)
        elif ent["type"] == "l":
            ent["target"] = os.readlink(p)
        snap[rel] = ent
    return snap


def snapshot_diff(a: dict, b: dict) -> list:
    out = []
    for k in sorted(set(a) | set(b)):
        if a.get(k) != b.get(k):
            out.append({"path": k, "before": a.get(k), "after": b.get(k)})
    return out


# ---------------------------------------------------------------- strace parse
LINE_RE = re.compile(r"^(?:\d+ +)?(?:\d+:\d+:\d+(?:\.\d+)? +)?(\w+)\((.*)\)\s+=\s+(-?\d+|\?)(.*)$")
STR_RE = re.compile(r'"((?:[^"\\]|\\.)*)"')


def parse_trace_text(text: str, pid: str = "") -> list:
    events = []
    for line in text.splitlines():
        m = LINE_RE.match(line.strip())
        if not m:
            continue
        sc, args, ret = m.group(1), m.group(2), m.group(3)
        strs = STR_RE.findall(args)
        events.append({"pid": pid, "syscall": sc, "args": args, "paths": strs,
                       "ok": ret != "?" and int(ret) >= 0, "ret": ret})
    return events


def parse_trace_dir(d: Path) -> list:
    events = []
    for f in sorted(d.glob("t.*")):
        events.extend(parse_trace_text(f.read_text(errors="replace"), f.suffix.lstrip(".")))
    return events


def classify(events: list, home: Path) -> dict:
    execs, mutations, home_opens, store_opens = [], [], [], []
    hs = str(home)
    for e in events:
        sc = e["syscall"]
        if sc in ("execve", "execveat"):
            if e["paths"]:
                execs.append({"pid": e["pid"], "path": e["paths"][0], "ok": e["ok"]})
            continue
        mut = sc in MUTATING or (sc.startswith("open") or sc == "creat") and any(f in e["args"] for f in WRITE_FLAGS)
        for p in e["paths"]:
            if mut and e["ok"]:
                mutations.append({"pid": e["pid"], "syscall": sc, "path": p})
            if p.startswith(hs):
                home_opens.append({"pid": e["pid"], "syscall": sc, "path": p, "ok": e["ok"]})
            if any(n in p for n in STORE_NAMES):
                store_opens.append({"pid": e["pid"], "syscall": sc, "path": p, "ok": e["ok"]})
    return {"execs": execs, "mutations": mutations, "home_touches": home_opens, "store_touches": store_opens}


def group_id_hits(texts) -> int:
    n = 0
    for t in texts:
        n += sum(t.count(g) for g in FIXTURE_GROUP_IDS)
        n += len(GROUP_ID_RE.findall(t))
    return n


# ---------------------------------------------------------------- known children
class Children:
    """Only children spawned by this harness are ever waited on / inspected."""

    def __init__(self):
        self.procs = []

    def spawn(self, cmd, **kw) -> subprocess.Popen:
        p = subprocess.Popen(cmd, **kw)
        self.procs.append(p)
        return p

    @staticmethod
    def fd_targets(pid: int) -> list:
        out = []
        fdd = Path(f"/proc/{pid}/fd")
        try:
            for fd in fdd.iterdir():
                try:
                    out.append(os.readlink(fd))
                except OSError:
                    pass
        except OSError:
            pass
        return sorted(out)

    def descendants(self, pid: int) -> list:
        res, stack = [], [pid]
        while stack:
            cur = stack.pop()
            for task in Path(f"/proc/{cur}/task").glob("*"):
                try:
                    kids = (task / "children").read_text().split()
                except OSError:
                    kids = []
                for k in kids:
                    res.append(int(k)); stack.append(int(k))
        return res

    def quiesce(self, timeout: float, watch_prefix: str) -> dict:
        samples, hits = 0, []
        deadline = time.monotonic() + timeout
        for p in self.procs:
            while p.poll() is None and time.monotonic() < deadline:
                for pid in [p.pid] + self.descendants(p.pid):
                    fds = self.fd_targets(pid)
                    samples += 1
                    hits.extend(f for f in fds if f.startswith(watch_prefix))
                time.sleep(0.01)
        alive = [p.pid for p in self.procs if p.poll() is None]
        proc_gone = all(not Path(f"/proc/{p.pid}").exists() or p.returncode is not None for p in self.procs)
        return {"children": len(self.procs), "alive_after_wait": alive, "proc_entries_reaped": proc_gone,
                "fd_samples": samples, "fd_hits_under_watch": sorted(set(hits)),
                "exit_codes": [p.returncode for p in self.procs]}


# ---------------------------------------------------------------- build
def build_one(repo: Path, sha: str, out: Path, label: str) -> dict:
    if run(["git", "-C", str(repo), "cat-file", "-t", sha]).stdout.strip() != "commit":
        raise SystemExit(f"frozen SHA not present as commit (git fetch origin {sha}): {sha}")
    tree = run(["git", "-C", str(repo), "rev-parse", f"{sha}^{{tree}}"]).stdout.strip()
    d = out / sha[:12]
    d.mkdir(parents=True, exist_ok=True)
    tar = d / f"hyphae-src-{sha}.tar"
    r = run(["git", "-C", str(repo), "archive", "--format=tar", f"--prefix=hyphae-{sha[:12]}/", "-o", str(tar), sha])
    if r.returncode:
        raise SystemExit(r.stderr)
    src = Path(tempfile.mkdtemp(prefix="src-", dir=d))
    with tarfile.open(tar) as tf:
        tf.extractall(src, filter="data")
    srcdir = src / f"hyphae-{sha[:12]}"
    binp = d / "bin" / "hyphae"
    binp.parent.mkdir(exist_ok=True)
    version = f"{label}-{sha}"
    env = dict(os.environ, CGO_ENABLED="0", GOOS="linux", GOFLAGS="-mod=readonly")
    cmd = ["go", "build", "-trimpath", "-buildvcs=false",
           f"-ldflags=-buildid= -X main.version={version}", "-o", str(binp), "./cmd/hyphae"]
    t0 = time.monotonic()
    b = run(cmd, cwd=srcdir, env=env)
    if b.returncode:
        raise SystemExit(f"build failed for {sha}: {b.stderr[-2000:]}")
    gv = run(["go", "version", "-m", str(binp)]).stdout
    shutil.rmtree(src)
    return {"sha": sha, "role": FROZEN.get(sha, label), "tree": tree,
            "source_tar": str(tar), "source_tar_sha256": sha256_file(tar),
            "binary": str(binp), "binary_sha256": sha256_file(binp), "version_stamp": version,
            "go_version_m": gv.splitlines()[:3], "build_cmd": " ".join(cmd),
            "build_seconds": round(time.monotonic() - t0, 1)}


def cmd_build(a) -> int:
    out = Path(a.out).resolve()
    shas = a.sha or list(FROZEN)
    arts = [build_one(Path(a.repo), s, out, "legacy") for s in shas]
    fwd = build_one(Path(a.repo), a.new_sha, out / "forward", "forward") if a.new_sha else None
    man = {"schema": "v7-linux-kit/artifacts-v1", "legacy": arts, "forward_new": fwd}
    (out / "legacy-artifacts.json").write_text(json.dumps(man, indent=2) + "\n")
    with open(out / "SHA256SUMS", "w") as f:
        for x in arts + ([fwd] if fwd else []):
            f.write(f"{x['binary_sha256']}  {x['sha'][:12]}/hyphae\n{x['source_tar_sha256']}  {Path(x['source_tar']).name}\n")
    print(json.dumps({"built": [(x["sha"], x["binary_sha256"]) for x in arts], "forward": fwd and fwd["binary_sha256"]}))
    return EXIT_OK


# ---------------------------------------------------------------- probe
def strace_run(strace: str, tdir: Path, argv: list, env: dict, children: Children, watch: str):
    tdir.mkdir(parents=True, exist_ok=True)
    full = [strace, "-ff", "-qq", "-o", str(tdir / "t"), "-e", f"trace={TRACE_SET}", "-s", "512", "--"] + argv
    p = children.spawn(full, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, cwd=env["HOME"])
    q = children.quiesce(30.0, watch)
    so, se = p.communicate()
    return p.returncode, so, se, q, parse_trace_dir(tdir)


def need_strace(a) -> str:
    s = shutil.which(a.strace) or (a.strace if os.access(a.strace, os.X_OK) else None)
    if not s:
        print(json.dumps({"status": "SKIP", "reason": f"strace not found ({a.strace}); kit never installs it"}))
        sys.exit(EXIT_NOTIMPLEMENTED)
    return s


def tracer_selfcheck(strace: str, work: Path) -> dict:
    """Positive control: prove this strace setup sees exec/open(O_CREAT)/rename/unlink."""
    home = private_home(work, "tracer-selfcheck")
    (home / "tmp").mkdir()
    ch = Children()
    rc, so, se, q, ev = strace_run(strace, work / "traces" / "tracer-selfcheck",
                                   ["/bin/sh", "-c", "mkdir d && : > d/a && mv d/a d/b && rm d/b && rmdir d"],
                                   sandbox_env(home), ch, str(home))
    c = classify(ev, home)
    seen = {m["syscall"] for m in c["mutations"]}
    got = {"exec": any(x["ok"] for x in c["execs"]),
           "open_create": any(m["syscall"].startswith("open") or m["syscall"] == "creat" for m in c["mutations"]),
           "rename": bool(seen & {"rename", "renameat", "renameat2"}),
           "unlink": bool(seen & {"unlink", "unlinkat"}),
           "multi_process_ff": len({x["pid"] for x in c["execs"]}) >= 2}
    return {"exit_code": rc, "seen": got, "pass": rc == 0 and all(got.values())}


def cmd_probe(a, man=None) -> tuple:
    strace = need_strace(a)
    man = man or json.loads(Path(a.manifest).read_text())
    work = Path(a.work).resolve()
    rows, fail = [], False
    sc = tracer_selfcheck(strace, work)
    rows.append({"sha": None, "role": "tracer-selfcheck", "arg": None, "pass": sc["pass"], **sc})
    fail |= not sc["pass"]
    for art in man["legacy"]:
        binp = Path(art["binary"])
        for arg in ("--version", "--help"):
            home = private_home(work, f"probe-{art['sha'][:8]}")
            (home / "tmp").mkdir()
            before, pre_hash = snapshot(home), sha256_file(binp)
            ch = Children()
            rc, so, se, q, ev = strace_run(strace, work / "traces" / f"probe-{art['sha'][:12]}{arg}", [str(binp), arg],
                                           sandbox_env(home), ch, str(home))
            c = classify(ev, home)
            diff = snapshot_diff(before, snapshot(home))
            ident = any(x["ok"] and x["path"] == str(binp) for x in c["execs"]) and pre_hash == sha256_file(binp) == art["binary_sha256"]
            stamp = arg != "--version" or art["version_stamp"] in so
            gids = group_id_hits([so, se])
            ok = rc == 0 and ident and stamp and not diff and not c["store_touches"] and gids == 0 \
                and not [m for m in c["mutations"] if m["path"].startswith(str(home))]
            fail |= not ok
            rows.append({"sha": art["sha"], "role": art["role"], "arg": arg, "exit_code": rc, "pass": ok,
                         "exec_identity": ident, "version_stamp_seen": stamp, "binary_sha256": art["binary_sha256"],
                         "execs": c["execs"], "mutations_anywhere": c["mutations"], "home_touches": c["home_touches"],
                         "store_touches": c["store_touches"], "snapshot_diff": diff, "group_id_hits": gids,
                         "quiesce": q, "stdout_first_line": so.splitlines()[0] if so else ""})
    return rows, fail


# ---------------------------------------------------------------- retire
STUB = """#!/bin/sh
# TEST STUB (not a launcher, not production): forwards ONLY read-only metadata
# probes to the fixed new binary; any other request is NOTIMPLEMENTED (exit 3).
case "$*" in
  --version|--help) exec "{new}" "$@" ;;
  *) echo "NOTIMPLEMENTED: launcher absent at this base; refusing '$*'" >&2; exit 3 ;;
esac
"""

SURFACES = {
    "path": ("export PATH=\"$KIT_HOME/legacy-bin:/usr/bin:/bin\"", "rm -f \"$KIT_HOME/legacy-bin/hyphae\"; hash -r"),
    "alias": ("shopt -s expand_aliases\nalias hyphae=\"$KIT_LEGACY_ABS\"", "unalias hyphae"),
    "function": ("hyphae() { \"$KIT_LEGACY_ABS\" \"$@\"; }", "unset -f hyphae"),
    "cache": ("hash -p \"$KIT_LEGACY_ABS\" hyphae", "hash -r"),
    "absolute": ("", ""),
}


def cmd_retire(a, man=None) -> tuple:
    strace = need_strace(a)
    man = man or json.loads(Path(a.manifest).read_text())
    if not man.get("forward_new"):
        raise SystemExit("retire needs a forward_new artifact (build --new-sha)")
    new = man["forward_new"]
    legacy_hashes = {x["binary_sha256"] for x in man["legacy"]}
    work = Path(a.work).resolve()
    rows, fail = [], False
    for art in man["legacy"]:
        for surf, (setup, retire) in SURFACES.items():
            for phase in ("pre", "post"):
                home = private_home(work, f"retire-{art['sha'][:8]}-{surf}-{phase}")
                (home / "tmp").mkdir()
                lb, inst = home / "legacy-bin", home / "opt/hyphae-legacy/bin"
                lb.mkdir(parents=True); inst.mkdir(parents=True)
                shutil.copy2(art["binary"], lb / "hyphae")
                shutil.copy2(art["binary"], inst / "hyphae")
                abs_old = inst / "hyphae"
                lines = ["set +e", setup]
                if phase == "post":
                    if surf == "absolute":
                        abs_old.unlink()
                        abs_old.write_text(STUB.format(new=new["binary"]))
                        abs_old.chmod(0o700)  # file created by this kit inside its own temp HOME
                    lines.append(retire)
                    if surf in ("alias", "function", "cache"):  # retirement also removes the copies
                        lines.append("rm -f \"$KIT_HOME/legacy-bin/hyphae\"")
                lines.append("{ type hyphae; hash -t hyphae; } 2>&1 | sed 's#^#TYPE: #'")
                lines.append("\"$KIT_LEGACY_ABS\" --version" if surf == "absolute" else "hyphae --version")
                lines.append("echo RC=$?")
                if phase == "post" and surf == "absolute":
                    lines.append("\"$KIT_LEGACY_ABS\" --legacy-version; echo LEGACY_REQ_RC=$?")
                script = home / "exercise.sh"
                script.write_text("\n".join(lines) + "\n")
                env = sandbox_env(home)
                env.update(KIT_HOME=str(home), KIT_LEGACY_ABS=str(abs_old))
                before = snapshot(home)
                ch = Children()
                rc, so, se, q, ev = strace_run(strace, work / "traces" / f"retire-{art['sha'][:12]}-{surf}-{phase}",
                                               ["/bin/bash", "--noprofile", "--norc", str(script)], env, ch, str(home))
                c = classify(ev, home)
                ok_execs = [x["path"] for x in c["execs"] if x["ok"]]
                exec_hashes = {p: sha256_file(Path(p)) for p in ok_execs if Path(p).is_file()}
                legacy_exec = [p for p, h in exec_hashes.items() if h in legacy_hashes]
                new_exec = [p for p, h in exec_hashes.items() if h == new["binary_sha256"]]
                m = re.search(r"RC=(\d+)", so)
                inner = int(m.group(1)) if m else None
                lm = re.search(r"LEGACY_REQ_RC=(\d+)", so)
                gids = group_id_hits([so, se])
                diff = [d for d in snapshot_diff(before, snapshot(home)) if d["path"] != "legacy-bin/hyphae"]
                if phase == "pre":
                    ok = bool(legacy_exec) and art["version_stamp"] in so and inner == 0
                elif surf == "absolute":
                    ok = not legacy_exec and bool(new_exec) and new["version_stamp"] in so and lm and lm.group(1) == "3"
                else:
                    ok = not legacy_exec and inner not in (0, None)
                ok = ok and not c["store_touches"] and gids == 0 and not diff
                fail |= not ok
                rows.append({"sha": art["sha"], "surface": surf, "phase": phase, "pass": ok, "shell_exit": rc,
                             "inner_rc": inner, "legacy_request_rc": lm and int(lm.group(1)),
                             "legacy_execs": legacy_exec, "new_execs": new_exec, "execs": c["execs"],
                             "resolution": [l[6:] for l in so.splitlines() if l.startswith("TYPE: ")],
                             "mutations": c["mutations"],
                             "store_touches": c["store_touches"], "snapshot_diff": diff, "group_id_hits": gids})
    return rows, fail


# ---------------------------------------------------------------- harness skeleton
SEQUENCE = ("close", "quiesce-1", "oneway-marker", "quiesce-2", "maintenance")


def launcher_status(launcher: str | None) -> tuple:
    if not launcher:
        return False, "no --launcher-bin given; no launcher/gate exists at base 9230cce (passive validator only)"
    p = Path(launcher)
    if not (p.is_file() and os.access(p, os.X_OK)):
        return False, f"launcher not executable: {launcher}"
    return False, "launcher protocol (close/marker/maintenance) is not specified by an accepted F3; kit refuses to guess"


def cmd_harness(a) -> tuple:
    for flag, v in (("--hyphae-source-sha", a.hyphae_source_sha), ("--launcher-source-sha", a.launcher_source_sha)):
        if v and not re.fullmatch(r"[0-9a-f]{40}", v):
            raise SystemExit(f"{flag} must be a full 40-hex SHA")
    work = Path(a.work).resolve()
    home = private_home(work, "harness")
    params = {"hyphae_bin": a.hyphae_bin and Path(a.hyphae_bin).name,
              "hyphae_bin_sha256": a.hyphae_bin and Path(a.hyphae_bin).is_file() and sha256_file(Path(a.hyphae_bin)),
              "hyphae_source_sha": a.hyphae_source_sha, "launcher_bin": a.launcher_bin and Path(a.launcher_bin).name,
              "launcher_source_sha": a.launcher_source_sha}
    impl, why = launcher_status(a.launcher_bin)
    steps = []
    # Self-test of the quiescence primitive on a harness-owned child only
    # (synthetic: a /bin/sh child holding a file open in the private HOME).
    ch = Children()
    probe_file = home / "kit-quiesce-selftest.txt"
    probe_file.write_text("x\n")
    ch.spawn(["/bin/sh", "-c", f"exec 3<'{probe_file}'; sleep 0.3"], env=sandbox_env(home))
    selftest = ch.quiesce(10.0, str(home))
    selftest_ok = selftest["fd_hits_under_watch"] == [str(probe_file)] and not selftest["alive_after_wait"]
    for s in SEQUENCE:
        if not impl:
            steps.append({"step": s, "status": "NOTIMPLEMENTED", "reason": why if s == "close" else "blocked by close"})
            continue
    res = {"params": params, "private_home_mode": oct(stat.S_IMODE(home.stat().st_mode)),
           "quiesce_selftest": {"pass": selftest_ok, **{k: v for k, v in selftest.items() if k != "fd_hits_under_watch"},
                                "fd_hit_count": len(selftest["fd_hits_under_watch"])},
           "sequence": steps,
           "marker_semantics": "one-way managed marker is NON-activating and is NOT old-writer exclusion (rev7 3.6.1)"}
    return res, not selftest_ok, not impl


WINDOWS = [
    {"id": "W-activation", "when": "at activation", "fixture": "legacy-165-group-nil-relays (impl plan section 4)"},
    {"id": "W-O3-before-T4", "when": "O3 JSON committed, T4 not committed (incl. after SIGKILL)", "fixture": "P-O3, P-BUDGET-9/10 (witness schema section 6)"},
    {"id": "W-D6-before-T2prime", "when": "D6 committed, T2' not committed (incl. after SIGKILL)", "fixture": "P-MISSING, P-EXHAUSTED-PENDING, P-EXHAUSTED-FAILED"},
]


def cmd_windows(_a) -> dict:
    return {"status": "NOTIMPLEMENTED", "reason": "requires accepted F3 launcher/gate and F3/F4 O3/D6 writers; old programs are never opened against these fixtures",
            "required_evidence_per_window": ["per registered entry: old path absent / launcher refusal code / stub forwards new only",
                                             "binary + source identity trace (execve path, sha256, source SHA)",
                                             "strace -ff: no old process opens live stores",
                                             "JSON bytes + SQLite logical snapshot diff empty before/after refusal",
                                             "zero group ids in all messages"],
            "windows": WINDOWS}


# ---------------------------------------------------------------- all
def sanitize(obj, work: Path, repo: Path):
    s = json.dumps(obj)
    for p, rep in ((str(work), "$WORK"), (str(repo), "$REPO"), (os.environ.get("HOME", "\0"), "$REALHOME")):
        if p and p != "\0":
            s = s.replace(p, rep)
    return json.loads(s)


def cmd_all(a) -> int:
    work = Path(a.work).resolve()
    work.mkdir(parents=True, exist_ok=True)
    a.out = str(work / "artifacts")
    cmd_build(a)
    man = json.loads((work / "artifacts/legacy-artifacts.json").read_text())
    probe, pf = cmd_probe(a, man)
    retire, rf = cmd_retire(a, man)
    a.hyphae_bin = man["forward_new"]["binary"] if man.get("forward_new") else None
    a.hyphae_source_sha = a.new_sha
    harness, hf, hni = cmd_harness(a)
    win = cmd_windows(a)
    summary = {"schema": "v7-linux-kit/summary-v1", "not_v7_closure": True, "runtime_activation": False,
               "host": {"uname": " ".join(os.uname()),
                        "go_build_toolchain": run(["go", "version"], cwd=a.repo).stdout.strip(),
                        "go_default_outside_module": run(["go", "version"], cwd="/", env=dict(os.environ, GOTOOLCHAIN="local")).stdout.strip(),
                        "strace": run([need_strace(a), "-V"]).stdout.splitlines()[0]},
               "artifacts": man, "probe": probe, "retire": retire, "harness": harness, "windows": win,
               "totals": {"probe_pass": sum(r["pass"] for r in probe), "probe_rows": len(probe),
                          "retire_pass": sum(r["pass"] for r in retire), "retire_rows": len(retire),
                          "notimplemented": ["harness.close..maintenance", "windows.*"]}}
    (work / "summary.json").write_text(json.dumps(sanitize(summary, work, Path(a.repo).resolve()), indent=2) + "\n")
    print(json.dumps(summary["totals"]))
    if pf or rf or hf:
        return EXIT_FAIL
    return EXIT_NOTIMPLEMENTED  # launcher + windows are NOTIMPLEMENTED: never green


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = ap.add_subparsers(dest="cmd", required=True)

    def common(p, build=False):
        p.add_argument("--work", default="./v7kit-work")
        p.add_argument("--strace", default=os.environ.get("STRACE", "strace"))
        if build:
            p.add_argument("--repo", default=".")
            p.add_argument("--sha", action="append", help="frozen SHA (default: all three)")
            p.add_argument("--new-sha", help="forward-target SHA for the new binary (e.g. the base under test)")

    b = sub.add_parser("build"); common(b, True); b.add_argument("--out", required=True)
    for n in ("probe", "retire"):
        p = sub.add_parser(n); common(p); p.add_argument("--manifest", required=True)
    h = sub.add_parser("harness"); common(h)
    al = sub.add_parser("all"); common(al, True)
    for p in (h, al):
        p.add_argument("--hyphae-bin"); p.add_argument("--hyphae-source-sha")
        p.add_argument("--launcher-bin"); p.add_argument("--launcher-source-sha")
    sub.add_parser("windows")
    a = ap.parse_args(argv)
    if a.cmd == "build":
        return cmd_build(a)
    if a.cmd in ("probe", "retire"):
        rows, f = (cmd_probe if a.cmd == "probe" else cmd_retire)(a)
        print(json.dumps(rows, indent=2))
        return EXIT_FAIL if f else EXIT_OK
    if a.cmd == "harness":
        res, f, ni = cmd_harness(a)
        print(json.dumps(res, indent=2))
        return EXIT_FAIL if f else (EXIT_NOTIMPLEMENTED if ni else EXIT_OK)
    if a.cmd == "windows":
        print(json.dumps(cmd_windows(a), indent=2))
        return EXIT_NOTIMPLEMENTED
    return cmd_all(a)


if __name__ == "__main__":
    sys.exit(main())
