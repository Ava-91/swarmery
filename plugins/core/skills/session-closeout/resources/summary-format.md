# SUMMARY.md — canonical final report format

Location: `{task-dir}/SUMMARY.md` (task root — not under `phases/`). The
dashboard's task view renders this file; the `phases/08-summary.md` mirror is
optional legacy compatibility. Budget ≤300 lines. `Status:` is one of
`COMPLETE | PARTIAL | FAILED` — as it actually is.

```markdown
# {Task name}

Status: COMPLETE
Priority: P2
Task: {task-id}

## Результат
{2-6 sentences: what shipped and where it landed — merged PR, deployed env,
 artifact paths. Lead with the outcome, not the process.}

## Змінені файли
- `path/to/file.ts` — {one line: what changed}
{counts from `git diff --stat`, never estimated}

## Агенти
| Агент | Фаза | Loops | Вердикт |
|---|---|---|---|
{one row per delegation, from logs/agents.md}

## Сесії
{session ids / dates that produced this task, if known}

## Відхилення від плану
{actual vs planned: subagents used vs the ORCHESTRATION.md table, correction
 loops taken, criteria deferred — "None" is a valid entry, silence is not}

## Скріншоти
- `screenshots/01-phase5-list-empty.png` — {caption}
{or: None captured}

## Follow-ups
- [ ] {follow-up with an owner, or "None"}
```

Rules:

- Line counts and file lists come from `git diff --stat` — measured, not
  recalled.
- Deviations compare against ORCHESTRATION.md (planned subagents + Loop
  sections) when it exists, else against `plan/`.
- Write the file with the Write tool and verify `test -s` before reporting
  the task closed.
