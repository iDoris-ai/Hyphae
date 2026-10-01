This is an unmodified copy of the public production lock at Agent24 main commit
`65a5c5115482522539496bc5c5cd8cdbec9a2f6e`:

- URL: https://raw.githubusercontent.com/iDoris-ai/Agent24/65a5c5115482522539496bc5c5cd8cdbec9a2f6e/rust/crates/agent24-comm/hyphae.lock.json
- Original file SHA-256: `fbb96d21b72597029826d321d65a7d8a2428a3d9f509d079213bb7808667578a`

The test fixture is for schema regression tests. The real integration runner still
requires an explicit external production lock path and checks its contents; it
never falls back to this fixture.
