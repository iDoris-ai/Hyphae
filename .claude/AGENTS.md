# Agent Development Guidelines for agent-speaker

## Test Coverage Requirement (MUST)

Every milestone **MUST** have:

1. **100% Unit Test Coverage** for all new packages/functions
   - Every public function must have at least one unit test
   - Edge cases must be covered (nil inputs, errors, empty results)
   - Use temp directories / mock data - NEVER touch production `~/.agent-speaker/`

2. **Real E2E Tests** using `wss://relay.aastar.io`
   - E2E tests must use the real relay, not mocks
   - Minimum 3 E2E tests per milestone
   - E2E tests must use real identities (alice, bob, jack, etc.)
   - Every CLI command added must be covered by E2E

3. **Test Scripts**
   - Add to `test.sh` for unit/CLI tests
   - Add dedicated `test_<feature>_e2e.sh` for E2E tests
   - All tests must pass before commit

## Commit & Release Process

1. Code the feature
2. Write unit tests (100% coverage)
3. Write E2E tests (real relay)
4. Run `./test.sh` and dedicated E2E script
5. Commit with conventional commits
6. Tag with version
7. Push branch + tag

## Milestone Naming

- v0.25.0: Agent Profile
- v0.26.0: Agent Discovery
- v0.27.0: Local LLM
- v0.28.0: Auto Responder
- v0.29.0: Task Delegation
- v0.30.0: MyTask Bridge
