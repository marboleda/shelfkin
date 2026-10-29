// jest-expo's React Native test environment, plus a handle on Node's own fetch.
//
// In Jest, `globalThis.fetch` is Expo's native fetch (`expo/fetch`), backed by a
// stub native module, so no request can complete and MSW cannot answer it.
// This environment keeps Node's fetch classes (from the realm Jest runs in) so
// `jest/setup-fetch.js` can put them back before each test file.
const ReactNativeEnvironment = require('jest-expo/src/preset/nativeEnvironment');

class AppTestEnvironment extends ReactNativeEnvironment {
  constructor(config, context) {
    super(config, context);
    this.global.__NODE_FETCH__ = { fetch, Request, Response, Headers, AbortController, AbortSignal };
  }
}

module.exports = AppTestEnvironment;
module.exports.TestEnvironment = AppTestEnvironment;
