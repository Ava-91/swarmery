#!/usr/bin/env node
// ensure-runtime.mjs — idempotent CLI wrapper around the runtime cache.
//
// First run downloads the pinned browser and packages into the pack's private
// cache; every later run is a read-only offline verification that finishes in
// milliseconds. Run it once while online to make the other scripts usable on a
// machine that is later offline.
//
// Exit codes: 0 prepared | 2 setup failed | 3 not prepared and unreachable.

import {
  EXIT,
  PINS,
  cacheDir,
  directorySize,
  ensureRuntime,
  humanBytes,
  inspectRuntime,
  parseArgs,
} from './runtime.mjs';

const USAGE = `usage: ensure-runtime.mjs [--quiet] [--check]

  --quiet   suppress the package manager and browser installer output
  --check   report cache state without installing anything (exit 3 if incomplete)`;

const args = parseArgs(process.argv.slice(2), {
  '--quiet': { type: 'flag' },
  '--check': { type: 'flag' },
}, USAGE);

const dir = cacheDir();

if (args.check) {
  const state = inspectRuntime(dir);
  process.stdout.write(`cache:    ${dir}\n`);
  process.stdout.write(`prepared: ${state.prepared ? 'yes' : 'no'}\n`);
  if (!state.prepared) {
    for (const item of state.missing) process.stdout.write(`  - ${item}\n`);
    process.exit(EXIT.NOT_PREPARED);
  }
  process.exit(EXIT.OK);
}

const before = inspectRuntime(dir);
const startedAt = Date.now();
const runtime = await ensureRuntime({ quiet: args.quiet });
const elapsed = (Date.now() - startedAt) / 1000;

const pins = Object.entries(PINS)
  .map(([name, version]) => `${name}@${version}`)
  .join(', ');

process.stdout.write(`design-pack runtime: ${runtime.installed ? 'prepared' : 'already prepared'}\n`);
process.stdout.write(`  cache:     ${runtime.cacheDir}\n`);
process.stdout.write(`  browsers:  ${runtime.browsersPath}\n`);
process.stdout.write(`  pins:      ${pins}\n`);
process.stdout.write(`  node:      ${process.version}\n`);
process.stdout.write(`  preparedAt:${runtime.preparedAt ? ` ${runtime.preparedAt}` : ' unknown'}\n`);

if (runtime.installed) {
  process.stdout.write(`  was missing: ${before.missing.join('; ')}\n`);
  process.stdout.write(`  packages:  ${humanBytes(directorySize(`${runtime.cacheDir}/node_modules`))}\n`);
  process.stdout.write(`  browser:   ${humanBytes(directorySize(runtime.browsersPath))}\n`);
  process.stdout.write(`  duration:  ${elapsed.toFixed(1)}s\n`);
} else {
  process.stdout.write(`  checked in ${elapsed.toFixed(2)}s without network access\n`);
}

process.exit(EXIT.OK);
