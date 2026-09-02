# Social cards

`og.png` (1200×630) is referenced by the `og:image` / `twitter:image` tags in
`index.html`; `og-social.png` (1280×640) is uploaded by hand in
**Settings → General → Social preview** on GitHub.

Both are generated — do not hand-edit them:

```bash
python3 scripts/make-og-image.py
```

The source is `docs/screenshots/overview.png`, the same command deck the README
shows. Replace that screenshot, re-run the script, and both cards follow.
Budget: ≤ 300 KB each (they sit at ~105 KB), and no client-identifying text may
appear in the crop.
