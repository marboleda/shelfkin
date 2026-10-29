// Tests for scripts/codegen.mjs. Run with `npm run test:codegen`.
// Each test works on copies in a temporary directory, never on the
// committed files.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, test } from 'node:test';

import { DIR_TARGETS, GenerateError, ROOT, SPEC, TARGETS, findStale, formatStale, generate } from './codegen.mjs';

const GO_FILE = 'api/internal/openapi/openapi.gen.go';
const TS_FILE = 'app/src/api/schema.ts';
const DB_DIR = 'api/internal/db';
const DB_FILES = ['db.go', 'health.sql.go', 'models.go'].map((name) => `${DB_DIR}/${name}`);
const ALL_FILES = [GO_FILE, TS_FILE, ...DB_FILES];
const specText = fs.readFileSync(SPEC, 'utf8');

let tmp;

beforeEach(() => {
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'shelfkin-codegen-test-'));
});

afterEach(() => {
  fs.rmSync(tmp, { recursive: true, force: true });
});

const at = (root, file) => path.join(root, ...file.split('/'));
const read = (root, file) => fs.readFileSync(at(root, file), 'utf8');

/** Copies the committed generated files into a fake repository root. */
function copyGenerated() {
  const root = path.join(tmp, 'repo');
  for (const { file } of TARGETS) {
    fs.mkdirSync(path.dirname(at(root, file)), { recursive: true });
    fs.copyFileSync(at(ROOT, file), at(root, file));
  }
  for (const { dir } of DIR_TARGETS) {
    fs.cpSync(at(ROOT, dir), at(root, dir), { recursive: true });
  }
  return root;
}

const readAll = (root) => ALL_FILES.map((file) => read(root, file));

function writeSpec(text) {
  const spec = path.join(tmp, 'openapi.yaml');
  fs.writeFileSync(spec, text);
  return spec;
}

// Adds a GET /ping endpoint, so both generators produce different output.
const specWithPing = () =>
  writeSpec(
    specText.replace(
      'paths:\n',
      "paths:\n  /ping:\n    get:\n      operationId: getPing\n      security: []\n      responses:\n        '204':\n          description: Pong.\n",
    ),
  );

describe('findStale', () => {
  test('the committed generated files are up to date', () => {
    assert.deepEqual(findStale(), []);
  });

  test('reports every output when the spec changed but was not regenerated', () => {
    const spec = specWithPing();
    assert.deepEqual(findStale(spec, ROOT), [
      { file: GO_FILE, problem: 'out of date' },
      { file: TS_FILE, problem: 'out of date' },
    ]);
  });

  test('reports a generated file that was edited by hand', () => {
    const root = copyGenerated();
    fs.appendFileSync(at(root, TS_FILE), 'export type Extra = string;\n');
    assert.deepEqual(findStale(SPEC, root), [{ file: TS_FILE, problem: 'out of date' }]);
  });

  test('reports a generated file with CRLF line endings', () => {
    const root = copyGenerated();
    fs.writeFileSync(at(root, GO_FILE), read(root, GO_FILE).replace(/\n/g, '\r\n'));
    assert.deepEqual(findStale(SPEC, root), [{ file: GO_FILE, problem: 'out of date' }]);
  });

  test('reports a generated file that was deleted', () => {
    const root = copyGenerated();
    fs.rmSync(at(root, GO_FILE));
    assert.deepEqual(findStale(SPEC, root), [{ file: GO_FILE, problem: 'missing' }]);
  });

  test('reports a sqlc file that was edited by hand or deleted', () => {
    const root = copyGenerated();
    fs.appendFileSync(at(root, DB_FILES[1]), '// edited\n');
    fs.rmSync(at(root, DB_FILES[2]));
    assert.deepEqual(findStale(SPEC, root), [
      { file: DB_FILES[1], problem: 'out of date' },
      { file: DB_FILES[2], problem: 'missing' },
    ]);
  });

  test('reports a file in the sqlc output directory that sqlc does not generate', () => {
    const root = copyGenerated();
    fs.writeFileSync(at(root, `${DB_DIR}/old.sql.go`), 'package db\n');
    assert.deepEqual(findStale(SPEC, root), [{ file: `${DB_DIR}/old.sql.go`, problem: 'not generated' }]);
  });

  test('does not modify the files it checks', () => {
    const root = copyGenerated();
    fs.rmSync(at(root, GO_FILE));
    fs.writeFileSync(at(root, TS_FILE), 'edited\n');
    findStale(specWithPing(), root);
    assert.equal(fs.existsSync(at(root, GO_FILE)), false);
    assert.equal(read(root, TS_FILE), 'edited\n');
    assert.deepEqual(fs.readdirSync(path.dirname(at(root, TS_FILE))), ['schema.ts']);
  });
});

describe('formatStale', () => {
  test('names the stale files and says to run npm run generate', () => {
    const message = formatStale([
      { file: GO_FILE, problem: 'out of date' },
      { file: TS_FILE, problem: 'missing' },
    ]);
    assert.match(message, /api\/internal\/openapi\/openapi\.gen\.go \(out of date\)/);
    assert.match(message, /app\/src\/api\/schema\.ts \(missing\)/);
    assert.match(message, /Run `npm run generate`/);
  });
});

describe('generate', () => {
  test('writes every target with a do-not-edit header and LF line endings', () => {
    const root = path.join(tmp, 'repo');
    assert.deepEqual(generate(SPEC, root), ALL_FILES);

    const go = read(root, GO_FILE);
    const ts = read(root, TS_FILE);
    assert.match(go.split('\n').slice(0, 6).join('\n'), /^\/\/ Code generated .* DO NOT EDIT\.$/m);
    assert.match(go, /^package openapi$/m);
    assert.match(go, /type StrictServerInterface interface/);
    assert.match(ts.split('\n').slice(0, 4).join('\n'), /auto-generated by openapi-typescript[\s\S]*Do not make direct changes/);
    assert.equal(go.includes('\r'), false);
    assert.equal(ts.includes('\r'), false);

    for (const file of DB_FILES) {
      const db = read(root, file);
      assert.match(db.split('\n').slice(0, 3).join('\n'), /^\/\/ Code generated by sqlc\. DO NOT EDIT\.$/m);
      assert.match(db, /^package db$/m);
      assert.equal(db.includes('\r'), false);
    }
    assert.match(read(root, DB_FILES[1]), /func \(q \*Queries\) Ping\(ctx context\.Context\)/);
  });

  test('removes files from the sqlc output directory that sqlc no longer generates', () => {
    const root = copyGenerated();
    fs.writeFileSync(at(root, `${DB_DIR}/old.sql.go`), 'package db\n');
    generate(SPEC, root);
    assert.deepEqual(fs.readdirSync(at(root, DB_DIR)).sort(), ['db.go', 'health.sql.go', 'models.go']);
  });

  test('is deterministic and matches the committed files', () => {
    const root = path.join(tmp, 'repo');
    generate(SPEC, root);
    const first = readAll(root);
    generate(SPEC, root);
    const second = readAll(root);
    assert.deepEqual(second, first);
    assert.deepEqual(first, readAll(ROOT));
  });

  test('fails on an invalid spec without touching existing output', () => {
    const root = copyGenerated();
    const before = readAll(root);
    // Break a $ref.
    const spec = writeSpec(specText.replace("'#/components/schemas/Health'", "'#/components/schemas/Missing'"));

    assert.throws(() => generate(spec, root), (err) => {
      assert.ok(err instanceof GenerateError);
      assert.match(err.message, /oapi-codegen failed/);
      assert.match(err.message, /Missing/);
      return true;
    });
    assert.deepEqual(readAll(root), before);
    assert.deepEqual(fs.readdirSync(path.dirname(at(root, GO_FILE))), ['openapi.gen.go']);
  });

  test('the TypeScript generator also rejects a broken $ref with its own error', () => {
    const spec = writeSpec(specText.replace("'#/components/schemas/Health'", "'#/components/schemas/Missing'"));
    const ts = TARGETS.find(({ file }) => file === TS_FILE);
    assert.throws(() => ts.generate(spec, path.join(tmp, 'schema.ts')), (err) => {
      assert.ok(err instanceof GenerateError);
      assert.match(err.message, /openapi-typescript failed/);
      assert.match(err.message, /Can't resolve \$ref/);
      return true;
    });
  });

  test('writes nothing when a later generator fails', () => {
    const root = copyGenerated();
    const before = readAll(root);
    const last = DIR_TARGETS.at(-1);
    const original = last.generate;
    last.generate = () => {
      throw new GenerateError('simulated failure');
    };
    try {
      assert.throws(() => generate(specWithPing(), root), /simulated failure/);
    } finally {
      last.generate = original;
    }
    // The earlier generators succeeded on the changed spec, but none of their output was written.
    assert.deepEqual(readAll(root), before);
  });
});
