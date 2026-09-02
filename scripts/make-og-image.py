#!/usr/bin/env python3
"""Render the Open Graph / social-preview cards for the landing page.

    python3 scripts/make-og-image.py

Writes:
  site/og.png         1200x630 — og:image / twitter:image (link previews)
  site/og-social.png  1280x640 — GitHub Settings -> Social preview

Both are composed from docs/screenshots/overview.png, the same command-deck
screenshot the README uses, so the card and the repo show the same product.
Regenerate after replacing that screenshot; do not hand-edit the PNGs.

Requires Pillow (pip install pillow) and the macOS system fonts; on other
platforms pass a TTF path via OG_FONT_DIR.
"""
import os
import sys

from PIL import Image, ImageDraw, ImageFont

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SRC = os.path.join(ROOT, "docs", "screenshots", "overview.png")

BG = (15, 16, 19)
PANEL = (22, 24, 29)
INK = (233, 231, 225)
DIM = (150, 147, 139)
ACCENT = (227, 164, 74)

FONT_DIR = os.environ.get("OG_FONT_DIR", "/System/Library/Fonts/Supplemental")
BOLD = os.path.join(FONT_DIR, "Arial Bold.ttf")
REG = os.path.join(FONT_DIR, "Arial.ttf")


def font(path, size):
    try:
        return ImageFont.truetype(path, size)
    except OSError:
        sys.exit(f"✗ font not found: {path} (set OG_FONT_DIR)")


def tracked(draw, xy, text, fnt, fill, tracking=0):
    """Draw text with letter-spacing; PIL has no tracking of its own."""
    x, y = xy
    for ch in text:
        draw.text((x, y), ch, font=fnt, fill=fill)
        x += draw.textlength(ch, font=fnt) + tracking
    return x


def card(width, height, band, shot):
    im = Image.new("RGB", (width, height), BG)
    d = ImageDraw.Draw(im)

    d.rectangle([0, 0, width, band], fill=PANEL)
    d.rectangle([0, band - 3, width, band], fill=ACCENT)

    pad = 64
    wm = tracked(d, (pad, 52), "SW", font(BOLD, 62), INK, 6)
    # The wordmark's diamond, drawn rather than typed: the glyph U+25C6 is
    # missing from the system faces available here and renders as tofu.
    cx, cy, r = wm + 26, 86, 15
    d.polygon([(cx, cy - r), (cx + r, cy), (cx, cy + r), (cx - r, cy)], fill=ACCENT)
    tracked(d, (wm + 58, 52), "RMERY", font(BOLD, 62), INK, 6)

    d.text((pad, 140), "Run your Claude Code agents like a fleet.",
           font=font(BOLD, 34), fill=INK)
    d.text((pad, 186), "Local control plane for Claude Code sessions · "
                       "one Go binary · no cloud, no account",
           font=font(REG, 25), fill=DIM)

    # Command deck, cropped to the headline + metrics + timeline band.
    crop = shot.crop((0, 40, shot.width, 40 + int(shot.width * 0.375)))
    crop = crop.resize((width, int(crop.height * width / crop.width)), Image.LANCZOS)
    im.paste(crop.crop((0, 0, width, height - band)), (0, band))

    # Fade the hard bottom cut into the background.
    fade = 90
    for i in range(fade):
        y = height - fade + i
        a = i / fade
        row = im.crop((0, y, width, y + 1)).load()
        for x in range(width):
            r, g, b = row[x, 0]
            d.point((x, y), fill=(int(r + (BG[0] - r) * a),
                                  int(g + (BG[1] - g) * a),
                                  int(b + (BG[2] - b) * a)))
    return im


def main():
    if not os.path.exists(SRC):
        sys.exit(f"✗ missing source screenshot: {SRC}")
    shot = Image.open(SRC).convert("RGB")
    for name, w, h, band in (("og.png", 1200, 630, 240),
                             ("og-social.png", 1280, 640, 250)):
        out = os.path.join(ROOT, "site", name)
        im = card(w, h, band, shot)
        im.convert("P", palette=Image.ADAPTIVE, colors=128).save(out, optimize=True)
        print(f"✓ {out}  {w}x{h}  {os.path.getsize(out) // 1024} KB")


if __name__ == "__main__":
    main()
