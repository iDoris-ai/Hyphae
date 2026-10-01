# `identity check-password`

`hyphae identity check-password --password-stdin` verifies a password against
the encrypted keystore's stored verification token. The flag is required;
without it, the command returns `user_error` (exit 1) before reading stdin.
The command does not prompt or accept a password argument.

On success, JSON mode prints only:

```json
{"ok":true,"data":{"encrypted":true,"valid":true}}
```

Human mode prints `Keystore password is valid`. Neither mode prints identity,
key, or password fields. `--json`, `HYPHAE_OUTPUT=json`, and the legacy
`AGENT_SPEAKER_OUTPUT=json` select JSON output.

The command reads `~/.hyphae/keystore.json` as a regular read-only file. It
requires an existing encrypted keystore. Missing/plaintext stores, empty or
oversized stdin, and an incorrect password return `auth_error` (exit 3).
Filesystem and JSON errors, or malformed salt/token encodings, return
`other_error` (exit 4). Input uses the same 4096-byte raw limit as other
password-stdin commands, removes one trailing LF or CRLF, and preserves other
whitespace.

Verification accepts both current and legacy tokens but never upgrades the
legacy token. The command does not create directories or files, write audit
history, contact a relay, or call keystore unlock/save APIs. It only confirms
that the supplied password matches the verifier in the keystore snapshot it
read; it does not unlock a daemon or verify decryption of every stored
identity. A password rotation can happen immediately after the check, so a
later operation must perform its own password verification.
