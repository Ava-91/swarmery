# env-check — output template and worked example

## Output format

**Format:** Markdown report saved to the path specified by the caller.

**Length budget:** Max 150 lines for the report body. Consolidate tables with more than 20 rows into a top-20 list sorted by severity, with a count of omitted items.

## Output template

```markdown
## Environment Variables Report

**Repositories Checked:** N
**Total Variables Found:** N
**Documented in env.example:** N
**Missing from examples:** N
**Confidence:** HIGH | MEDIUM (if dynamic env access patterns detected)

### By Repository
| Repo | Defined | Used | Documented | Issues |
|------|---------|------|------------|--------|
| apps/<mainApp> | 12 | 14 | 10 | 4 |

### Missing Variables
| Variable | Used at | Expected in |
|----------|---------|-------------|
| `DATABASE_URL` | `src/lib/db/index.ts:8` | `env.example` |

### Unused Variables
| Variable | Declared in | Last referenced |
|----------|-------------|-----------------|
| `OLD_API_URL` | `env.example:3` | nowhere |

### Security Issues
| Issue | Location | Severity |
|-------|----------|----------|
| Hardcoded API key | `src/lib/maps.ts:4` | critical |

### Low-Confidence Findings
[Dynamic env access patterns that could not be resolved statically]
```

## Worked example: cross-repo env var audit for BACKEND_API_URL

**Task:** Verify `BACKEND_API_URL` is consistent across the device/edge repo and the service config in the infrastructure repo.

**Step 1 — Grep the device/edge repo:**
```
Grep: os.environ.get("BACKEND_API_URL" in <device>/src/
Found: src/agents/main_agent.py:12  BACKEND_API_URL = os.environ.get("BACKEND_API_URL", "http://localhost:3000")
```

**Step 2 — Grep the service config (run in parallel with Step 1):**
```
Grep: BACKEND_API_URL in the infrastructure repo
Found: charts/<device>/values.yaml:18  BACKEND_API_URL: "http://<mainApp>:3000"
Found: charts/<device>/templates/deployment.yaml:42  - name: BACKEND_API_URL
```

**Step 3 — Cross-reference:**
- Device repo default: `http://localhost:3000`
- Production value: `http://<mainApp>:3000`
- Consistent naming, different defaults (expected: local dev vs in-cluster service DNS)

**Step 4 — Check env.example:**
```
Grep: BACKEND_API_URL in <device>/env.example
Not found — MISSING from env.example
```

**Report entry:**
```
| `BACKEND_API_URL` | `src/agents/main_agent.py:12` | `env.example` |
```

The naming is consistent and the differing defaults are expected (local dev vs in-cluster DNS), so the one real finding is the missing `env.example` entry.
