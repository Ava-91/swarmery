# html-reporting — the canonical shell

Copy this shell verbatim, then fill `{{TITLE}}`, `{{META}}` and the `<main>` body.
Severity classes: `.sev-p0` / `.sev-p1` / `.sev-p2` / `.sev-p3` for audit tiers;
`.badge.ok` / `.badge.warn` / `.badge.crit` for status. Never invent inline
colors — the whole point of one shell is that every report reads the same.

```html
<!DOCTYPE html><html lang="uk"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{TITLE}}</title>
<style>
  :root{--bg:#0d1117;--panel:#161b22;--border:#30363d;--text:#e6edf3;
        --muted:#8b949e;--accent:#58a6ff;--mono:"SF Mono","JetBrains Mono",Menlo,monospace;
        --p0:#f85149;--p1:#d29922;--p2:#58a6ff;--p3:#8b949e;
        --ok:#3fb950;--warn:#d29922;--crit:#f85149;}
  *{box-sizing:border-box}
  body{margin:0;background:var(--bg);color:var(--text);line-height:1.6;
       font-family:var(--mono);-webkit-font-smoothing:antialiased}
  .wrap{max-width:920px;margin:0 auto;padding:2rem 1.5rem 4rem}
  header{border-bottom:1px solid var(--border);padding-bottom:1rem;margin-bottom:1.5rem}
  h1{font-size:1.5rem;margin:0 0 .4rem} h2{font-size:1.15rem;margin:2rem 0 .8rem}
  .meta{color:var(--muted);font-size:.85rem}
  .score{display:inline-block;font-size:2rem;font-weight:700}
  .card{background:var(--panel);border:1px solid var(--border);border-radius:8px;
        padding:1rem 1.2rem;margin:.8rem 0}
  .card.sev-p0{border-left:4px solid var(--p0)} .card.sev-p1{border-left:4px solid var(--p1)}
  .card.sev-p2{border-left:4px solid var(--p2)} .card.sev-p3{border-left:4px solid var(--p3)}
  table{width:100%;border-collapse:collapse;margin:1rem 0;font-size:.88rem}
  th,td{text-align:left;padding:.5rem .6rem;border-bottom:1px solid var(--border)}
  th{color:var(--muted);text-transform:uppercase;font-size:.72rem;letter-spacing:.05em}
  code{background:#1f2630;padding:.1em .4em;border-radius:4px;font-size:.9em}
  .badge{display:inline-block;padding:.1em .55em;border-radius:999px;font-size:.72rem;font-weight:600}
  .badge.ok{background:rgba(63,185,80,.15);color:var(--ok)}
  .badge.warn{background:rgba(210,153,34,.15);color:var(--warn)}
  .badge.crit{background:rgba(248,81,73,.15);color:var(--crit)}
  details{background:var(--panel);border:1px solid var(--border);border-radius:8px;padding:.6rem 1rem;margin:.6rem 0}
  summary{cursor:pointer;font-weight:600}
  a{color:var(--accent)}
</style></head>
<body><div class="wrap">
<header><h1>{{TITLE}}</h1><p class="meta">{{META}}</p></header>
<main>{{BODY}}</main>
</div></body></html>
```

## Component map

| Content | Component |
|---|---|
| Status header | `<h1>` plus a `.badge` |
| Metrics | `<table>` |
| Per-role guidance | `<details>` / `<summary>` |
| Findings | `.card.sev-pN` |
| Positives | `.badge.ok` |
| Known issues | `.card.crit` |
| Health score | `.score` in the header |
