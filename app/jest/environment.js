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
    // Sets the time zone of the test process (`process.env.TZ` inside a test is a
    // copy and has no effect). Node applies a TZ change at once, to every Date
    // and Intl call in the process. Returns the previous value, to restore it.
    this.global.__SET_TIME_ZONE__ = (zone) => {
      const previous = process.env.TZ;
      if (zone === undefined) delete process.env.TZ;
      else process.env.TZ = zone;
      return previous;
    };
  }
}

module.exports = AppTestEnvironment;
module.exports.TestEnvironment = AppTestEnvironment;
