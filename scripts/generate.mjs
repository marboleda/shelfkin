// `npm run generate`: regenerates the Go server interface and the app's
// TypeScript types from openapi/openapi.yaml.

import { GenerateError, generate } from './codegen.mjs';

try {
  for (const file of generate()) {
    console.log(`generated ${file}`);
  }
} catch (err) {
  if (!(err instanceof GenerateError)) throw err;
  console.error(err.message);
  console.error('\nNothing was written; the generated files are unchanged.');
  process.exitCode = 1;
}
