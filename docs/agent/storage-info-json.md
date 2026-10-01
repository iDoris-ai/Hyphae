# `storage info` JSON contract

`storage info` supports the usual JSON selectors: `--json`, `HYPHAE_OUTPUT=json`, and the legacy `AGENT_SPEAKER_OUTPUT=json` alias. Success returns one `{"ok":true,"data":...}` envelope with `path`, `exists`, `size_bytes`, `mode`, `message_count`, and `tables` fields. `tables` is sorted by name.

The command inspects `~/.hyphae/messages.db` without creating the home or storage directory. If the database is absent, it returns `exists:false`, zero size and message count, an empty mode, and an empty table list. Existing database paths must be regular files. The command opens an existing database in SQLite read-only mode and reports an error for corrupt databases or schemas without the `messages` table; it does not run migrations or modify the schema.

Read-only SQLite access avoids database writes, but a live WAL database may require SQLite to manage its shared-memory sidecar. The command does not claim that inspecting an actively used WAL database leaves every filesystem sidecar untouched.

Human-readable output retains the existing storage path, byte size, mode, message count, and table listing.
