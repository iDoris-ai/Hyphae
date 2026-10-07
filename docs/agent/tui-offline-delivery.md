# TUI offline delivery

The chat TUI sends through the existing signed Agent message path and durable messaging outbox. It does not introduce a second queue and does not require a daemon to remain running.

## User-visible states

- `Outbox: sending; durable queue not yet confirmed` means signing/enqueue/publish is still in progress. It is not a queued or accepted result.
- `Outbox: queued for retry • <12-hex-event-id> (not delivered; awaiting relay ACK)` is shown only after the existing outbox has durably retained the signed event. The original text stays in the input until this result arrives.
- `Outbox: relay accepted • <12-hex-event-id> (recipient delivery/read not confirmed)` means at least one configured relay acknowledged the same signed event. It does not establish recipient receipt or reading.
- `Outbox: failed` distinguishes a known failure from a durable pending state. Queue durability uncertainty is shown explicitly and never relabeled as queued. A send that cannot confirm enqueue leaves its text in the input.

The shared `messaging.QueuedAgentMessageResult` exposes content-free typed `State` (`failed`, `queued`, `relay_accepted`) and `Issue` codes. Relay ACK and local history/outbox bookkeeping issues can coexist; a safe issue is not hidden by an ACK. Raw relay errors, message content, secrets, or encryption material are not copied into the TUI status line or logs.

## Recovery and lifecycle

Each chat model owns one serial outbox worker. It processes the initial send and retries through `messaging.AttemptSend`, which reuses the persisted event JSON, EventID, QueueID guards, and cross-process outbox lock. Retry eligibility follows the existing quadratic seconds backoff capped at five minutes. Automatic retries run while the TUI is open and respect the existing `MaxRetries`; exhausted entries remain in the shared outbox with failed status and can be inspected/retried using the existing outbox CLI.

At startup, the worker scans persisted entries and retries pending entries for the current identity across contacts. Identity matching uses the signed event author; entries from other identities are not touched. Status/ACK updates for other contacts are not rendered in the currently open conversation. Reopening the relevant chat reconstructs pending/failed state from disk.

The TUI cancels and joins its outbox worker before closing its database. A shutdown during network I/O leaves the durable entry available for the next TUI session. Publishing is at-least-once: if a relay accepts an event just as the client is interrupted before outbox cleanup is durable, that same EventID can be published again. Repeated copies of that event are handled by the existing event-ID idempotency path; this is not a promise of exactly-once network delivery.

## Scope

This covers one-to-one chat send, outbox persistence/retry, and visible relay acceptance. It does not implement group messaging, recipient delivery receipts, or a TUI outbox browser/manual retry control. A relay outage may leave an entry pending until connectivity returns or the existing retry limit is reached.
