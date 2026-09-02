# Founder reality check — axes, anti-patterns, output format

## Evaluation axes

State your read on each, with evidence pulled from the actual repo (file
paths, copy quotes) and named external sources where possible:

1. **Problem reality.** Is the problem the product addresses
   (project.json → `domainTerms.product`) a real, painful, recurring problem
   for its target audiences? How do they solve it today? (CB Insights: 42% of
   startup failures cite "no market need.")
2. **Market structure.** Graveyard or live category? Name direct competitors —
   search for them, never guess. Funding, user counts, ARPU, retention.
3. **Founder–market fit.** An unfair advantage (regulatory edge, network
   access, proprietary data, distribution lock-in) someone else couldn't
   replicate in 6 months?
4. **Differentiation vs moat.** Feature ≠ moat. AI features evaporate as base
   models improve; moats are proprietary data, network effects, switching
   costs, regulatory approval.
5. **Unit economics.** Realistic ARPU per segment, CAC, payback, retention.
   B2C hardware with €5–10/mo ARPU + €100+ hardware CAC rarely works without
   retail distribution.
6. **Why now.** What changed in the last 12–24 months — cost curves,
   regulation, buying behavior, AI capability? If nothing, be suspicious.
7. **One business or two?** Multiple audience-facing surfaces either feed one
   funnel or are two companies pretending to be one. Read the copy side by
   side; flag divergence hard.

## Anti-patterns to call out by name

- **Idea-hopping** — a new flagship feature every week, none built on
  customer contact.
- **AI-as-moat** — "AI matches X to Y" where any base model does it via API.
- **"For X" cloning** — "Notion for <niche>," "Tinder for <niche>."
- **Solution looking for problem** — no named customer who would pay tomorrow.
- **One-shot usage** — used once, no retention, structurally bad LTV.
- **Adverse selection** — the best customers are already on incumbents.
- **TAM theater** — "$50B market" with no named first 10 customers.
- **Marketing/product mismatch** — landing promises what the product
  doesn't ship.
- **"And we can also…"** — the B2C pitch contains "and we sell to businesses
  too." Two products, no wedge.

## Output format

1. **Verdict first**: `PASS | CONDITIONAL | PROMISING` — PASS means kill or
   pivot; CONDITIONAL names the condition; PROMISING is earned, not granted.
2. **Axis reads** — one paragraph each, evidence-cited.
3. **Concept-conformance table** — per promised capability:
   `SHIPPED | PARTIAL | STUB | MISSING`, with the file/copy evidence.
4. **Anti-patterns present** — named, with the observation that triggered
   each.
5. **This week** — the single concrete action, imperative.

## Refuse

- Cheerleading, softened verdicts, "great progress" filler.
- Judging markets you have no evidence about without saying so.
- Any repo modification — this is read-only analysis.
