// Code generation, shared by `npm run generate` (scripts/generate.mjs) and the
// out-of-date check run by `npm test` (scripts/check-generated.mjs). It
// generates the Go server interface and the app's TypeScript types from the
// OpenAPI spec, and the Go database code from the SQL queries with sqlc.
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
// sqlc's inputs, relative to api/. They must match api/sqlc.yaml.
const SQLC_INPUTS = ['sqlc.yaml', path.join('db', 'migrations'), path.join('db', 'queries')];
// The `out` directory in api/sqlc.yaml.
const SQLC_OUT = path.join('internal', 'db');

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
 * relative to the repository root, with forward slashes. `generate(spec, out)`
 * writes the file to `out`.
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

/**
 * Generated directories: every file in `dir` (relative to the repository
 * root, forward slashes) is generated, and a file there that the generator
 * no longer produces is stale. `generate(outDir)` writes the files to
 * `outDir`. These do not depend on the OpenAPI spec.
 */
export const DIR_TARGETS = [
  {
    dir: 'api/internal/db',
    generate(outDir) {
      // sqlc writes to the `out` directory next to its config, so run it on a
      // copy of its inputs. `go tool` runs the sqlc version pinned by the tool
      // directive in api/go.mod; it needs no cgo, so it also runs on Windows.
      withTempDir((staging) => {
        for (const input of SQLC_INPUTS) {
          fs.cpSync(path.join(API_DIR, input), path.join(staging, input), { recursive: true });
        }
        run('sqlc', 'go', ['tool', 'sqlc', 'generate', '-f', path.join(staging, 'sqlc.yaml')], API_DIR);
        fs.cpSync(path.join(staging, SQLC_OUT), outDir, { recursive: true });
      });
    },
  },
];

/** The files directly in `dir` (an absolute path), sorted; [] if it does not exist. */
function listFiles(dir) {
  if (!fs.existsSync(dir)) return [];
  return fs
    .readdirSync(dir, { withFileTypes: true })
    .filter((entry) => entry.isFile())
    .map((entry) => entry.name)
    .sort();
}

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
 * and returns the generated contents keyed by file. Line endings are
 * normalised to LF to match .gitattributes.
 */
function generateInto(spec, dir) {
  const outputs = new Map();
  const add = (file, out) => outputs.set(file, fs.readFileSync(out, 'utf8').replace(/\r\n/g, '\n'));
  for (const target of TARGETS) {
    const out = path.join(dir, ...target.file.split('/'));
    fs.mkdirSync(path.dirname(out), { recursive: true });
    target.generate(path.resolve(spec), out);
    if (!fs.existsSync(out)) {
      throw new GenerateError(`${target.file}: the generator exited successfully but wrote no file`);
    }
    add(target.file, out);
  }
  for (const target of DIR_TARGETS) {
    const outDir = path.join(dir, ...target.dir.split('/'));
    fs.mkdirSync(outDir, { recursive: true });
    target.generate(outDir);
    const names = listFiles(outDir);
    if (names.length === 0) {
      throw new GenerateError(`${target.dir}: the generator exited successfully but wrote no files`);
    }
    for (const name of names) {
      add(`${target.dir}/${name}`, path.join(outDir, name));
    }
  }
  return outputs;
}

/** Files in generated directories under `root` that no generator produced. */
function findExtra(outputs, root) {
  const extra = [];
  for (const target of DIR_TARGETS) {
    for (const name of listFiles(path.join(root, ...target.dir.split('/')))) {
      const file = `${target.dir}/${name}`;
      if (!outputs.has(file)) extra.push(file);
    }
  }
  return extra;
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
  // A generated directory holds only what its generator produced, for
  // example after a query file was deleted.
  for (const file of findExtra(outputs, root)) {
    fs.rmSync(path.join(root, ...file.split('/')));
  }
  return [...outputs.keys()];
}

/**
 * Regenerates every target from `spec` into a temporary directory and
 * compares the result with the files under `root`. Does not use git and does
 * not write under `root`. Returns `[{ file, problem }]`, where problem is
 * 'missing', 'out of date' or 'not generated' (a file in a generated
 * directory that no generator produces); an empty array means everything is
 * current.
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
  for (const file of findExtra(outputs, root)) {
    stale.push({ file, problem: 'not generated' });
  }
  return stale;
}

export function formatStale(stale) {
  return [
    'Generated code does not match openapi/openapi.yaml or the sqlc queries:',
    ...stale.map(({ file, problem }) => `  ${file} (${problem})`),
    '',
    'Run `npm run generate` from the repository root and commit the result.',
    'Generated files must not be edited by hand.',
  ].join('\n');
}
