#!/usr/bin/env node
// extract-computed-styles.mjs — ground truth token inventory from a rendered design.
//
// Renders an exported design in the pinned browser and walks every element,
// reading `getComputedStyle`. What a design *is* is what the browser resolves it
// to, not what its stylesheet says: this is the only measurement in the pack that
// cannot be argued with.
//
//   node extract-computed-styles.mjs --input <file.html|dir|file.zip> [--viewport 1440x900]
//                                    [--out .design-verify/tokens] [--json-only]
//
// Exit codes: 0 ok | 1 bad arguments/input | 2 render failure | 3 runtime not prepared.

import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

import {
  EXIT,
  die,
  ensureRuntime,
  launchChromium,
  openPage,
  parseArgs,
  parseViewport,
  resolveDesignInput,
  writeJson,
} from './runtime.mjs';

const USAGE = `usage: extract-computed-styles.mjs --input <file.html|dir|file.zip> [options]

  --input <path>        exported design: .html file, directory or .zip  (required)
  --viewport <WxH>      render viewport, default 1440x900
  --out <dir>           output directory, default .design-verify/tokens
  --json-only           write tokens.json only, skip tokens.md`;

/* ── in-page collection ───────────────────────────────────────────────────── */
// Everything below runs inside the rendered document; it must be self-contained.
function collectInventory() {
  // `html` is skipped with the non-rendered elements: it carries only browser
  // defaults, and its selector path is empty by construction.
  const SKIP = new Set([
    'HTML', 'SCRIPT', 'STYLE', 'LINK', 'META', 'TITLE', 'HEAD', 'BASE',
    'NOSCRIPT', 'TEMPLATE', 'BR', 'SOURCE', 'TRACK', 'PARAM',
  ]);

  const selectorFor = (element) => {
    const parts = [];
    let node = element;
    while (node && node.nodeType === 1 && node.tagName !== 'HTML' && parts.length < 4) {
      let part = node.tagName.toLowerCase();
      const first = (node.getAttribute('class') || '').trim().split(/\s+/).filter(Boolean)[0];
      if (first) part += `.${first.replace(/[^\w-]/g, '')}`;
      const parent = node.parentElement;
      if (parent) {
        const twins = Array.prototype.filter.call(parent.children, (c) => c.tagName === node.tagName);
        if (twins.length > 1) {
          part += `:nth-child(${Array.prototype.indexOf.call(parent.children, node) + 1})`;
        }
      }
      parts.unshift(part);
      node = node.parentElement;
    }
    return parts.join(' > ');
  };

  /* colour normalisation — the browser is the parser, canvas is the reader */
  const canvas = document.createElement('canvas');
  canvas.width = 1;
  canvas.height = 1;
  const ctx = canvas.getContext('2d', { willReadFrequently: true });
  const colorCache = new Map();

  const clamp255 = (v) => Math.max(0, Math.min(255, Math.round(v)));

  // Fallback for browsers whose canvas cannot parse CSS Color 4 syntax yet.
  const oklchToRgb = (L, C, H) => {
    const hr = (H * Math.PI) / 180;
    const a = C * Math.cos(hr);
    const b = C * Math.sin(hr);
    const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
    const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
    const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
    const lin = [
      4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
      -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
      -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
    ];
    return lin.map((c) => clamp255(255 * (c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055)));
  };

  const manualColor = (value) => {
    const oklch = /^oklch\(\s*([\d.]+%?)\s+([\d.]+%?)\s+([\d.]+)(?:deg)?\s*(?:\/\s*([\d.]+%?)\s*)?\)$/i.exec(value);
    if (oklch) {
      const num = (raw, scale) => (raw.endsWith('%') ? parseFloat(raw) / 100 * scale : parseFloat(raw));
      const [r, g, b] = oklchToRgb(num(oklch[1], 1), num(oklch[2], 0.4), parseFloat(oklch[3]));
      const alpha = oklch[4] === undefined ? 1 : num(oklch[4], 1);
      return [r, g, b, Math.round(alpha * 1000) / 1000];
    }
    const rgb = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)\s*(?:[,/]\s*([\d.]+%?)\s*)?\)$/i.exec(value);
    if (rgb) {
      const alpha = rgb[4] === undefined
        ? 1
        : rgb[4].endsWith('%') ? parseFloat(rgb[4]) / 100 : parseFloat(rgb[4]);
      return [clamp255(+rgb[1]), clamp255(+rgb[2]), clamp255(+rgb[3]), Math.round(alpha * 1000) / 1000];
    }
    const hex = /^#([0-9a-f]{3,8})$/i.exec(value);
    if (hex) {
      let digits = hex[1];
      if (digits.length === 3 || digits.length === 4) digits = digits.split('').map((d) => d + d).join('');
      const part = (i) => parseInt(digits.slice(i * 2, i * 2 + 2), 16);
      const alpha = digits.length === 8 ? Math.round((part(3) / 255) * 1000) / 1000 : 1;
      return [part(0), part(1), part(2), alpha];
    }
    return null;
  };

  const probe = (value) => {
    try {
      ctx.fillStyle = '#010203';
      ctx.fillStyle = value;
      const first = ctx.fillStyle;
      ctx.fillStyle = '#040506';
      ctx.fillStyle = value;
      // An unparsable value leaves both sentinels untouched, so they differ.
      if (first !== ctx.fillStyle) return null;
      ctx.save();
      ctx.globalCompositeOperation = 'copy';
      ctx.fillStyle = value;
      ctx.fillRect(0, 0, 1, 1);
      ctx.restore();
      const data = ctx.getImageData(0, 0, 1, 1).data;
      return [data[0], data[1], data[2], Math.round((data[3] / 255) * 1000) / 1000];
    } catch {
      return null;
    }
  };

  const toRgba = (raw) => {
    if (!raw) return null;
    const value = String(raw).trim();
    if (!value || value === 'none' || value === 'transparent' || value === 'currentcolor') return null;
    if (colorCache.has(value)) return colorCache.get(value);
    let rgba = probe(value) ?? manualColor(value);
    if (rgba && rgba[3] === 0) rgba = null;
    colorCache.set(value, rgba);
    return rgba;
  };

  const hexOf = ([r, g, b, a]) => {
    const two = (n) => n.toString(16).padStart(2, '0');
    const base = `#${two(r)}${two(g)}${two(b)}`;
    return a >= 1 ? base : base + two(Math.round(a * 255));
  };

  /* inventories */
  const colors = new Map();
  const typography = new Map();
  const spacing = new Map();
  const radii = new Map();
  const borders = new Map();
  const shadows = new Map();
  const layout = new Map();
  const stacks = new Map();

  const bump = (map, key, seed) => {
    let entry = map.get(key);
    if (!entry) {
      entry = seed();
      map.set(key, entry);
    }
    entry.count += 1;
    return entry;
  };

  const elements = Array.prototype.filter.call(document.querySelectorAll('*'), (el) => !SKIP.has(el.tagName));

  for (const element of elements) {
    const cs = getComputedStyle(element);
    let cachedSelector = null;
    const sel = () => {
      if (cachedSelector === null) cachedSelector = selectorFor(element);
      return cachedSelector;
    };

    const addColor = (raw, property) => {
      const rgba = toRgba(raw);
      if (!rgba) return null;
      const hex = hexOf(rgba);
      const entry = bump(colors, hex, () => ({
        hex,
        rgb: [rgba[0], rgba[1], rgba[2]],
        alpha: rgba[3],
        count: 0,
        usedFor: [],
        example: sel(),
      }));
      if (!entry.usedFor.includes(property)) entry.usedFor.push(property);
      return hex;
    };

    addColor(cs.color, 'color');
    addColor(cs.backgroundColor, 'background-color');

    const backgroundImage = cs.backgroundImage;
    if (backgroundImage && backgroundImage !== 'none' && /gradient/i.test(backgroundImage)) {
      const stops = backgroundImage.match(/(?:rgba?|oklch|oklab|hsla?)\([^)]*\)|#[0-9a-fA-F]{3,8}/g) || [];
      for (const stop of stops) addColor(stop, 'background-image');
    }

    for (const side of ['top', 'right', 'bottom', 'left']) {
      const width = cs.getPropertyValue(`border-${side}-width`);
      const style = cs.getPropertyValue(`border-${side}-style`);
      if (!width || parseFloat(width) === 0 || style === 'none' || style === 'hidden') continue;
      const hex = addColor(cs.getPropertyValue(`border-${side}-color`), `border-${side}-color`);
      const value = `${width} ${style} ${hex ?? 'currentcolor'}`;
      bump(borders, value, () => ({ value, count: 0, example: sel() }));
    }

    for (const corner of ['top-left', 'top-right', 'bottom-right', 'bottom-left']) {
      const value = cs.getPropertyValue(`border-${corner}-radius`);
      if (!value || parseFloat(value) === 0) continue;
      bump(radii, value, () => ({ value, count: 0, example: sel() }));
    }

    const shadow = cs.boxShadow;
    if (shadow && shadow !== 'none') {
      bump(shadows, shadow, () => ({ value: shadow, count: 0, example: sel() }));
      const stops = shadow.match(/(?:rgba?|oklch|oklab|hsla?)\([^)]*\)|#[0-9a-fA-F]{3,8}/g) || [];
      for (const stop of stops) addColor(stop, 'box-shadow');
    }

    const bearsText = Array.prototype.some.call(
      element.childNodes,
      (node) => node.nodeType === 3 && node.textContent.trim().length > 0,
    );
    if (bearsText) {
      const face = {
        fontFamily: cs.fontFamily,
        fontSize: cs.fontSize,
        fontWeight: cs.fontWeight,
        fontStyle: cs.fontStyle,
        lineHeight: cs.lineHeight,
        letterSpacing: cs.letterSpacing,
        textTransform: cs.textTransform,
      };
      bump(typography, JSON.stringify(face), () => ({ ...face, count: 0, example: sel() }));
      const first = String(cs.fontFamily).split(',')[0].trim().replace(/^["']|["']$/g, '');
      if (first) {
        const stack = stacks.get(cs.fontFamily) ?? { stack: cs.fontFamily, first, count: 0 };
        stack.count += 1;
        stacks.set(cs.fontFamily, stack);
      }
    }

    const addSpace = (property, value) => {
      const numeric = parseFloat(value);
      if (!value || !value.endsWith('px') || !Number.isFinite(numeric) || numeric === 0) return;
      bump(spacing, `${property}|${value}`, () => ({ value, property, count: 0, example: sel() }));
    };
    for (const box of ['margin', 'padding']) {
      for (const side of ['top', 'right', 'bottom', 'left']) {
        addSpace(`${box}-${side}`, cs.getPropertyValue(`${box}-${side}`));
      }
    }

    const isTrack = /flex|grid/.test(cs.display);
    if (isTrack) {
      const rowGap = cs.rowGap;
      const columnGap = cs.columnGap;
      if (rowGap === columnGap) addSpace('gap', rowGap);
      else {
        addSpace('row-gap', rowGap);
        addSpace('column-gap', columnGap);
      }
    }

    // Categorical layout properties only. Resolved `width`/`height` are read per
    // element but deliberately kept out of the inventory: they are measurements
    // of one node, not reusable design decisions, and would bury the real tokens.
    const layoutProps = [
      ['display', cs.display, (v) => Boolean(v)],
      ['flex-direction', cs.flexDirection, (v) => isTrack && v !== 'row'],
      ['align-items', cs.alignItems, (v) => isTrack && v !== 'normal'],
      ['justify-content', cs.justifyContent, (v) => isTrack && v !== 'normal'],
      ['grid-template-columns', cs.gridTemplateColumns, (v) => v && v !== 'none'],
      ['opacity', cs.opacity, (v) => v && v !== '1'],
      ['z-index', cs.zIndex, (v) => v && v !== 'auto'],
      ['max-width', cs.maxWidth, (v) => v && v !== 'none'],
      ['text-decoration', cs.textDecoration, (v) => v && !v.startsWith('none')],
    ];
    for (const [property, value, keep] of layoutProps) {
      if (!keep(value)) continue;
      bump(layout, `${property}|${value}`, () => ({ property, value, count: 0, example: sel() }));
    }
  }

  /* authored breakpoints from every reachable stylesheet */
  const breakpoints = new Set();
  const walkRules = (rules) => {
    for (const rule of Array.prototype.slice.call(rules ?? [])) {
      if (rule.media && rule.media.mediaText) {
        for (const match of rule.media.mediaText.matchAll(/(\d+(?:\.\d+)?)px/g)) {
          breakpoints.add(Math.round(parseFloat(match[1])));
        }
      }
      if (rule.cssRules) walkRules(rule.cssRules);
    }
  };
  for (const sheet of Array.prototype.slice.call(document.styleSheets)) {
    try {
      walkRules(sheet.cssRules);
    } catch {
      /* a stylesheet the document may not read is simply not inventoried */
    }
  }

  return {
    elementsScanned: elements.length,
    innerWidth: window.innerWidth,
    mediaBreakpoints: Array.from(breakpoints),
    colors: Array.from(colors.values()),
    typography: Array.from(typography.values()),
    spacing: Array.from(spacing.values()),
    radii: Array.from(radii.values()),
    borders: Array.from(borders.values()),
    shadows: Array.from(shadows.values()),
    layout: Array.from(layout.values()),
    stacks: Array.from(stacks.values()),
  };
}

/* ── node-side shaping ────────────────────────────────────────────────────── */

function srgbToOklch([r, g, b]) {
  const linear = (channel) => {
    const c = channel / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  const R = linear(r);
  const G = linear(g);
  const B = linear(b);
  const l = Math.cbrt(0.4122214708 * R + 0.5363325363 * G + 0.0514459929 * B);
  const m = Math.cbrt(0.2119034982 * R + 0.6806995451 * G + 0.1073969566 * B);
  const s = Math.cbrt(0.0883024619 * R + 0.2817188376 * G + 0.6299787005 * B);
  const L = 0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s;
  const A = 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s;
  const Bb = 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s;
  let hue = (Math.atan2(Bb, A) * 180) / Math.PI;
  if (hue < 0) hue += 360;
  return { l: L, c: Math.hypot(A, Bb), h: hue };
}

function oklchString(rgb, alpha) {
  const { l, c, h } = srgbToOklch(rgb);
  const base = `${(l * 100).toFixed(1)}% ${c.toFixed(4)} ${h.toFixed(1)}`;
  return alpha >= 1 ? `oklch(${base})` : `oklch(${base} / ${alpha})`;
}

const byCountDesc = (a, b) => b.count - a.count;
const numeric = (value) => {
  const parsed = parseFloat(value);
  return Number.isFinite(parsed) ? parsed : Number.POSITIVE_INFINITY;
};

function shapeTokens(raw, { source, viewport }) {
  const authoredBreakpoints = Array.from(new Set([...raw.mediaBreakpoints, raw.innerWidth])).sort((a, b) => b - a);

  const colors = raw.colors
    .map((entry) => ({
      hex: entry.hex,
      oklch: oklchString(entry.rgb, entry.alpha),
      count: entry.count,
      usedFor: entry.usedFor,
      example: entry.example,
    }))
    .sort(byCountDesc);

  const typography = raw.typography
    .map(({ count, example, ...face }) => ({ ...face, count, example }))
    .sort((a, b) => b.count - a.count || numeric(b.fontSize) - numeric(a.fontSize));

  const spacing = raw.spacing
    .map((entry) => ({
      value: entry.value,
      property: entry.property,
      count: entry.count,
      onFourPxGrid: Math.abs(numeric(entry.value)) % 4 === 0,
      example: entry.example,
    }))
    .sort((a, b) => numeric(a.value) - numeric(b.value) || a.property.localeCompare(b.property));

  const radii = raw.radii
    .map(({ value, count, example }) => ({ value, count, example }))
    .sort((a, b) => numeric(a.value) - numeric(b.value));

  const borders = raw.borders.map(({ value, count, example }) => ({ value, count, example })).sort(byCountDesc);
  const shadows = raw.shadows.map(({ value, count, example }) => ({ value, count, example })).sort(byCountDesc);
  const layout = raw.layout
    .map(({ property, value, count, example }) => ({ property, value, count, example }))
    .sort((a, b) => a.property.localeCompare(b.property) || b.count - a.count);

  const fontFamiliesRequired = [];
  for (const stack of [...raw.stacks].sort(byCountDesc)) {
    if (stack.first && !fontFamiliesRequired.includes(stack.first)) fontFamiliesRequired.push(stack.first);
  }

  return {
    source,
    viewport,
    authoredBreakpoints,
    elementsScanned: raw.elementsScanned,
    colors,
    typography,
    spacing,
    radii,
    borders,
    shadows,
    layout,
    fontFamiliesRequired,
  };
}

/* ── markdown rendering ───────────────────────────────────────────────────── */

// Backslash FIRST, then the pipe: escaping `|` into `\|` without having escaped
// the backslashes already present turns a value ending in `\` into `\\|`, which
// markdown reads as an escaped backslash followed by a LIVE pipe — a stray
// column break in the report. CSS values reach here verbatim (font stacks,
// content strings), so this is reachable, not theoretical.
const cell = (value) =>
  String(value ?? '')
    .replace(/\\/g, '\\\\')
    .replace(/\|/g, '\\|')
    .replace(/\n/g, ' ');

function table(headers, rows) {
  if (!rows.length) return '_none_\n';
  const lines = [
    `| ${headers.join(' | ')} |`,
    `|${headers.map(() => '---').join('|')}|`,
    ...rows.map((row) => `| ${row.map(cell).join(' | ')} |`),
  ];
  return `${lines.join('\n')}\n`;
}

function renderMarkdown(tokens, alternatives) {
  const out = [];
  out.push('# Computed style inventory\n');
  out.push(`- **Source:** \`${tokens.source}\``);
  out.push(`- **Viewport:** ${tokens.viewport.width}x${tokens.viewport.height}`);
  out.push(`- **Elements scanned:** ${tokens.elementsScanned}`);
  out.push(`- **Authored breakpoints:** ${tokens.authoredBreakpoints.join(', ') || 'none'}`);
  out.push(
    `- **Recommended viewport:** ${tokens.viewport.width}x${tokens.viewport.height}` +
      (alternatives.length ? ` (also authored for: ${alternatives.join(', ')})` : ''),
  );
  out.push(`- **Font families required:** ${tokens.fontFamiliesRequired.join(', ') || 'none'}\n`);

  out.push('## Colors\n');
  out.push(table(
    ['hex', 'oklch', 'count', 'used for', 'example'],
    tokens.colors.map((c) => [c.hex, c.oklch, c.count, c.usedFor.join(', '), c.example]),
  ));

  out.push('\n## Typography\n');
  out.push(table(
    ['font-family', 'size', 'weight', 'style', 'line-height', 'letter-spacing', 'transform', 'count', 'example'],
    tokens.typography.map((t) => [
      t.fontFamily, t.fontSize, t.fontWeight, t.fontStyle,
      t.lineHeight, t.letterSpacing, t.textTransform, t.count, t.example,
    ]),
  ));

  out.push('\n## Spacing\n');
  out.push(table(
    ['value', 'property', 'count', '4px grid', 'example'],
    tokens.spacing.map((s) => [s.value, s.property, s.count, s.onFourPxGrid ? 'yes' : 'NO', s.example]),
  ));

  out.push('\n## Radii\n');
  out.push(table(['value', 'count', 'example'], tokens.radii.map((r) => [r.value, r.count, r.example])));

  out.push('\n## Borders\n');
  out.push(table(['value', 'count', 'example'], tokens.borders.map((b) => [b.value, b.count, b.example])));

  out.push('\n## Shadows\n');
  out.push(table(['value', 'count', 'example'], tokens.shadows.map((s) => [s.value, s.count, s.example])));

  out.push('\n## Layout\n');
  out.push(table(
    ['property', 'value', 'count', 'example'],
    tokens.layout.map((l) => [l.property, l.value, l.count, l.example]),
  ));

  return `${out.join('\n')}\n`;
}

/* ── main ─────────────────────────────────────────────────────────────────── */

const args = parseArgs(process.argv.slice(2), {
  '--input': { type: 'value' },
  '--viewport': { type: 'value', default: '1440x900' },
  '--out': { type: 'value', default: path.join('.design-verify', 'tokens') },
  '--json-only': { type: 'flag' },
}, USAGE);

if (!args.input) die(EXIT.USAGE, `--input is required\n\n${USAGE}`);

const viewport = parseViewport(args.viewport, USAGE);
const design = resolveDesignInput(args.input);
const outDir = path.resolve(args.out);

const runtime = await ensureRuntime({ quiet: true });
const browser = await launchChromium(runtime);

let tokens;
try {
  const { page } = await openPage(browser, { viewport, url: pathToFileURL(design.html).href });
  const raw = await page.evaluate(collectInventory).catch((error) => {
    die(EXIT.RUNTIME, `cannot inventory ${design.html}: ${error.message}`);
  });
  tokens = shapeTokens(raw, { source: design.html, viewport });
} finally {
  await browser.close().catch(() => {});
  design.cleanup();
}

fs.mkdirSync(outDir, { recursive: true });
const jsonPath = path.join(outDir, 'tokens.json');
writeJson(jsonPath, tokens);

const alternatives = tokens.authoredBreakpoints.filter((width) => width !== viewport.width);
let markdownPath = null;
if (!args.jsonOnly) {
  markdownPath = path.join(outDir, 'tokens.md');
  fs.writeFileSync(markdownPath, renderMarkdown(tokens, alternatives));
}

process.stdout.write(`source:            ${tokens.source}\n`);
process.stdout.write(`viewport:          ${viewport.width}x${viewport.height} (deviceScaleFactor 1)\n`);
process.stdout.write(`elements scanned:  ${tokens.elementsScanned}\n`);
process.stdout.write(
  `authored widths:   ${tokens.authoredBreakpoints.join(', ') || 'none'}` +
    `${alternatives.length ? ` — also render at ${alternatives.join(', ')}` : ''}\n`,
);
process.stdout.write(
  `inventory:         ${tokens.colors.length} colors, ${tokens.typography.length} text styles, ` +
    `${tokens.spacing.length} spacing steps, ${tokens.radii.length} radii, ` +
    `${tokens.borders.length} borders, ${tokens.shadows.length} shadows\n`,
);
const offGrid = tokens.spacing.filter((s) => !s.onFourPxGrid).map((s) => s.value);
process.stdout.write(`off 4px grid:      ${[...new Set(offGrid)].join(', ') || 'none'}\n`);
process.stdout.write(`fonts required:    ${tokens.fontFamiliesRequired.join(', ') || 'none'}\n`);
process.stdout.write(`wrote:             ${jsonPath}${markdownPath ? `, ${markdownPath}` : ''}\n`);

process.exit(EXIT.OK);
