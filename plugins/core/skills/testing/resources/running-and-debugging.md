# Running suites and debugging failures

## Test commands per repository

| Repository | Framework | Command | Notes |
|-----------|-----------|---------|-------|
| `<device>` (device/edge repo) | pytest | `make test` (unit), `make test-all` (full) | Hardware protocol libraries may be missing on macOS; use `--ignore` flag |
| `apps/<mainApp>` | Jest | `npm run test` | |
| `apps/<mainApp>` | Playwright | `npm run test:e2e` | Requires running app instance |
| infrastructure repo | chart tooling | template render + `--dry-run` install | |

## Goal: Run existing test suite

```bash
# device repo
cd <device>
make test              # Unit tests only (fast)
make test-all          # Full suite (requires DB for integration tests)
pytest test/unit/test_telemetry_parser.py -v  # Single file

# main app
cd apps/<mainApp>
npm run test           # Jest unit tests
npm run test:e2e       # Playwright E2E (requires running app)
npx jest <path> -v     # Single file

# deployment config
cd <infrastructure-repo>
npm run build charts/<mainApp>/ -f charts/<mainApp>/values.localdev.yaml
```

**Warning:** `make test-all` runs integration tests that may require a live database and hardware connections. Use `make test` for fast unit-only feedback.

## Goal: Debug a failing test

### Step 1: Get the failure output

```bash
# Python
pytest test/unit/test_<module>.py -v -s --tb=long
# TypeScript
npx jest <path> --verbose --no-cache
```

### Step 2: Check environment prerequisites

- **Database required?** Integration tests may need a running PostgreSQL instance
- **Hardware required?** Device-repo tests involving the hardware protocol need real device hardware or `MOCK_MODE=true`
- **App running?** Playwright E2E tests need the app server running

### Step 3: Identify root cause

- **Assertion failure:** Compare expected vs. actual values; check if the source code behavior changed
- **Import error:** Verify module paths; check if the module under test was renamed or moved
- **Timeout:** Check if the test depends on an external service not available
- **Flaky test:** Run 5 times in sequence; if it passes intermittently, check for race conditions or shared state

### Step 4: Fix and re-run

Apply the fix, then run the test again. Also run the full suite to check for regressions.

**Checkpoint:** Test passes. Full suite passes.

## Self-check before returning

- [ ] Test file is in the correct location following repo naming conventions
- [ ] Test uses AAA pattern (Arrange, Act, Assert)
- [ ] Test assertions verify behavior, not implementation details
- [ ] Test runs successfully (executed after writing)
- [ ] No vacuous assertions (`expect(true).toBe(true)`)
- [ ] Test does not depend on execution order (can run independently)
- [ ] Mock/fixture data is realistic (uses the project's domain types -- see the consumer project's `CLAUDE.md` and schema)
- [ ] For integration tests: prerequisite services are documented in test comments
- [ ] For E2E tests: `data-testid` attributes are used for selectors (not CSS classes)

## Common mistakes to avoid

- **Vacuous assertions** -- `expect(true).toBe(true)` or `assert True` prove nothing
- **Testing implementation details** -- asserting that a specific internal method was called N times; assert on the output instead
- **Missing error case tests** -- every function that can fail should have a test for the failure path
- **Hardcoded test data that drifts** -- use fixtures/factories; update when schema changes
- **Running integration tests without checking prerequisites** -- `make test-all` requires a database; `npm run test:e2e` requires a running app
- **Not running the test after writing it** -- always execute the test to verify it passes

## Escalation

- **Test requires infrastructure not available locally** (live database, hardware, cluster): Document the prerequisite and recommend running in CI instead
- **Flaky test cannot be stabilized after three attempts:** Flag for manual investigation; do not delete without user approval
- **Test framework not installed:** Provide installation command but do not run `npm install` or `pip install` without user confirmation

## Failure modes

| Failure | Recovery |
|---------|----------|
| Hardware protocol library import error on macOS | Use `--ignore` for the hardware-dependent test files; these tests require device hardware |
| Jest cannot find module | Check import paths; verify `tsconfig.json` path aliases match |
| Playwright timeout | Verify app server is running; increase timeout for slow CI |
| Template render fails | Check template syntax; verify values file exists and is valid YAML |
| Test passes locally but fails in CI | Check env differences (NODE_ENV, database URL, timezone) |

## Related skills

- **test-coverage** -- identifies *what* needs tests (gap analysis); this skill provides *how* to write them. Coverage targets are defined in the test-coverage skill as the single source of truth.
- **code-standards** -- testing naming conventions and code quality standards
- **troubleshooting** -- for debugging live application issues (vs. test failures)
