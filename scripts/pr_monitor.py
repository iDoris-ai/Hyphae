#!/usr/bin/env python3
"""Periodic, single-instance GitHub PR monitor for a Codex queue thread."""

import argparse
import fcntl
import json
import logging
from logging.handlers import RotatingFileHandler
import os
from pathlib import Path
import re
import sqlite3
import subprocess
import sys
from datetime import datetime, timezone

PR_FIELDS = "number,title,url,author,headRefOid,headRefName,baseRefName,isDraft,reviewDecision,mergeStateStatus,statusCheckRollup"
RUN_FIELDS = "status,conclusion,headSha,url,workflowName"
MAIN_WORKFLOW = "ci.yml"
MAIN_WORKFLOW_NAME = "CI"


def utc_now():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def atomic_json(path, value):
    path = Path(path)
    tmp = path.with_name(path.name + ".tmp")
    with tmp.open("w", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(tmp, path)


def make_logger(state_dir):
    logger = logging.getLogger("pr_monitor")
    logger.setLevel(logging.INFO)
    for old_handler in logger.handlers[:]:
        logger.removeHandler(old_handler)
        old_handler.close()
    handler = RotatingFileHandler(Path(state_dir) / "monitor.log", maxBytes=1024 * 1024, backupCount=1)
    handler.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(message)s"))
    logger.addHandler(handler)
    return logger


def run_command(args, repo_dir):
    return subprocess.run(args, capture_output=True, text=True, timeout=60, check=False, shell=False, cwd=repo_dir)


def gh_json(args, repo_dir):
    result = run_command(args, repo_dir)
    if result.returncode:
        raise RuntimeError(f"command failed ({result.returncode}): {result.stderr.strip()}")
    return json.loads(result.stdout)


def queued_count(db_path, thread_id):
    uri = Path(db_path).resolve().as_uri() + "?mode=ro"
    conn = sqlite3.connect(uri, uri=True)
    try:
        row = conn.execute("SELECT count(*) FROM queued_items WHERE thread_id=?", (thread_id,)).fetchone()
        return int(row[0])
    finally:
        conn.close()


def scan(config, scanned_at):
    gh = config["gh_bin"]
    branch = gh_json([gh, "api", f"repos/{config['repo_slug']}/branches/main"], config["repo_dir"])
    commit = branch.get("commit") if isinstance(branch, dict) else None
    main_sha = commit.get("sha") if isinstance(commit, dict) else None
    if not isinstance(main_sha, str) or not re.fullmatch(r"[0-9a-fA-F]{40}", main_sha):
        raise RuntimeError("GitHub main branch response did not contain a valid 40-character commit SHA")
    main_sha = main_sha.lower()
    prs = gh_json([gh, "pr", "list", "--repo", config["repo_slug"], "--state", "open", "--limit", "100", "--json", PR_FIELDS], config["repo_dir"])
    runs = gh_json([
        gh, "run", "list", "--repo", config["repo_slug"], "--branch", "main",
        "--workflow", MAIN_WORKFLOW, "--commit", main_sha, "--limit", "5", "--json", RUN_FIELDS,
    ], config["repo_dir"])
    if not isinstance(runs, list):
        raise RuntimeError("GitHub main workflow query did not return a run list")
    # `--workflow ci.yml` and the workflowName check identify this repository's
    # current CI workflow; headSha prevents any stale main run from being used.
    main_runs = [run for run in runs if isinstance(run, dict)
                 and str(run.get("headSha", "")).lower() == main_sha
                 and run.get("workflowName") == MAIN_WORKFLOW_NAME]
    snapshot = {
        "scanned_at_utc": scanned_at,
        "repo_slug": config["repo_slug"],
        "pull_requests": prs,
        "main_sha": main_sha,
        "main_workflow": MAIN_WORKFLOW,
        "main_runs": main_runs,
        "main_ci_missing": not main_runs,
    }
    latest = Path(config["state_dir"]) / "latest.json"
    atomic_json(latest, snapshot)
    return latest


def execute(config, scan_only=False):
    os.umask(0o077)
    state_dir = Path(config["state_dir"])
    state_dir.mkdir(parents=True, exist_ok=True)
    logger = make_logger(state_dir)
    lock_stream = (state_dir / "monitor.lock").open("a+")
    try:
        fcntl.flock(lock_stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        logger.error("another monitor instance holds the lock")
        lock_stream.close()
        for handler in logger.handlers[:]:
            logger.removeHandler(handler)
            handler.close()
        return 1

    scanned_at = utc_now()
    status = {"last_scan_utc": scanned_at, "action": "error", "error": None, "queued_count": None}
    try:
        latest = scan(config, scanned_at)
        count = queued_count(config["queue_db"], config["thread_id"])
        status["queued_count"] = count
        if scan_only:
            status["action"] = "scan_only"
        elif count:
            status["action"] = "already_queued"
        else:
            prompt = Path(config["prompt_file"]).read_text(encoding="utf-8")
            message = f"{prompt.rstrip()}\n\nLatest PR monitor snapshot: {latest}\nScan time (UTC): {scanned_at}"
            result = subprocess.run(
                [config["codex_bin"], "queue", "--thread", config["thread_id"], "--message", message],
                capture_output=True, text=True, timeout=45, check=False, shell=False, cwd=config["repo_dir"],
            )
            if result.returncode:
                raise RuntimeError(f"codex queue failed ({result.returncode}): {result.stderr.strip()}")
            status["action"] = "queued"
        logger.info("scan complete action=%s queued_count=%s", status["action"], count)
    except Exception as exc:  # Fail closed; never enqueue if scanning or queue inspection failed.
        status["error"] = str(exc)
        logger.exception("monitor failed")
    finally:
        atomic_json(state_dir / "status.json", status)
        lock_stream.close()
        for handler in logger.handlers[:]:
            logger.removeHandler(handler)
            handler.close()
    return 1 if status["error"] else 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True, help="path to local JSON config")
    parser.add_argument("--scan-only", action="store_true", help="scan and record state without queueing")
    args = parser.parse_args(argv)
    try:
        config = json.loads(Path(args.config).read_text(encoding="utf-8"))
        return execute(config, args.scan_only)
    except Exception as exc:
        print(f"pr_monitor: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
