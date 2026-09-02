# Writing tests: philosophy, frameworks, and patterns

## Testing philosophy

- **Test behavior, not implementation** -- assert what the code does, not how
- **TDD for new functions** -- when writing a brand-new function, write the test stub before the implementation
- **Read-first for existing functions** -- when adding tests to existing code, read the source module first, identify edge cases, then write the test
- **One assertion per test** when practical -- keep tests focused
- **Fast feedback** -- unit tests should complete in milliseconds
- **No flaky tests** -- if a test is intermittent, fix it or delete it
- **AAA pattern** -- Arrange, Act, Assert in every test

## Testing pyramid

```
       /\
      /  \     E2E Tests (10%)
     /____\    - dashboard flows, critical user journeys
    /      \   Integration Tests (30%)
   /________\  - API route handlers, DB queries, WebSocket
  /          \ Unit Tests (60%)
 /____________\- Pure functions, telemetry parsing, formatters
```

## Step 1: Identify the repository and test framework

| Repository | Framework | Test location | Naming convention |
|-----------|-----------|--------------|-------------------|
| `<device>` (device/edge repo) | pytest | `test/unit/test_*.py`, `test/integration/test_*.py` | `test_<module>.py` |
| `apps/<mainApp>` | Jest + RTL | `src/**/__tests__/*.test.{ts,tsx}` | `<module>.test.ts` |
| `apps/<mainApp>` | Playwright | `tests/e2e/*.spec.ts` | `<feature>.spec.ts` |
| infrastructure repo | template render / dry-run | N/A (inline commands) | N/A |

**Checkpoint:** Repository and framework confirmed.

## Step 2: Verify source module exists

Use Glob or Read to confirm the source module path exists. If the module is not found, STOP and ask the user to confirm the path. Do not write tests for a module that cannot be located.

**Checkpoint:** Source module file exists and has been read.

## Step 3: Write the test using appropriate patterns

### Device-repo unit test (pytest)

```python
# test/unit/test_telemetry_parser.py
import pytest
from src.telemetry_parser import TelemetryParser

class TestTelemetryParser:
    @pytest.fixture
    def parser(self):
        return TelemetryParser()

    def test_parse_position_message(self, parser):
        raw = {"type": "POSITION", "lat": 505000000, "lon": 305000000}
        result = parser.parse(raw)
        assert result["LATITUDE"] == 505000000
        assert result["LONGITUDE"] == 305000000

    def test_parse_invalid_message_raises(self, parser):
        with pytest.raises(ValueError, match="Unknown message type"):
            parser.parse({"type": "UNKNOWN"})
```

### Main-app Jest unit test

```typescript
// src/lib/utils/__tests__/formatCoordinates.test.ts
import { formatCoordinates } from '../formatCoordinates';

describe('formatCoordinates', () => {
  it('should format raw telemetry lat/lon to degrees', () => {
    const result = formatCoordinates(505000000, 305000000);
    expect(result).toEqual({ lat: 50.5, lon: 30.5 });
  });

  it('should handle zero coordinates', () => {
    const result = formatCoordinates(0, 0);
    expect(result).toEqual({ lat: 0, lon: 0 });
  });
});
```

### Main-app RTL component test

```typescript
// src/components/__tests__/DeviceCard.test.tsx
import { render, screen } from '@testing-library/react';
import { DeviceCard } from '../DeviceCard';

describe('DeviceCard', () => {
  const mockDevice = { id: 1, identifier: 'd1', active: true };

  it('renders device identifier', () => {
    render(<DeviceCard device={mockDevice} />);
    expect(screen.getByText('d1')).toBeInTheDocument();
  });

  it('shows active status badge for active device', () => {
    render(<DeviceCard device={mockDevice} />);
    expect(screen.getByText('Active')).toBeInTheDocument();
  });
});
```

### Playwright E2E test

```typescript
// tests/e2e/device-dashboard.spec.ts
test.describe('Device Dashboard', () => {
  test('should display device list', async ({ page }) => {
    await page.goto('/dashboard');
    await expect(page.locator('[data-testid="device-card"]')).toHaveCount(9);
  });

  test('should show telemetry for selected device', async ({ page }) => {
    await page.goto('/dashboard');
    await page.click('[data-testid="device-card"]:first-child');
    await expect(page.locator('[data-testid="telemetry-panel"]')).toBeVisible();
  });
});
```

### Deployment config testing (template render + dry-run)

```bash
cd <infrastructure-repo>
# Render the chart templates against the localdev values file
npm run build charts/<mainApp>/ -f charts/<mainApp>/values.localdev.yaml
# Dry-run install to validate the rendered manifests without deploying
helm upgrade --install <mainApp> charts/<mainApp>/ \
  -f charts/<mainApp>/values.localdev.yaml \
  -n <mainApp> --dry-run \
  --set ingress.enabled=true
```

**Checkpoint:** Test file written and syntax-valid.

## Step 4: Run the test

After writing a test, always run it to confirm it passes:

```bash
# device repo
pytest test/unit/test_<module>.py -v

# main app
npx jest src/lib/<module>/__tests__/<module>.test.ts

# Skip hardware-protocol tests on macOS
pytest --ignore=test/test_get_telemetry.py
```

**Checkpoint:** Test passes. If it fails, debug before returning.

## Coverage targets

See the `test-coverage` skill for the authoritative coverage target table. This skill is responsible for writing tests to meet those targets, not defining them.
