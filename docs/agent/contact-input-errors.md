# Contact public-key input errors

`contact add --nickname NAME --npub KEY` now parses `KEY` before loading or creating `~/.hyphae`. Malformed npub/hex input, a private nsec, and other invalid public-key forms return the generic `user_error` classification (exit code 1). The error message does not echo the supplied key. Valid npub and 64-character hex public keys continue through `AddContactWithRole`, which stores them in canonical npub form.

This deliberately changes malformed-key classification from `other_error` (exit code 4) in the locked a4aa606 baseline. Other failures, including keystore read or write failures with a valid key, retain their existing error classification; this change does not reclassify every `AddContactWithRole` failure.

Agent24 #614's real-binary test currently expects exit code 4 for this invalid-contact case. Keep the test against its currently pinned binary unchanged. Update that expectation only after Agent24 adopts a new Hyphae source/build hash that includes this change.
