# Local PR monitor

`scripts/pr_monitor.py` scans open pull requests and recent CI runs, writes a local snapshot, and queues a review request on one existing Codex thread. It uses Python 3 standard-library modules only. It never merges PRs or starts a separate Codex session.

## Local configuration

Keep the JSON config, prompt, queue database, and state directory outside the repository. The config has these keys:

```json
{
  "repo_slug": "OWNER/REPOSITORY",
  "repo_dir": "/absolute/path/to/checkout",
  "thread_id": "LOCAL_CODEX_THREAD_ID",
  "codex_bin": "/absolute/path/to/codex",
  "gh_bin": "/absolute/path/to/gh",
  "queue_db": "/absolute/path/to/queue.sqlite",
  "state_dir": "/absolute/path/to/pr-monitor-state",
  "prompt_file": "/absolute/path/to/pr-monitor-prompt.txt"
}
```

Use absolute executable and file paths because launchd has a minimal environment. The monitor reads `queued_items` from the configured SQLite database through a read-only connection. If the database or expected table is unavailable, it records an error and does not queue work. A nonzero queue count also suppresses another request.

Run a harmless scan first:

```sh
python3 /absolute/path/to/Hyphae/scripts/pr_monitor.py --config /absolute/path/to/config.json --scan-only
```

Then run without `--scan-only` to enable queueing. Each run writes `latest.json`, `status.json`, `monitor.log`, and a lock file under `state_dir`. The JSON files are replaced atomically. The log is limited to 1 MiB with one rotated copy. Files created by the process are private to the current user.

## launchd

Install a per-user LaunchAgent with `StartInterval` set to `1200` seconds (20 minutes). Its program arguments should invoke the absolute Python 3 path, this script, `--config`, and the absolute config path. Set the working directory to the checkout if desired; the monitor itself does not depend on the shell's current directory. Keep the plist and local config out of Git.

launchd runs only while the Mac is awake and the user agent is loaded. Missed intervals during sleep or shutdown are not replayed. `gh` and Codex must remain logged in, and the Codex CLI must support `codex queue --thread ... --message ...`.

To stop monitoring, unload the LaunchAgent with `launchctl bootout gui/$(id -u) /absolute/path/to/launch-agent.plist`, then remove the local plist if no longer needed. The exact installed path depends on the local setup.
