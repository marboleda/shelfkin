// Use Node's fetch in tests so requests reach MSW (see jest/environment.js).
// Runs after jest-expo's setup, which installs Expo's native fetch.
const nodeFetch = globalThis.__NODE_FETCH__;

for (const name of Object.keys(nodeFetch)) {
  Object.defineProperty(globalThis, name, {
    value: nodeFetch[name],
    configurable: true,
    enumerable: true,
    writable: true,
  });
}
