# Not a skill

A SKILL.md whose first line is not `---`. Per system-docs-format.md §5.5 (and
system-config-format.md §1.1, the rule the agent and command scanners already
follow) a file with no frontmatter is not a registrable item, so the scanner
must skip this directory silently — no skill row, no parse_error, no
docs_missing finding. The CI coverage gate skips it for the same reason; the
two sides must agree or an undocumented item hides behind a green gate.
