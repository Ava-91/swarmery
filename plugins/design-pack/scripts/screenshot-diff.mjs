#!/usr/bin/env node
// screenshot-diff.mjs — pixel diff between an exported design and a live route.
//
// Both sides are rendered by the same pinned browser at the same viewport with
// motion disabled, so the only thing that can differ is the implementation. The
// output is a number, four images, and a ranked list of the regions that carry
// the difference — enough to act on without opening either page.
//
//   node screenshot-diff.mjs --design <file.html|dir|zip> --url <http://localhost:3000/route>
//                            [--viewport 1440x900] [--out .design-verify/<slug>]
//                            [--threshold 0.5] [--pixel-tolerance 0.1] [--full-page] [--allow-remote]
//
// The process exit code reports technical success, never the verdict: a failing
// diff still exits 0 with "pass": false in report.json. Deciding what to do about
// a failing diff belongs to the skill or the agent, not to this process.
//
// Exit codes: 0 ok | 1 bad arguments/input | 2 render or reachability failure
//             3 runtime not prepared.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import {
  EXIT,
  die,
  ensureRuntime,
  launchChromium,
  loadImageTools,
  openPage,
  parseArgs,
  parseNumber,
  parseViewport,
  resolveDesignInput,
  writeJson,
} from './runtime.mjs';

const USAGE = `usage: screenshot-diff.mjs --design <file.html|dir|zip> --url <url> [options]

  --design <path>          exported design: .html file, directory or .zip  (required)
  --url <url>              implemented route; loopback or file:// unless --allow-remote  (required)
  --viewport <WxH>         render viewport, default 1440x900
  --out <dir>              output directory, default .design-verify/<slug>
  --threshold <percent>    pass when the diff is at or below this, default 0.5
  --pixel-tolerance <n>    per-pixel colour tolerance 0..1, default 0.1
  --full-page              capture the full scrollable page instead of the viewport
  --allow-remote           permit a non-loopback --url (off by default on purpose)`;

const GRID = 16;
const MAX_REGIONS = 10;
const DIFF_COLOR = [255, 0, 0];

/* ── guards ───────────────────────────────────────────────────────────────── */

function isLoopback(url) {
  if (url.protocol === 'file:') return true;
  const host = url.hostname.toLowerCase().replace(/^\[|\]$/g, '');
  if (host === 'localhost' || host.endsWith('.localhost')) return true;
  if (host === '::1' || host === '::' || host === '0.0.0.0') return true;
  return /^127\.\d+\.\d+\.\d+$/.test(host);
}

async function assertReachable(url) {
  if (url.protocol === 'file:') {
    const file = fileURLToPath(url);
    if (!fs.existsSync(file)) die(EXIT.RUNTIME, `the implementation file does not exist: ${file}`);
    return;
  }
  const startTheApp =
    'Start the application first with the command recorded in the design.devCommand config key — ' +
    'this script never starts a dev server for you.';
  const probe = (method) => fetch(url, { method, redirect: 'follow', signal: AbortSignal.timeout(10000) });

  let response = null;
  try {
    response = await probe('HEAD');
  } catch {
    response = null;
  }
  if (!response || response.status === 405 || response.status === 501) {
    try {
      response = await probe('GET');
    } catch (error) {
      die(EXIT.RUNTIME, `${url.href} is unreachable (${error.message}). ${startTheApp}`);
    }
  }
  if (response.status >= 400) {
    die(EXIT.RUNTIME, `${url.href} answered HTTP ${response.status}. ${startTheApp}`);
  }
}

function slugFor(url) {
  const base = url.protocol === 'file:'
    ? path.basename(fileURLToPath(url)).replace(/\.html?$/i, '')
    : url.pathname;
  const slug = base.replace(/[^a-zA-Z0-9]+/g, '-').replace(/^-+|-+$/g, '').toLowerCase();
  return slug || 'root';
}

/* ── image helpers ────────────────────────────────────────────────────────── */

function padTo(PNG, image, width, height) {
  if (image.width === width && image.height === height) return image;
  const padded = new PNG({ width, height });
  padded.data.fill(0);
  PNG.bitblt(image, padded, 0, 0, image.width, image.height, 0, 0);
  return padded;
}

/**
 * Group differing pixels into regions: a 16x16 grid, cells that hold at least one
 * differing pixel, merged across 4-connected neighbours. Bounding boxes are taken
 * from the differing pixels themselves, so a region is tight, not grid-aligned.
 */
function clusterRegions(diffData, width, height, totalDiff) {
  const cols = Math.ceil(width / GRID);
  const rows = Math.ceil(height / GRID);
  const cells = cols * rows;
  const counts = new Int32Array(cells);
  const minX = new Int32Array(cells).fill(width);
  const minY = new Int32Array(cells).fill(height);
  const maxX = new Int32Array(cells).fill(-1);
  const maxY = new Int32Array(cells).fill(-1);

  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const at = (y * width + x) * 4;
      if (diffData[at] !== DIFF_COLOR[0] || diffData[at + 1] !== DIFF_COLOR[1] || diffData[at + 2] !== DIFF_COLOR[2]) {
        continue;
      }
      const cell = Math.floor(y / GRID) * cols + Math.floor(x / GRID);
      counts[cell] += 1;
      if (x < minX[cell]) minX[cell] = x;
      if (y < minY[cell]) minY[cell] = y;
      if (x > maxX[cell]) maxX[cell] = x;
      if (y > maxY[cell]) maxY[cell] = y;
    }
  }

  const seen = new Uint8Array(cells);
  const regions = [];
  for (let start = 0; start < cells; start += 1) {
    if (seen[start] || counts[start] === 0) continue;
    let diffPixels = 0;
    let x0 = width;
    let y0 = height;
    let x1 = -1;
    let y1 = -1;
    const queue = [start];
    seen[start] = 1;
    while (queue.length) {
      const cell = queue.pop();
      diffPixels += counts[cell];
      if (minX[cell] < x0) x0 = minX[cell];
      if (minY[cell] < y0) y0 = minY[cell];
      if (maxX[cell] > x1) x1 = maxX[cell];
      if (maxY[cell] > y1) y1 = maxY[cell];
      const col = cell % cols;
      const row = (cell - col) / cols;
      const neighbours = [
        col > 0 ? cell - 1 : -1,
        col < cols - 1 ? cell + 1 : -1,
        row > 0 ? cell - cols : -1,
        row < rows - 1 ? cell + cols : -1,
      ];
      for (const next of neighbours) {
        if (next < 0 || seen[next] || counts[next] === 0) continue;
        seen[next] = 1;
        queue.push(next);
      }
    }
    regions.push({
      x: x0,
      y: y0,
      width: x1 - x0 + 1,
      height: y1 - y0 + 1,
      diffPixels,
      shareOfDiff: totalDiff ? Math.round((diffPixels / totalDiff) * 10000) / 10000 : 0,
    });
  }

  return regions.sort((a, b) => b.diffPixels - a.diffPixels).slice(0, MAX_REGIONS);
}

// The composite is laid out by the browser that is already running: it is the one
// tool here that can draw a caption without adding a font or a canvas dependency.
async function writeSideBySide(browser, outDir, target, size) {
  const sheetWidth = size.width * 3 + 48;
  const sheetHeight = size.height + 44;
  const html = `<!doctype html>
<html><head><meta charset="utf-8"><style>
  html,body{margin:0;padding:0;background:#0b1220}
  #sheet{display:flex;gap:12px;padding:12px;width:max-content;
         font:600 12px/16px ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;color:#e2e8f0}
  figure{margin:0}
  figcaption{padding:0 0 6px;letter-spacing:0.06em;text-transform:uppercase}
  img{display:block;border:1px solid #1e293b;image-rendering:pixelated}
</style></head><body>
  <div id="sheet">
    <figure><figcaption>design</figcaption><img src="design.png"></figure>
    <figure><figcaption>implementation</figcaption><img src="impl.png"></figure>
    <figure><figcaption>diff</figcaption><img src="diff.png"></figure>
  </div>
</body></html>`;

  const scratch = path.join(outDir, '.side-by-side.html');
  fs.writeFileSync(scratch, html);
  const context = await browser.newContext({
    viewport: { width: Math.min(sheetWidth, 8000), height: Math.min(sheetHeight, 8000) },
    deviceScaleFactor: 1,
  });
  try {
    const page = await context.newPage();
    await page.goto(pathToFileURL(scratch).href, { waitUntil: 'load' });
    await page.locator('#sheet').screenshot({ path: target });
  } finally {
    await context.close().catch(() => {});
    fs.rmSync(scratch, { force: true });
  }
}

/* ── main ─────────────────────────────────────────────────────────────────── */

const args = parseArgs(process.argv.slice(2), {
  '--design': { type: 'value' },
  '--url': { type: 'value' },
  '--viewport': { type: 'value', default: '1440x900' },
  '--out': { type: 'value', default: null },
  '--threshold': { type: 'value', default: '0.5' },
  '--pixel-tolerance': { type: 'value', default: '0.1' },
  '--full-page': { type: 'flag' },
  '--allow-remote': { type: 'flag' },
}, USAGE);

if (!args.design) die(EXIT.USAGE, `--design is required\n\n${USAGE}`);
if (!args.url) die(EXIT.USAGE, `--url is required\n\n${USAGE}`);

let url;
try {
  url = new URL(args.url);
} catch {
  die(EXIT.USAGE, `--url must be an absolute URL (got "${args.url}")\n\n${USAGE}`);
}

// Refusing a non-loopback target by default keeps this tool from ever pointing a
// browser at a shared or production origin by accident.
if (!isLoopback(url) && !args.allowRemote) {
  die(
    EXIT.USAGE,
    `refusing to capture ${url.href}: only loopback targets (localhost, 127.0.0.0/8, ::1) and ` +
      'file:// are allowed. Pass --allow-remote if you really mean to screenshot a remote origin.',
  );
}

const viewport = parseViewport(args.viewport, USAGE);
const threshold = parseNumber(args.threshold, '--threshold', USAGE);
const pixelTolerance = parseNumber(args.pixelTolerance, '--pixel-tolerance', USAGE);
if (pixelTolerance > 1) die(EXIT.USAGE, `--pixel-tolerance must be between 0 and 1\n\n${USAGE}`);

const design = resolveDesignInput(args.design);
const outDir = path.resolve(args.out ?? path.join('.design-verify', slugFor(url)));

await assertReachable(url);

const runtime = await ensureRuntime({ quiet: true });
const { PNG, pixelmatch } = await loadImageTools(runtime);
const browser = await launchChromium(runtime);

fs.mkdirSync(outDir, { recursive: true });
const artifacts = {
  design: path.join(outDir, 'design.png'),
  impl: path.join(outDir, 'impl.png'),
  diff: path.join(outDir, 'diff.png'),
  sideBySide: path.join(outDir, 'side-by-side.png'),
};

let report;
try {
  const capture = async (target) => {
    const { context, page } = await openPage(browser, { viewport, url: target });
    const buffer = await page.screenshot({ fullPage: args.fullPage });
    await context.close().catch(() => {});
    return PNG.sync.read(buffer);
  };

  const designImage = await capture(pathToFileURL(design.html).href);
  const implImage = await capture(url.href);

  const sizeMismatch =
    designImage.width === implImage.width && designImage.height === implImage.height
      ? null
      : {
          design: { width: designImage.width, height: designImage.height },
          impl: { width: implImage.width, height: implImage.height },
        };

  const width = Math.max(designImage.width, implImage.width);
  const height = Math.max(designImage.height, implImage.height);
  const left = padTo(PNG, designImage, width, height);
  const right = padTo(PNG, implImage, width, height);

  const diff = new PNG({ width, height });
  const diffPixels = pixelmatch(left.data, right.data, diff.data, width, height, {
    threshold: pixelTolerance,
    includeAA: false,
    diffColor: DIFF_COLOR,
  });

  fs.writeFileSync(artifacts.design, PNG.sync.write(left));
  fs.writeFileSync(artifacts.impl, PNG.sync.write(right));
  fs.writeFileSync(artifacts.diff, PNG.sync.write(diff));
  await writeSideBySide(browser, outDir, artifacts.sideBySide, { width, height });

  const totalPixels = width * height;
  const diffPercent = Math.round((diffPixels / totalPixels) * 100 * 100) / 100;

  report = {
    url: url.href,
    design: design.html,
    viewport,
    sizeMismatch,
    diffPixels,
    totalPixels,
    diffPercent,
    threshold,
    // A size mismatch is a layout defect in its own right, so it cannot pass even
    // when the overlapping pixels happen to line up.
    pass: diffPercent <= threshold && sizeMismatch === null,
    regions: clusterRegions(diff.data, width, height, diffPixels),
    artifacts,
  };
} finally {
  await browser.close().catch(() => {});
  design.cleanup();
}

writeJson(path.join(outDir, 'report.json'), report);

process.stdout.write(`design:     ${report.design}\n`);
process.stdout.write(`url:        ${report.url}\n`);
process.stdout.write(`viewport:   ${viewport.width}x${viewport.height}${args.fullPage ? ' (full page)' : ''}\n`);
if (report.sizeMismatch) {
  const { design: d, impl: i } = report.sizeMismatch;
  process.stdout.write(
    `SIZE:       design ${d.width}x${d.height} vs implementation ${i.width}x${i.height} — ` +
      'compared on the padded union; this is a layout defect by itself\n',
  );
}
process.stdout.write(
  `diff:       ${report.diffPixels}/${report.totalPixels} px = ${report.diffPercent}% ` +
    `(threshold ${report.threshold}%, pixel tolerance ${pixelTolerance})\n`,
);
process.stdout.write(`verdict:    ${report.pass ? 'pass' : 'FAIL'}\n`);
if (report.regions.length) {
  process.stdout.write(`regions:    top ${report.regions.length} of the difference\n`);
  for (const region of report.regions) {
    process.stdout.write(
      `  ${String(Math.round(region.shareOfDiff * 100)).padStart(3)}%  ` +
        `${region.width}x${region.height} at ${region.x},${region.y}  (${region.diffPixels} px)\n`,
    );
  }
}
process.stdout.write(`artifacts:  ${outDir}\n`);

process.exit(EXIT.OK);
