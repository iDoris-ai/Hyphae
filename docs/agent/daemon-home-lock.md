# Daemon single-instance lock

Hyphae runs at most one daemon per `HOME`. Identities in one keystore share the same SQLite history and outbox, so selecting different identities does not permit another daemon under that home.

On Linux and macOS, `daemon` holds an exclusive non-blocking `flock` on `~/.hyphae/daemon.lock` from after interval validation until the command returns. The keystore directory is mode `0700`; the stable lock file is mode `0600`. The lock file is never removed or replaced, so contenders always lock the same inode. The kernel releases the lock when the descriptor closes or the process exits, including `SIGKILL`.

A second daemon for that home fails with `write_conflict` and exit code 5 before keystore unlock, stdin reads, relay work, notifications, or auto-replies. Lock setup I/O errors use the ordinary error path. Other operating systems report that daemon locking is unsupported. Different homes can run independently.

The lock does not report whether a daemon is healthy or whether its last relay scan completed. PID files are not used as lock authority.
