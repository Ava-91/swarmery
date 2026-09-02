# functional-design — procedure, guardrails, and worked examples

## Inputs

- `file_path` — the TypeScript file to refactor.
- `function_name` — one function to target; omit to scan the whole file.

## Outputs

Edits applied via the Edit tool, capped at **3 per invocation**; each refactored
function stays under 40 lines; the summary stays under 30 lines. One summary row
per change: function name | principle applied | before snippet (3–5 lines) |
after snippet (3–5 lines).

## Procedure

1. **Read the target file** — find functions that mutate parameters, use
   imperative loops, or mix I/O with calculation.
   *Checkpoint: candidates listed with line numbers.*
2. **Classify each candidate** — which principle applies: immutability, pure
   function extraction, composition, or data-flow pipeline. Skip anything on the
   "when not to use" list. *Checkpoint: no candidate is a route handler, server
   action, ORM query, or performance-critical path.*
3. **Verify the route-handler boundary** — when extracting a calculation out of a
   route handler, Read the handler but Edit only files in `src/lib/`. The handler
   itself gets at most **one** Edit, adding an import and the call. Never refactor
   its I/O. *Checkpoint: every Edit target is in `src/lib/`, except that one import.*
4. **Verify types** — Grep/Read to confirm every referenced type, interface, and
   import. Never guess a parameter shape. *Checkpoint: types verified.*
5. **Apply the refactor** — one Edit per function so diffs stay reviewable. Use
   `readonly` on interface properties and `readonly T[]` for array parameters.
   *Checkpoint: each Edit applied and shown.*
6. **Show the diff** — per Edit: function name, principle, before and after
   snippets. *Checkpoint: summary complete.*
7. **Verify** — re-read the file to confirm it still parses; flag anything needing
   manual testing as `[VERIFY]`. *Checkpoint: parses; `[VERIFY]` items flagged.*

## Self-check

- [ ] Every refactored function is pure — no side effects, same input, same output.
- [ ] No refactor landed in a route handler, server action, or ORM call site
      (beyond the single import-only Edit).
- [ ] Every Edit target is in the main app's `src/lib/`.
- [ ] Interface properties carry `readonly` wherever immutability was applied.
- [ ] No new dependency introduced (no immer, ramda, lodash-fp unless already in
      `package.json`).
- [ ] Every change has a before/after diff in the summary.
- [ ] No I/O function (database, fetch, WebSocket) got wrapped in a synchronous pipeline.
- [ ] No `pipe()` used — TypeScript has none built in; chain methods or use
      sequential `const` assignments.
- [ ] Nothing was applied to Python — this skill is TypeScript-only.

## Common mistakes

- Do not apply immutability to high-frequency telemetry structures on the device
  side — spread-copy allocation on a per-tick hot path is prohibitive.
- Do not wrap async I/O in synchronous composition chains — `await` the I/O, then
  pipe the result through pure transformations.
- Do not add `immer` or `ramda` without confirming they are already dependencies.
- Do not refactor inside React render functions — `useState`/`useReducer` already
  enforce immutable updates.
- Do not use an undeclared `pipe()`.
- Do not Edit a route handler beyond the import and call — its structure belongs
  to `code-standards`.
- Do not apply this skill to any Python file.

## Escalation

Stop and ask when: the target both calculates and performs I/O (the caller decides
where the boundary goes); the file has no unit tests and the refactor changes a
return type (tests first); or the function has more than 3 call sites (the blast
radius needs review).

## What to surface

File paths and line numbers per changed function; the principle applied to each;
every function skipped and why (I/O, performance-critical, framework constraint);
and every `[VERIFY]` item needing manual testing.

## Worked example — order pricing

Before (`src/lib/orders/pricing.ts`):

```typescript
function calculateOrderCost(order: Order): void {
  order.baseCost = order.items.length * 2.5;
  order.shippingCost = calculateTotalWeight(order.items) * 0.1;
  order.totalCost = order.baseCost + order.shippingCost;
  order.status = 'PRICED';
}
```

Principle: replace mutation with transformation + immutability.

After:

```typescript
interface OrderCostResult {
  readonly baseCost: number;
  readonly shippingCost: number;
  readonly totalCost: number;
}

const calculateOrderCost = (
  items: readonly OrderItem[]
): OrderCostResult => {
  const baseCost = items.length * 2.5;
  const shippingCost = calculateTotalWeight(items) * 0.1;
  return {
    baseCost,
    shippingCost,
    totalCost: baseCost + shippingCost,
  };
};
```

Changes: the function no longer mutates the `Order` — it returns a new
`OrderCostResult`; it takes `readonly OrderItem[]`, depending only on what it
needs; and the `status = 'PRICED'` transition is gone, because a status change is
a side effect belonging to the calling route handler, not a pricing calculation.

## Worked example — fleet filtering with composition

Before (`src/lib/devices/filters.ts`):

```typescript
function getAvailableDevices(devices: Device[], taskType: string): Device[] {
  const result: Device[] = [];
  for (const device of devices) {
    if (device.active && device.batteryLevel > 20) {
      if (taskType === 'TRACKING' && device.hasGPS) {
        result.push(device);
      } else if (taskType === 'MONITORING' && device.hasSensors) {
        result.push(device);
      }
    }
  }
  return result;
}
```

Principle: replace the loop with higher-order functions and extracted predicates.

After:

```typescript
const isOperational = (device: Device): boolean =>
  device.active && device.batteryLevel > 20;

const supportsTracking = (device: Device): boolean => device.hasGPS;
const supportsMonitoring = (device: Device): boolean => device.hasSensors;

const taskCapability: Record<string, (device: Device) => boolean> = {
  TRACKING: supportsTracking,
  MONITORING: supportsMonitoring,
};

const getAvailableDevices = (
  devices: readonly Device[],
  taskType: string
): readonly Device[] =>
  devices
    .filter(isOperational)
    .filter(taskCapability[taskType] ?? (() => false));
```

## Failure modes

| Mode | Symptom | Detection | Fix |
|------|---------|-----------|-----|
| Over-refactoring I/O code | Async operations wrapped in a synchronous chain; runtime errors | `await` inside a composed function | Split I/O from pure logic: `await` the I/O, then pass the result through the pure pipeline |
| Performance regression on the telemetry path | GC pressure, dropped frames at 5Hz | Spread operator in a per-tick function | Revert to a mutable update on the hot path and document why |
| Broken call sites | Type errors in callers | `npm run typecheck` fails | Update every call site to the new signature, or revert if the blast radius is too large |
| Edit applied to a route handler | Handler logic changed beyond the import | Edit target is not under `src/lib/` | Undo it; move the logic to `src/lib/` and leave only the import in the handler |
