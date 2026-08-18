// Captures the MVP screens in mock mode against a running dev server.
//
// Usage:
//   VITE_MOCK=1 npm run dev          # terminal 1
//   node scripts/screenshot.mjs      # terminal 2 (BASE_URL to override)
//
// Uses the system Chrome via playwright-core (no browser download).

import { chromium } from 'playwright-core';
import { mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const base = process.env.BASE_URL ?? 'http://localhost:5173';
const outDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'screenshots');
mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch({ channel: 'chrome', headless: true });

async function settle(page) {
  await page.waitForLoadState('networkidle');
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(600); // mock latency + transitions
}

async function assertNoHorizontalScroll(page, path) {
  // The app frame scrolls inside <main>, so check both the document and main.
  const { scrollWidth, clientWidth, mainScrollWidth, mainClientWidth } = await page.evaluate(
    () => {
      const main = document.querySelector('main');
      return {
        scrollWidth: document.documentElement.scrollWidth,
        clientWidth: document.documentElement.clientWidth,
        mainScrollWidth: main?.scrollWidth ?? 0,
        mainClientWidth: main?.clientWidth ?? 0,
      };
    },
  );
  if (scrollWidth > clientWidth) {
    throw new Error(`horizontal overflow on ${path}: ${scrollWidth} > ${clientWidth}`);
  }
  if (mainScrollWidth > mainClientWidth) {
    throw new Error(`horizontal overflow in <main> on ${path}: ${mainScrollWidth} > ${mainClientWidth}`);
  }
  console.log(`✓ no horizontal scroll on ${path} (${scrollWidth} <= ${clientWidth})`);
}

// Fill-route invariant: on a page that owns its own scroll there must be exactly
// ONE vertical scroller, and it must be deeper than the shell. Two things would
// break that and both are invisible in a screenshot: the document growing past
// the viewport (the whole app scrolls, header included), or the shell's own
// container scrolling (the classic two-scrollbar bug). Neither <main> nor its
// direct child may have scrollable overflow — anything further in is the page's
// own pane, which is exactly where the scroll belongs.
async function assertNoNestedVerticalScroll(page, path) {
  const { docScrollHeight, docClientHeight, offenders } = await page.evaluate(() => {
    const shellNodes = [...document.querySelectorAll('main, main > div')];
    return {
      docScrollHeight: document.documentElement.scrollHeight,
      docClientHeight: document.documentElement.clientHeight,
      offenders: shellNodes
        .filter((el) => el.scrollHeight - el.clientHeight > 1)
        .map((el) => `${el.tagName.toLowerCase()}.${el.className} (${el.scrollHeight}>${el.clientHeight})`),
    };
  });
  if (docScrollHeight > docClientHeight) {
    throw new Error(
      `document scrolls on fill route ${path}: ${docScrollHeight} > ${docClientHeight}`,
    );
  }
  if (offenders.length > 0) {
    throw new Error(`shell container scrolls on fill route ${path}: ${offenders.join('; ')}`);
  }
  console.log(`✓ no nested vertical scroll on ${path} (${docScrollHeight} <= ${docClientHeight})`);
}

// The contract itself, not one of its side effects: on a fill route the shell
// container hands the vertical scroll to the page. assertNoNestedVerticalScroll
// above can pass on a short page even with fill mode reverted, so it needs a
// companion that only passes when the shell actually stops scrolling.
//
// It targets [data-shell-scroller] rather than <main> on purpose: the two shells
// hand off at different depths (global = <main>; workspace = a div inside a
// <main> that is ALWAYS overflow-hidden), so a <main> query would pass
// unconditionally on every /p/<slug>/… route and hide a real regression there.
async function assertShellScroll(page, path, want) {
  const overflowY = await page.evaluate(() => {
    const box = document.querySelector('[data-shell-scroller]');
    return box === null ? 'no-shell-scroller' : getComputedStyle(box).overflowY;
  });
  if (overflowY !== want) {
    throw new Error(
      `shell scroller on ${path}: expected overflow-y=${want}, got ${overflowY}`,
    );
  }
  const what = want === 'hidden' ? 'hands off scroll' : 'keeps its scroller';
  console.log(`✓ shell ${what} on ${path} (overflow-y=${overflowY})`);
}

async function shot(page, path, name, opts = {}) {
  await page.goto(base + path);
  await settle(page);
  if (opts.waitMs) await page.waitForTimeout(opts.waitMs);
  await page.screenshot({ path: join(outDir, name), fullPage: opts.fullPage ?? false });
  await assertNoHorizontalScroll(page, path);
  console.log(`✓ ${name}`);
}

// The approvals mock scenario injects a permission_requested ~3 s after load —
// wait it out so the WS-pushed pending card is in frame.
const APPROVALS_WAIT_MS = 3200;

// Scrolls the session-detail tab panel (its own scroller — the header stays pinned).
async function scrollTabPanel(page, px) {
  await page.locator('[role="tabpanel"]').evaluate((el, y) => {
    el.scrollTop = y;
  }, px);
  await page.waitForTimeout(200);
}

// Mobile-first: the owner's viewport (390×844).
const mobile = await browser.newPage({
  viewport: { width: 390, height: 844 },
  deviceScaleFactor: 2,
});
await shot(mobile, '/', 'overview.png');
await shot(mobile, '/docs', 'docs-mobile.png');
await shot(mobile, '/sessions', 'sessions.png');
// Approvals (phase 2): pending cards + history, incl. the WS-injected request.
await shot(mobile, '/approvals', 'approvals.png', { waitMs: APPROVALS_WAIT_MS });
// Session 1 is the subagent fixture. Chat is the default tab now.
await shot(mobile, '/sessions/1', 'session-detail-chat.png');
// Timeline via ?tab= deep-link; the panel scrolls under the pinned header.
await shot(mobile, '/sessions/1?tab=timeline', 'session-detail-chips.png');
await scrollTabPanel(mobile, 400);
await mobile.screenshot({ path: join(outDir, 'session-detail-timeline.png') });
console.log('✓ session-detail-timeline.png');
await shot(mobile, '/sessions/1?tab=diffs', 'session-detail-diffs.png');
await mobile.close();

// Desktop (≥1280px): full-width header bar, sidebar below, right rails.
const desktop = await browser.newPage({ viewport: { width: 1440, height: 900 } });
await shot(desktop, '/', 'overview-desktop.png');
// Sessions table (≥900px): dropdown + status-count chips + aligned columns.
await shot(desktop, '/sessions', 'sessions-desktop.png');
await shot(desktop, '/approvals', 'approvals-desktop.png', { waitMs: APPROVALS_WAIT_MS });
// Detail with the timeline scrolled — header block and rail stay pinned.
await desktop.goto(`${base}/sessions/1?tab=timeline`);
await settle(desktop);
await scrollTabPanel(desktop, 600);
await desktop.screenshot({ path: join(outDir, 'session-detail-desktop.png') });
await assertNoHorizontalScroll(desktop, '/sessions/1?tab=timeline');
console.log('✓ session-detail-desktop.png');
await shot(desktop, '/docs/neutrality', 'docs.png');
// Fill-route invariant, one route for now. /docs at this viewport is the only
// fill route where it already holds: the shell hands over the scroll and the
// docs INDEX happens to fit 1440×900 — the page is not structurally fill-ready
// yet (its own scroller lands in the Docs phase), and the embedded map/graph
// pages get theirs in their own phases. Extending this call to every fill route
// is the sweep phase's job; until then it guards the shell contract on the one
// route that can prove it.
await desktop.goto(`${base}/docs`);
await settle(desktop);
await assertShellScroll(desktop, '/docs', 'hidden');
await assertNoNestedVerticalScroll(desktop, '/docs');
// Control: a non-fill route must keep its shell scroller, so the pair above is
// discriminating rather than true everywhere.
await desktop.goto(`${base}/sessions`);
await settle(desktop);
await assertShellScroll(desktop, '/sessions', 'auto');
await desktop.close();

await browser.close();
