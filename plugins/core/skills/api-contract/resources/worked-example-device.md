# api-contract — worked example: verifying the `device` entity

**Step 1: ORM schema** (`apps/<mainApp>/src/lib/db/schema.ts:45`, Drizzle shown)
```typescript
export const devices = backendSchema.table('device', {
  id: serial('id').primaryKey(),
  name: varchar('name', { length: 100 }).notNull(),
  model_id: integer('model_id').references(() => deviceModels.id),
  active: boolean('active').notNull().default(true),
  fleet_id: integer('fleet_id').references(() => fleets.id),
});
```

**Step 2: Zod schema** (`apps/<mainApp>/src/lib/validations/device.ts:8`)
```typescript
export const deviceSchema = z.object({
  id: z.number(),
  name: z.string().min(1).max(100),
  modelId: z.number().nullable(),  // camelCase vs snake_case mismatch
  active: z.boolean(),
  fleetId: z.number().nullable(),
});
```

**Step 3: Route handler** (`apps/<mainApp>/src/app/api/devices/route.ts:12`)
```typescript
export async function GET() {
  const session = await auth();
  if (!session) return Response.json({ error: 'Unauthorized' }, { status: 401 });
  const allDevices = await getDb().select().from(devices);
  return Response.json(allDevices);  // Returns snake_case from the ORM
}
```

**Step 4: SQL migration** (infrastructure repo, `V3__create_device.sql:1`)
```sql
CREATE TABLE backend.device (
  id SERIAL PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  model_id INTEGER REFERENCES backend.device_model(id),
  active BOOLEAN NOT NULL DEFAULT TRUE,
  fleet_id INTEGER REFERENCES backend.fleet(id)
);
```

**Report:**

## API Contract Report for "device"

### Issues Found

| # | Severity | Location | Issue | Fix |
|---|----------|----------|-------|-----|
| 1 | Medium | `src/lib/validations/device.ts:5` | Zod uses `modelId` (camelCase) but the ORM/SQL use `model_id` (snake_case). Route handler returns snake_case from the ORM directly. | Align Zod field name to `model_id` or add a transform layer in the route handler. |
| 2 | Medium | `src/lib/validations/device.ts:7` | Zod uses `fleetId` (camelCase) but the ORM/SQL use `fleet_id` (snake_case). | Same fix as #1 — align naming convention. |
| 3 | Low | `src/app/api/devices/route.ts:15` | Route handler returns raw ORM result without Zod validation on the output shape. | Wrap response in `deviceSchema.array().parse(allDevices)` or document that output validation is intentionally skipped. |

### Action Required
1. Decide on naming convention (camelCase vs snake_case) for API responses and align Zod + ORM accordingly
2. Add output validation or document its intentional absence

### Summary
CONTRACT-ISSUES: device | CRITICAL: 0 | HIGH: 0 | MEDIUM: 2 | LOW: 1
