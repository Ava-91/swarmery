# code-quality — report format and worked examples

## Output template

Markdown, no files written. Budget: 200 lines for a single-file audit, 500 for a
directory audit. The first line is machine-readable so CI gates and downstream
agents can parse it.

```markdown
QUALITY-SCORE: {overall}/100 | ERRORS: {n} | WARNINGS: {n}

## Code Quality Report

**Scope:** {file/module/directory path}
**Repo Type:** {typescript | python}
**Overall Score:** X/100

| Category | Score | Status |
|----------|-------|--------|
| Function Length | /100 | {count} errors, {count} warnings |
| Nesting | /100 | {count} errors, {count} warnings |
| Code Smells | /100 | {count} findings |
| Project-Specific | /100 | {count} errors |

### Error-Level Issues ({count})
[Numbered list with file:line, description, fix suggestion]

### Warning-Level Issues ({count})
[Numbered list with file:line, description, fix suggestion]

### Action Plan
[Prioritized list with effort estimates: High/Medium/Low]
```

## Worked example — TypeScript route handler

**Input:** `scope: apps/<mainApp>/src/app/api/orders/route.ts`, `repo_type: typescript`

**Findings:**

```
[ERROR] Function length: GET() is 62 lines (threshold: 50)
  Location: apps/<mainApp>/src/app/api/orders/route.ts:15
  Fix: Extract order query logic into src/lib/services/order-service.ts

[WARNING] Function length: POST() is 38 lines (threshold: 30)
  Location: apps/<mainApp>/src/app/api/orders/route.ts:78
  Fix: Extract validation logic into a reusable validator

[WARNING] Nesting depth: 3 levels of nesting in POST()
  Location: apps/<mainApp>/src/app/api/orders/route.ts:95
  Fix: Use guard clauses to reduce nesting

[ERROR] Project-specific: missing 'export const dynamic = "force-dynamic"'
        but route imports auth()
  Location: apps/<mainApp>/src/app/api/orders/route.ts:1
  Fix: Add 'export const dynamic = "force-dynamic"' after imports
```

**Report:**

```markdown
QUALITY-SCORE: 68/100 | ERRORS: 2 | WARNINGS: 2

## Code Quality Report

**Scope:** `apps/<mainApp>/src/app/api/orders/route.ts`
**Repo Type:** typescript
**Overall Score:** 68/100

| Category | Score | Status |
|----------|-------|--------|
| Function Length | 60/100 | 1 error, 1 warning |
| Nesting | 90/100 | 1 warning |
| Code Smells | 100/100 | none |
| Project-Specific | 80/100 | 1 error (missing force-dynamic) |

### Error-Level Issues (2)
1. `route.ts:15` -- GET() is 62 lines; extract query logic to the service layer
2. `route.ts:1` -- missing `export const dynamic = 'force-dynamic'`; required
   because the route imports `auth()`

### Warning-Level Issues (2)
1. `route.ts:78` -- POST() is 38 lines; consider extracting validation
2. `route.ts:95` -- 3 levels of nesting; use guard clauses

### Action Plan
1. [High effort] Extract GET() query logic into `src/lib/services/order-service.ts`
   -- reduces length and improves testability
2. [Low effort] Add `export const dynamic = 'force-dynamic'` -- one-line fix
```

## Worked example — Python device-service module

**Input:** `scope: <device>/src/telemetry/protocol_handler.py`, `repo_type: python`

**Findings:**

```
[ERROR] Function length: handle_telemetry() is 68 lines (threshold: 50)
  Location: <device>/src/telemetry/protocol_handler.py:42
  Fix: Extract message parsing into one function per message type

[ERROR] Nesting depth: 4 levels of nesting in handle_telemetry()
  Location: <device>/src/telemetry/protocol_handler.py:55
  Fix: Use early returns and guard clauses

[ERROR] Project-specific: bare except: clause swallows all exceptions
  Location: <device>/src/telemetry/protocol_handler.py:90
  Fix: Catch specific exceptions (e.g. ConnectionError, TimeoutError)
```

**Report:**

```markdown
QUALITY-SCORE: 50/100 | ERRORS: 3 | WARNINGS: 0

## Code Quality Report

**Scope:** `<device>/src/telemetry/protocol_handler.py`
**Repo Type:** python
**Overall Score:** 50/100

| Category | Score | Status |
|----------|-------|--------|
| Function Length | 60/100 | 1 error |
| Nesting | 80/100 | 1 error |
| Code Smells | 100/100 | none |
| Project-Specific | 80/100 | 1 error (bare except) |

### Error-Level Issues (3)
1. `protocol_handler.py:42` -- handle_telemetry() is 68 lines; extract
   per-message-type parsing
2. `protocol_handler.py:55` -- 4 levels of nesting; use guard clauses
3. `protocol_handler.py:90` -- bare `except:`; catch `ConnectionError,
   TimeoutError` instead

### Action Plan
1. [High effort] Decompose handle_telemetry() by message type -- cuts length and
   nesting at once
2. [Low effort] Replace the bare `except:` with specific types -- one-line fix
```
