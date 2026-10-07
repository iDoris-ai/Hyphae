# Go acceptance: kind 30078 classification and outbox concurrency

## Changes and compatibility

The shared classifier in `internal/wireevent` now separates profile and agent
message events without changing kind `30078`. Profile events require exactly
`c=profile` plus `d=agent-profile`. Messages require exactly `c=agent`,
`v=v1`, and one valid 32-byte hex `p`. CLI messages retain their existing
random-plus-content 16-hex unique token; TUI messages now use that generator.
Daemon auto-replies retain their existing random 32-hex token and retry
behavior. All three write paths use the shared `agent-message:` namespace
formatter.

Read compatibility remains for legacy signed messages with no `d`, a 16-hex
`d`, or a 32-hex `d`; the message c/v/p discriminator is mandatory for every
form. Profile/message conflicts, duplicate discriminator tags, malformed
tags, and unknown namespaces are rejected. Message queries filter by kind,
c, v, and recipient p but omit d so old no-d events remain discoverable;
returned events are classified and signature/recipient-checked locally.
Historical signed events, IDs, and outbox retry payloads are not rewritten.

The existing outbox implementation was not reworked: production read-modify-
write mutation paths use locked `UpdateOutbox`; `SaveOutbox` is a locked,
atomic whole-file replacement and must not be used with a stale loaded
snapshot. A new subprocess regression models an entry added while a clear
confirmation is outstanding and verifies that clear removes only the
confirmed old entry, preserves the later addition, and leaves valid JSON.

## Verification

Commands run from the repository root:

```text
go test -race ./internal/wireevent ./internal/messaging ./internal/profile ./internal/daemon ./internal/tui
go test -race ./internal/messaging -run 'TestOutboxUpdatesAcrossProcesses|TestOutboxClearSnapshotPreservesEntryAddedByAnotherProcess' -count=1
go test -tags integration ./tests
```

The process concurrency regression `TestOutboxUpdatesAcrossProcesses` starts
six independent child test processes concurrently, then asserts all six
entries and unique queue IDs survive. The additional clear regression starts
one independent child process between snapshot and clear. All commands passed:
the targeted race suite passed all five packages, the isolated two-test outbox
process regression passed under the race detector, and the integration suite
completed in 19.661 seconds.
