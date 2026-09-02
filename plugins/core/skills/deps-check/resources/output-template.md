# deps-check — output template and worked example

## Output format

**Format:** Structured markdown report returned inline to the caller.

**Length budget:** Max 200 lines per report. Consolidate the outdated-packages table if it exceeds 20 rows (show top 20 by severity, append a "... and N more" note).

## Output template

```markdown
## Dependency Audit Report

**Date**: {YYYY-MM-DD}
**Repos scanned**: {N} of {total}
**Scan failures**: {list of repos where scan command failed, or "None"}

### Summary

| Repo | Dependency file | Total deps | Outdated | Vulnerable (>= threshold) |
|------|----------------|------------|----------|---------------------------|
| apps/<mainApp> | package.json | {N} | {N} | {N} |
| <device> | requirements.txt | {N} | {N} | {N} |
| <infrastructure-repo> | Chart.yaml (subcharts) | {N} | {N} | N/A |

### Critical / High Vulnerabilities

| Package | Current | Fixed in | Severity | CVE | Repo |
|---------|---------|----------|----------|-----|------|
| {name} | {ver} | {ver} | {sev} | {id} | {repo} |

### Cross-Repo Version Mismatches

| Package | Version in repo A | Version in repo B | Notes |
|---------|-------------------|-------------------|-------|

### Outdated Packages (non-vulnerable)

| Package | Current | Latest | Type | Repo |
|---------|---------|--------|------|------|

### Recommendations

1. {Prioritized action item with specific version target}
```

## Worked example: monthly dependency audit across all project repos

```bash
# Step 1: Scan the main app (runs in parallel with Step 2)
cd apps/<mainApp>
npm outdated --json
# {
#   "next": {"current": "16.1.6", "wanted": "16.1.8", "latest": "16.2.0"},
#   "some-orm": {"current": "0.45.1", "wanted": "0.45.1", "latest": "0.46.0"}
# }

npm audit --json
# {
#   "vulnerabilities": {
#     "got": {"severity": "moderate", "via": ["CVE-2025-XXXX"], "fixAvailable": true}
#   }
# }

# Step 2: Scan the device repo (runs in parallel with Step 1)
cd <device>
pip list --outdated --format=json
# [{"name": "device-protocol-lib", "version": "2.4.41", "latest_version": "2.4.43"}]

pip-audit --format=json
# {"dependencies": [], "vulnerabilities": []}

# Step 3: Scan chart dependencies (after helm repo update)
helm repo update
helm search repo bitnami/postgresql --versions | head -5
```

**Report output:**
```markdown
## Dependency Audit Report

**Date**: 2026-05-24
**Repos scanned**: 3 of 3
**Scan failures**: None

### Summary

| Repo | Dependency file | Total deps | Outdated | Vulnerable (>= moderate) |
|------|----------------|------------|----------|--------------------------|
| apps/<mainApp> | package.json | 42 | 2 | 1 |
| <device> | requirements.txt | 15 | 1 | 0 |
| <infrastructure-repo> | Chart.yaml | 3 | 0 | N/A |

### Critical / High Vulnerabilities

None at this severity level.

### Outdated Packages (non-vulnerable)

| Package | Current | Latest | Type | Repo |
|---------|---------|--------|------|------|
| next | 16.1.6 | 16.2.0 | npm | apps/<mainApp> |
| some-orm | 0.45.1 | 0.46.0 | npm | apps/<mainApp> |
| device-protocol-lib | 2.4.41 | 2.4.43 | pip | <device> |

### Recommendations

1. Upgrade `got` in the main app to fix moderate CVE-2025-XXXX (fix available via `npm audit fix`)
2. Evaluate the Next.js minor upgrade for bug fixes (test build + typecheck before merging)
3. Upgrade device-protocol-lib 2.4.41 -> 2.4.43 for protocol updates
```
