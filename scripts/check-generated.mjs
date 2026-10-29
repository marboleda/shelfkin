// Run by `npm test`: fails if the committed generated code does not match
// what `npm run generate` would produce from openapi/openapi.yaml.

import { GenerateError, findStale, formatStale } from './codegen.mjs';

try {
  const stale = findStale();
  if (stale.length > 0) {
    console.error(formatStale(stale));
    process.exitCode = 1;
  } else {
    console.log('Generated code is up to date.');
  }
} catch (err) {
  if (!(err instanceof GenerateError)) throw err;
  console.error(err.message);
  process.exitCode = 1;
}
