"""Run the workflow's real candidate and publish scripts against temporary Git remotes."""

from pathlib import Path
import os
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
UPDATER = ROOT / ".github/workflows/nostr-upstream.yml"


def run(cwd: Path, *args: str, check: bool = True, env=None) -> subprocess.CompletedProcess:
    result = subprocess.run(args, cwd=cwd, text=True, stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT, env=env)
    if check and result.returncode:
        raise AssertionError(f"{args!r} failed:\n{result.stdout}")
    return result


def git(repo: Path, *args: str, check: bool = True) -> str:
    return run(repo, "git", *args, check=check).stdout.strip()


def extract_run(step_name: str) -> str:
    lines = UPDATER.read_text().splitlines()
    marker = f"      - name: {step_name}"
    start = lines.index(marker)
    run_line = next(i for i in range(start + 1, len(lines)) if lines[i] == "        run: |" or lines[i].startswith("        run: "))
    if lines[run_line] != "        run: |":
        raise AssertionError(f"step {step_name!r} no longer uses a shell block")
    block = []
    for line in lines[run_line + 1:]:
        if line.strip() and len(line) - len(line.lstrip()) < 10:
            break
        block.append(line[10:] if line.startswith("          ") else "")
    return "\n".join(block) + "\n"


CANDIDATE_SCRIPT = extract_run("Resolve, update, and validate Nostr")
PUBLISH_SCRIPT = extract_run("Verify and push tested commit")


def write(repo: Path, path: str, content: str) -> None:
    target = repo / path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(content)


def commit(repo: Path, message: str, *paths: str) -> str:
    git(repo, "add", *paths)
    git(repo, "commit", "-m", message)
    return git(repo, "rev-parse", "HEAD")


def outputs(path: Path) -> dict[str, str]:
    return dict(line.split("=", 1) for line in path.read_text().splitlines() if "=" in line)


def make_go_stub(folder: Path) -> None:
    folder.mkdir()
    stub = folder / "go"
    stub.write_text("""#!/usr/bin/env bash
set -Eeuo pipefail
case "$1" in
  list)
    printf '{\\n  "Path": "fiatjaf.com/nostr",\\n  "Version": "%s"\\n}\\n' "$TEST_NOSTR_VERSION"
    ;;
  get)
    version="${2#*@}"
    sed -E "s|(fiatjaf.com/nostr )[^ ]+|\\1$version|" go.mod > go.mod.tmp
    mv go.mod.tmp go.mod
    printf 'sum %s\\n' "$version" > go.sum
    ;;
  mod)
    exit 0
    ;;
  test)
    if [[ "${FAIL_GO_TEST:-}" == 1 ]]; then exit 23; fi
    exit 0
    ;;
  build)
    if [[ "$2" == -o ]]; then touch "$3"; fi
    ;;
  *) echo "unexpected go command: $*" >&2; exit 2 ;;
esac
""")
    stub.chmod(0o755)


def candidate_run(repo: Path, run_temp: Path, stub_dir: Path, version: str, label: str,
                  expect_failure: bool = False, fail_go_test: bool = False):
    run_temp.mkdir(parents=True, exist_ok=True)
    output, summary = run_temp / "output", run_temp / "summary"
    output.touch()
    summary.touch()
    home = run_temp / "home"
    home.mkdir(exist_ok=True)
    env = os.environ.copy()
    env.update({
        "BASE_REF": "main", "BOT_BRANCH": "automation/nostr-upstream",
        "RUNNER_TEMP": str(run_temp), "GITHUB_OUTPUT": str(output),
        "GITHUB_STEP_SUMMARY": str(summary), "HOME": str(home),
        "TEST_NOSTR_VERSION": version,
        "FAIL_GO_TEST": "1" if fail_go_test else "0",
        "PATH": f"{stub_dir}{os.pathsep}{env['PATH']}",
    })
    result = run(repo, "bash", "--noprofile", "--norc", "-c", CANDIDATE_SCRIPT, check=False, env=env)
    if result.returncode and not expect_failure:
        raise AssertionError(f"candidate run {label} failed:\n{result.stdout}\n{summary.read_text()}")
    if expect_failure and result.returncode == 0:
        raise AssertionError(f"candidate run {label} unexpectedly succeeded")
    return outputs(output), summary.read_text(), run_temp


def publish_run(repo: Path, run_temp: Path, data: dict[str, str], label: str):
    artifact_dir = run_temp / "candidate"
    artifact_dir.mkdir()
    shutil.copy2(run_temp / "nostr-candidate.bundle", artifact_dir / "nostr-candidate.bundle")
    output, summary = run_temp / "publish-output", run_temp / "publish-summary"
    output.touch()
    summary.touch()
    env = os.environ.copy()
    env.update({
        "BASE_REF": "main", "BOT_BRANCH": "automation/nostr-upstream",
        "GH_TOKEN": "test-token", "RUNNER_TEMP": str(run_temp),
        "GITHUB_OUTPUT": str(output), "GITHUB_STEP_SUMMARY": str(summary),
        "EXPECTED_BASE": data["base_sha"], "EXPECTED_BRANCH": data["branch_sha"],
        "EXPECTED_TREE": data["tree_sha"], "CANDIDATE_COMMIT": data["commit_sha"],
        "CANDIDATE_VERSION": data["candidate"],
    })
    result = run(repo, "bash", "--noprofile", "--norc", "-c", PUBLISH_SCRIPT, check=False, env=env)
    return result, summary.read_text()


def clone(remote: Path, path: Path) -> Path:
    run(path.parent, "git", "clone", str(remote), str(path))
    return path


def main() -> None:
    with tempfile.TemporaryDirectory(prefix="hyphae-upstream-workflow-") as tmp:
        root = Path(tmp)
        remote, seed = root / "remote.git", root / "seed"
        run(root, "git", "init", "--bare", "--initial-branch=main", str(remote))
        clone(remote, seed)
        git(seed, "config", "user.name", "Workflow Integration Test")
        git(seed, "config", "user.email", "workflow-test@example.invalid")
        write(seed, "go.mod", "module example.invalid/hyphae\n\nrequire fiatjaf.com/nostr v0.0.0-old\n")
        write(seed, "go.sum", "old sum\n")
        write(seed, "app.txt", "baseline\n")
        git(seed, "add", ".")
        git(seed, "commit", "-m", "baseline")
        git(seed, "push", "-u", "origin", "main")
        base0 = git(seed, "rev-parse", "HEAD")
        stub_dir = root / "stub-bin"
        make_go_stub(stub_dir)

        # First candidate: resolve the version, validate the exact tree, bundle and publish it.
        first = clone(remote, root / "candidate-first")
        first_data, _, first_temp = candidate_run(first, root / "first-run", stub_dir, "v0.0.0-new", "first publication")
        assert first_data["changed"] == "true"
        assert first_data["candidate"] == "v0.0.0-new"
        assert (first_temp / "nostr-candidate.bundle").is_file()
        publisher = clone(remote, root / "publisher")
        result, summary = publish_run(publisher, first_temp, first_data, "first publication")
        assert result.returncode == 0, f"first publish failed:\n{result.stdout}\n{summary}"
        first_head = git(publisher, "ls-remote", "origin", "refs/heads/automation/nostr-upstream").split()[0]
        assert first_head == first_data["commit_sha"]
        assert git(publisher, "rev-parse", f"{first_head}^{{tree}}") == first_data["tree_sha"]
        print("PASS: actual workflow blocks resolve, bundle, verify tree and publish first branch")

        failing = clone(remote, root / "candidate-failing-test")
        failed_data, failed_summary, failed_temp = candidate_run(
            failing, root / "failed-test-run", stub_dir, "v0.0.0-test-failure",
            "failed validation", expect_failure=True, fail_go_test=True,
        )
        assert failed_data.get("changed") != "true"
        assert not (failed_temp / "nostr-candidate.bundle").exists()
        assert "go test ./..." in failed_summary
        print("PASS: a failed Go test keeps the candidate unpublished with a failure summary")

        # Main adds source while the open PR has the same dependency. The real candidate block
        # must test the merge result and publish a refresh based on the new main commit.
        write(seed, "internal/new.go", "package internal\n")
        main_v2 = commit(seed, "main adds source", "internal/new.go")
        git(seed, "push", "origin", f"{main_v2}:refs/heads/main")
        stale_candidate = clone(remote, root / "candidate-refresh")
        refresh_data, _, refresh_temp = candidate_run(stale_candidate, root / "refresh-run", stub_dir, "v0.0.0-new", "refresh after main advances")
        assert refresh_data["changed"] == "true"
        assert refresh_data["base_sha"] == main_v2
        refresh_publish, refresh_summary = publish_run(publisher, refresh_temp, refresh_data, "refresh after main advances")
        assert refresh_publish.returncode == 0, f"refresh publish failed:\n{refresh_publish.stdout}\n{refresh_summary}"
        refreshed = git(publisher, "ls-remote", "origin", "refs/heads/automation/nostr-upstream").split()[0]
        assert refreshed == refresh_data["commit_sha"]
        assert "internal/new.go" in git(publisher, "ls-tree", "-r", "--name-only", refreshed).splitlines()
        assert git(publisher, "diff", "--quiet", main_v2, refreshed, "--", ".", ":!go.mod", ":!go.sum") == ""
        print("PASS: actual candidate block refreshes stale same-version PR over new main source")

        # Once the candidate is in main but its remote branch remains, the schedule must be a no-op.
        git(seed, "fetch", "origin", "+refs/heads/automation/nostr-upstream:refs/remotes/origin/automation/nostr-upstream")
        git(seed, "switch", "main")
        git(seed, "merge", "--ff-only", "refs/remotes/origin/automation/nostr-upstream")
        merged_main = git(seed, "rev-parse", "HEAD")
        git(seed, "push", "origin", f"{merged_main}:refs/heads/main")
        merged_candidate = clone(remote, root / "candidate-merged")
        merged_data, _, _ = candidate_run(merged_candidate, root / "merged-run", stub_dir, "v0.0.0-new", "already merged")
        assert merged_data["changed"] == "false"
        print("PASS: actual candidate block skips an unchanged bot branch already merged into main")

        # A branch change between validation and publish is detected by SHA before pushing.
        race_candidate = clone(remote, root / "candidate-race")
        race_data, _, race_temp = candidate_run(race_candidate, root / "race-run", stub_dir, "v0.0.0-newer", "prepare branch race")
        assert race_data["changed"] == "true"
        human = clone(remote, root / "human-race")
        git(human, "config", "user.name", "Human")
        git(human, "config", "user.email", "human@example.invalid")
        git(human, "fetch", "origin", "+refs/heads/automation/nostr-upstream:refs/remotes/origin/automation/nostr-upstream")
        git(human, "switch", "-c", "human-race", "refs/remotes/origin/automation/nostr-upstream")
        write(human, "go.sum", "human edit\n")
        human_head = commit(human, "manual manifest edit", "go.sum")
        git(human, "push", "origin", f"{human_head}:refs/heads/automation/nostr-upstream")
        race_publish, race_summary = publish_run(publisher, race_temp, race_data, "branch SHA race")
        assert race_publish.returncode != 0
        assert "Bot branch changed after validation" in race_publish.stdout + race_summary
        print("PASS: actual publish block rejects a changed bot branch SHA")

        # A default-branch change during validation stops publish before it pushes.
        base_race = clone(remote, root / "candidate-base-race")
        base_data, _, base_temp = candidate_run(base_race, root / "base-run", stub_dir, "v0.0.0-later", "prepare base race")
        assert base_data["changed"] == "true"
        write(seed, "main-later.txt", "main moved\n")
        main_v3 = commit(seed, "main advances again", "main-later.txt")
        git(seed, "push", "origin", f"{main_v3}:refs/heads/main")
        base_publish, base_summary = publish_run(publisher, base_temp, base_data, "default branch SHA race")
        assert base_publish.returncode != 0
        assert "Default branch moved after validation" in base_publish.stdout + base_summary
        print("PASS: actual publish block rejects a changed default branch SHA")

        # A human source edit on the bot branch is rejected before publishing.
        write(human, "internal/manual.go", "package internal\n")
        manual_head = commit(human, "manual code edit", "internal/manual.go")
        git(human, "push", "origin", f"{manual_head}:refs/heads/automation/nostr-upstream")
        tampered = clone(remote, root / "candidate-tampered")
        tamper_data, tamper_summary, _ = candidate_run(tampered, root / "tamper-run", stub_dir, "v0.0.0-later", "manual source edit", expect_failure=True)
        assert not tamper_data.get("changed")
        assert "Existing bot branch contains an unexpected path" in tamper_summary
        print("PASS: actual candidate block rejects manual source edits on the bot branch")


if __name__ == "__main__":
    main()
