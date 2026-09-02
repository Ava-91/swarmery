# Migration Review Worked Examples

## Example: Migration safety review

Reviewing `V1.0.4__add_mission_status.sql`:

```sql
-- V1.0.4__add_mission_status.sql
ALTER TABLE backend.mission
  ADD COLUMN status VARCHAR(20) NOT NULL DEFAULT 'DRAFT';

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mission_status
  ON backend.mission (status);
```

**Assessment:**
- Reversibility: YES -- `ALTER TABLE DROP COLUMN` can undo
- Data safety: PASS -- no destructive operations
- Index safety: PASS -- uses `CONCURRENTLY`
- Constraints: PASS -- NOT NULL has DEFAULT 'DRAFT'
- Naming: PASS -- follows `V{version}__{description}.sql`
- Idempotency: PARTIAL -- index uses `IF NOT EXISTS` but column does not

**Before (not idempotent):**
```sql
ALTER TABLE backend.mission ADD COLUMN status VARCHAR(20) NOT NULL DEFAULT 'DRAFT';
```

**After (idempotent):**
```sql
ALTER TABLE backend.mission ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'DRAFT';
```

## Example: Schema alignment output

```markdown
## Migration Report

**Database:** PostgreSQL (schema: backend)
**Migration Tool:** Flyway
**ORM:** Drizzle (apps/<mainApp>)

### Migration Files
| File | Status | Safe | Notes |
|------|--------|------|-------|
| V1.0.1__create_backend_schema.sql | Applied | YES | Creates all initial tables |
| V1.0.2__create_indexes.sql | Applied | YES | Index creation |

### Schema Alignment
| Table | Migration | ORM | Zod/DTO | Issues |
|-------|-----------|-----|---------|--------|
| settings | YES | YES | YES | None |
| mission | YES | YES | MISSING | No Zod schema for mission input validation |

### Recommendations
- Add Zod input validation schema for mission creation in the main app's route handler
```
