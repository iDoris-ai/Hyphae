# NIP-44 reference vectors

`nip44-reference.json` contains unchanged public test-vector values from two
fixed upstream sources, with a zero-based `index` added to each row.

1. [paulmillr/nip44 `nip44.vectors.json`](https://github.com/paulmillr/nip44/blob/671a1f04bcfacaf125b0db68adc45bc9ce0e763b/nip44.vectors.json)
   at `671a1f04bcfacaf125b0db68adc45bc9ce0e763b`, 37,630 bytes,
   SHA-256 `269ed0f69e4c192512cc779e78c555090cebc7c785b609e338a62afc3ce25040`.
   This is the source pointed to by the official NIP-44 Tests and code section.
   Its README says the TypeScript/JavaScript and NIP source is public domain;
   other listed implementations have separate licenses. This fixture copies
   data only, not implementation code.
2. [nostr-protocol/nips `44.md`](https://github.com/nostr-protocol/nips/blob/0046368a747c5c25ae2bec28bae0e537744c8f10/44.md)
   at `0046368a747c5c25ae2bec28bae0e537744c8f10`, 19,610 bytes,
   SHA-256 `b5f89374e4e1dbdee7881e8573b4313b6a89430a9c8d518471599ae0660cf0e2`.
   The NIPS repository README says all NIPs are public domain.

The subset preserves all original fields and values for:

- all 10 `v2.valid.encrypt_decrypt` vectors;
- all 3 `v2.valid.encrypt_decrypt_long_msg` vectors;
- all 12 `v2.invalid.decrypt` vectors;
- all 3 official extended length-prefix rows for 65,535, 65,536, and 65,537 bytes.

The old `v2.invalid.encrypt_msg_lengths` group is intentionally excluded:
it treats 65,536 and 100,000 byte messages as invalid and conflicts with the
extended-length-prefix format now specified by NIP-44. The fixed official
extended vectors above are included and must pass; do not remove or weaken them
if a pinned implementation fails.

Secret keys and nonces in these vectors are public test data. They are used
only in tests and must never be treated as real identities or credentials.
Tests read only this checked-in fixture and temporary test data; they do not
read or write `~/.hyphae`.

Run with:

```sh
go test ./pkg/crypto -run 'TestNIP44Reference' -count=1
```

The pinned Go SDK currently has separate known gaps: MAC comparison uses
`bytes.Equal` rather than a constant-time comparison; `Decrypt` has no explicit
maximum encoded-input length; and a 132-character CR/LF-only base64 input can
panic after Go's base64 decoder ignores the line breaks. A local probe reproduced
the panic in both the SDK and Hyphae's decrypt wrapper. The application guard
is tracked separately in [PR #93](https://github.com/iDoris-ai/hyphae/pull/93);
this vector suite does not claim that guard is merged or that the upstream SDK
gaps are fixed.

Passing these vectors does not establish constant-time MAC behavior, signature
verification, authorization, resource limits, or Go/Agent24 interoperability.
It also does not establish a product message-size limit: the 65,535–65,537 byte
cases test the official extended prefix behavior of the pinned Go SDK only.
