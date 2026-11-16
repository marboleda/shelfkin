// A stand-in for expo-device in tests (mapped in jest.config.js). Tests run as
// a physical phone unless they call `mockDevice.setIsDevice(false)` (a
// simulator). jest/setup-push.js resets it before every test.

// eslint-disable-next-line import/no-mutable-exports
export let isDevice = true;

export const mockDevice = {
  setIsDevice(value: boolean) {
    isDevice = value;
  },
  reset() {
    isDevice = true;
  },
};
