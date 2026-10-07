# M1 TUI live inbox acceptance

## Delivered scope

The chat TUI starts its own cancellable inbox watcher; a separate daemon is
not required. Incoming kind-30078 messages are validated, decrypted, and
stored through the existing message-store idempotency path before the TUI is
notified. A durable first arrival refreshes the open conversation. Historical
events are walked before live subscription, with a one-second overlap and
event-ID deduplication to cover messages published during the history scan.
Reconnects repeat the history walk before subscribing again. Relay failures
appear as non-fatal inbox status, so chat input remains usable. Close cancels
and joins the watcher (bounded to five seconds) before closing the DB.

The watcher and conversation view share the same MessageStore. A generation
number on async history loads prevents an older initial empty query from
overwriting a newer live refresh. The notification buffer has capacity 256 and
the producer applies backpressure instead of dropping durable message notices.

## Verification

On the local machine, the real CLI and real TUI were run in three isolated
temporary HOME directories against one persistent loopback relay. Three
identities published profiles and were discovered from that relay; each
pairwise session opened both users' chats in real PTYs. With each receiving
TUI left open and no daemon running, visible delivery passed in both
directions for A↔B, B↔C, and C↔A. During the final open C/A session, the local
relay was stopped and restarted with its same data directory; delivery passed
after reconnect/history recovery. The temporary PTY driver and all test data
were removed after the run; no process remains.

Targeted Go tests:

- go test -race ./internal/messaging ./internal/tui
- TestWatchAgentInboxRecoversHistoryAndDeduplicatesLiveOverlap
- TestWatchAgentInboxWalksPastFirstPageWithSameSecondEvents — 120 distinct
  events sharing one timestamp are stored/notified across the initial
  100-event page boundary.
- TestWatchAgentInboxWithStoreRejectsInvalidInputs — invalid caller inputs
  fail before keystore or relay access.
- TestStoreIncomingWatchEventRejectsBadEventAndContinues — wrong recipient,
  invalid compressed content, and undecryptable ciphertext create no row;
  a later valid event is still stored and notified.
- TestInboxDisconnectIsNonFatalAndInputRemainsUsable
- TestChatModelConcurrentWatcherStartIsSingleAndCloseJoins — concurrent start
  calls launch one watcher and Close waits for its cancellation.
- TestInboxWatchUpdateLoadsDurableMessageIntoView — exercises the watcher
  tea message, actual refresh command, messagesMsg, rendered body, and an
  older empty initial query arriving after the live refresh.

## Scope boundaries

This completes live DM reception/refresh only. Offline outgoing-message retry
or outbox queueing is not added here; send failure/retry behavior remains a
separate M1 item. Group-chat reception/routing is not included. The first PTY
probe used a circular set of open conversations, so it sent Alice→Bob while
Bob was viewing Carol; the stored message was correctly excluded from that
conversation. The final run corrected the setup by opening the matching
pairwise conversation at both ends and passed all six directions.
