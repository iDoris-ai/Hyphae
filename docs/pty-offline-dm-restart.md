# Offline DM restart PTY fixture

`tests/e2e/pty_offline_dm_restart.py` is a local end-to-end fixture. It speaks
only the nostr frames the TUI already uses, on a loopback port, and it drives
`hyphae tui chat` with a fresh temporary HOME. It is not a production command,
and nothing in the CLI or daemon imports it.

From the repository root:

```sh
python3 tests/e2e/test_pty_offline_dm_restart.py
python3 tests/e2e/pty_offline_dm_restart.py --reps 1 --receipt <receipt.json>
python3 tests/e2e/pty_offline_dm_restart.py --reps 1 --drop-recipient --receipt <receipt.json>
```

Put `<receipt.json>` outside the real HOME. The first command is the unit
suite. The second exits 0 only when Bob's TUI shows the decrypted plaintext
once. The third still ACKs Alice, withholds the event from Bob, and must exit
non-zero. No daemon is started. Group runtime stays unproven.
