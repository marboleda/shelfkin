// Code generation from the OpenAPI spec, shared by `npm run generate`
// (scripts/generate.mjs) and the out-of-date check run by `npm test`
// (scripts/check-generated.mjs).
//
// Plain Node with no shell, so it behaves the same on Windows and Linux.
// Generators always write into a fresh temporary directory first. Nothing in
// the repository is touched unless every generator succeeds.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const SPEC = path.join(ROOT, 'openapi', 'openapi.yaml');

const API_DIR = path.join(ROOT, 'api');
const OAPI_CODEGEN_CONFIG = path.join(API_DIR, 'oapi-codegen.yaml');
const OPENAPI_TS_CLI = path.join(ROOT, 'app', 'node_modules', 'openapi-typescript', 'bin', 'cli.js');

export class GenerateError extends Error {}

function run(label, command, args, cwd) {
  const result = spawnSync(command, args, { cwd, encoding: 'utf8' });
  if (result.error) {
    throw new GenerateError(`${label}: could not run ${command}: ${result.error.message}`);
  }
  if (result.status !== 0) {
    const output = `${result.stderr ?? ''}${result.stdout ?? ''}`.trim();
    throw new GenerateError(`${label} failed (exit code ${result.status}):\n${output}`);
  }
}

/**
 * Every generated file, with the generator that produces it. `file` is
 * relative to the repository root, with forward slashes.
 */
export const TARGETS = [
  {
    file: 'api/internal/openapi/openapi.gen.go',
    generate(spec, out) {
      // `go tool` runs the oapi-codegen version pinned by the tool directive in api/go.mod.
      run('oapi-codegen', 'go', ['tool', 'oapi-codegen', '-config', OAPI_CODEGEN_CONFIG, '-o', out, spec], API_DIR);
    },
  },
  {
    file: 'app/src/api/schema.ts',
    generate(spec, out) {
      // openapi-typescript is an exact devDependency of the app.
      if (!fs.existsSync(OPENAPI_TS_CLI)) {
        throw new GenerateError(
          'openapi-typescript is not installed. Run `npm --prefix app install` first.',
        );
      }
      run('openapi-typescript', process.execPath, [OPENAPI_TS_CLI, spec, '-o', out], ROOT);
    },
  },
];

function withTempDir(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'shelfkin-codegen-'));
  try {
    return fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

/**
 * Runs every generator on `spec` into `dir` (laid out like the repository)
 * and returns the generated contents keyed by target file. Line endings are
 * normalised to LF to match .gitattributes.
 */
function generateInto(spec, dir) {
  const outputs = new Map();
  for (const target of TARGETS) {
    const out = path.join(dir, ...target.file.split('/'));
    fs.mkdirSync(path.dirname(out), { recursive: true });
    target.generate(path.resolve(spec), out);
    if (!fs.existsSync(out)) {
      throw new GenerateError(`${target.file}: the generator exited successfully but wrote no file`);
    }
    outputs.set(target.file, fs.readFileSync(out, 'utf8').replace(/\r\n/g, '\n'));
  }
  return outputs;
}

/**
 * Regenerates every target from `spec` into `root`. Throws GenerateError,
 * leaving `root` untouched, if any generator fails. Returns the files written.
 */
export function generate(spec = SPEC, root = ROOT) {
  const outputs = withTempDir((dir) => generateInto(spec, dir));
  for (const [file, content] of outputs) {
    const dest = path.join(root, ...file.split('/'));
    fs.mkdirSync(path.dirname(dest), { recursive: true });
    // Write next to the destination, then rename, so an interrupted run
    // cannot leave a half-written file in place.
    const partial = `${dest}.partial`;
    fs.writeFileSync(partial, content);
    fs.renameSync(partial, dest);
  }
  return [...outputs.keys()];
}

/**
 * Regenerates every target from `spec` into a temporary directory and
 * compares the result with the files under `root`. Does not use git and does
 * not write under `root`. Returns `[{ file, problem }]`, where problem is
 * 'missing' or 'out of date'; an empty array means everything is current.
 */
export function findStale(spec = SPEC, root = ROOT) {
  const outputs = withTempDir((dir) => generateInto(spec, dir));
  const stale = [];
  for (const [file, expected] of outputs) {
    const actualPath = path.join(root, ...file.split('/'));
    if (!fs.existsSync(actualPath)) {
      stale.push({ file, problem: 'missing' });
    } else if (fs.readFileSync(actualPath, 'utf8') !== expected) {
      stale.push({ file, problem: 'out of date' });
    }
  }
  return stale;
}

export function formatStale(stale) {
  return [
    'Generated code does not match openapi/openapi.yaml:',
    ...stale.map(({ file, problem }) => `  ${file} (${problem})`),
    '',
    'Run `npm run generate` from the repository root and commit the result.',
    'Generated files must not be edited by hand.',
  ].join('\n');
}
