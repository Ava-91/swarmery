# Comment template — `cannot-reproduce`

**Purpose**: Jira comment body for the `cannot-reproduce` verdict — the
reproduction command was **executed**, the behavior described in the ticket
did **not** occur, and no other explanation for the ticket was found.

**Rendered and posted by**: `plugins/jira-pack/skills/jira-writeback/SKILL.md`.
Verdict source: `plugins/jira-pack/skills/jira-triage/SKILL.md`, which only
assigns this verdict when a reproduction command actually ran and its output
was captured — if the run could not execute the reproduction at all, the
correct template is `comment-needs-info.md`, never this one.

**Required blocks** (every one must be filled in with real content — no
placeholder left unresolved in the posted comment):
1. what was checked (the exact command)
2. exit code
3. a relevant fragment of the output
4. environment / branch / commit
5. conclusion
6. what to do next if the bug does still reproduce for the reporter

**Language**: the same language the ticket (`summary`/`description`) is
written in; English if that is ambiguous.

---

## Template

```markdown
Ran the reproduction and could not reproduce the reported behavior.

**Checked**
- Command: `<exact command executed — jira.repro.test, jira.repro.setup, and/or
  the ticket's own more specific repro step>`
- Exit code: `<0 | non-zero, whatever the command actually returned>`
- Environment: `<branch>` @ `<commit sha>`, working repo `<resolved repo root>`

**Output** (relevant fragment — not the full log)
```
<trimmed, meaningful excerpt of the command's stdout/stderr>
```

**Conclusion**: the behavior described in the ticket
("<one-line restatement of the reported symptom>") did not occur under the
conditions above.

**If this still reproduces for you**: please attach the exact steps (command,
input, environment difference) that trigger it. This run's attempt found no
discrepancy from the behavior documented here — a narrower, more specific
repro is what would confirm a real regression.

<!-- swarmery:jira-task-runner run=<external_id or run tag> verdict=cannot-reproduce -->
```
