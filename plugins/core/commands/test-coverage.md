---
description: Find missing tests for a module/feature and suggest test cases
color: red
docs:
  status: generated
  source_sha: 2c48903a7e77
  updated: 2026-08-06
---

# Test Coverage Check

Read-only test gap analysis -- map source files to test files, identify untested modules with risk classification, and suggest test cases (incl. E2E gaps). Does not write tests.

Follow the playbook in `skills/test-coverage/SKILL.md` (auto-loaded skill `test-coverage`); apply it to $ARGUMENTS if provided.

To actually write or run tests, use the `testing` skill instead.

# How to use

## What it does

This command finds the gaps in your test suite. It maps source files to their test files, flags the modules that have no coverage, sorts them by how risky the gap is, and proposes concrete test cases to close it — including missing end-to-end flows. It only reads and reports; it never writes a test file.

## When to use it

- You inherited a module and want to know which parts are untested before you change them.
- A feature is about to ship and you need a list of the test cases it is still missing.
- Coverage numbers look fine but you suspect whole flows are unexercised.
- You want a prioritized to-do list of tests rather than a raw percentage.

## When not to use it

- To actually write or run the tests — use the `testing` skill for that.
- To get a pass/fail run of the existing suite — that is a test runner's job, not a gap analysis.
- To review code style or complexity — reach for a code-quality check instead.

## How to invoke

```
/test-coverage
```

Type it with no arguments to analyze the whole project, or add a path, module, or feature name to narrow the scan to that area.

## Inputs

- Scope — optional. A file path, directory, module, or feature name. Anything you type after the command is passed through as the target of the analysis. With no argument, the command analyzes the project as a whole.

## What you get back

A report in the conversation, not files on disk. You get the source-to-test mapping it found, the list of untested modules with a risk classification on each, the end-to-end flows that have no test covering them, and suggested test cases for the gaps that matter most. Nothing in your repository is modified.

## Worked example

```
/test-coverage orders/line-items
```

The command maps every source file under that path to its matching test file, reports which ones have none, marks the high-risk gaps (for example, price calculation with no unit test), notes that the checkout flow through line items has no end-to-end test, and lists the specific cases worth adding. You end up with a ranked backlog of tests to write — then hand it to the `testing` skill to write them.

## Related

- `testing` skill — use it when you are ready to write, run, or debug the tests this command identified.
- `code-quality` command — use it when the question is about complexity and standards rather than missing tests.
