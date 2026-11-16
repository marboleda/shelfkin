// Jest uses the jest-expo preset. Two additions let MSW (which mocks the API in
// tests) load: MSW and its dependencies ship as ES modules, so they are
// compiled to CommonJS like Expo's own packages, including their `.mjs` files.
const expoPreset = require('jest-expo/jest-preset');

// Stop `expo` from installing its native fetch when a test imports it; tests use
// Node's fetch instead (see jest/environment.js). Set here so test workers inherit it.
process.env.EXPO_PUBLIC_USE_RN_FETCH = '1';

const ESM_PACKAGES = [
  // From jest-expo's default transformIgnorePatterns.
  '.pnpm',
  'react-native',
  '@react-native',
  '@react-native-community',
  'expo',
  '@expo',
  '@expo-google-fonts',
  'react-navigation',
  '@react-navigation',
  '@sentry/react-native',
  'native-base',
  'standard-navigation',
  // MSW and its ES module dependencies.
  'msw',
  '@msw',
  '@mswjs',
  'rettime',
  'until-async',
  'outvariant',
  'headers-polyfill',
  'is-node-process',
  'strict-event-emitter',
  '@open-draft',
  'tough-cookie',
  'tldts',
  'tldts-core',
  'cookie',
  'type-fest',
];

/** @type {import('jest').Config} */
module.exports = {
  preset: 'jest-expo',
  testEnvironment: '<rootDir>/jest/environment.js',
  setupFilesAfterEnv: ['<rootDir>/jest/setup-fetch.js', '<rootDir>/jest/setup-auth.js', '<rootDir>/jest/setup-push.js'],
  // Tests never reach Firebase, the device's storage or its notifications:
  // these in-memory fakes stand in for them (see jest/firebase-auth-mock.ts and
  // jest/expo-notifications-mock.ts for the test controls).
  moduleNameMapper: {
    '^firebase/auth$': '<rootDir>/jest/firebase-auth-mock.ts',
    '^firebase/app$': '<rootDir>/jest/firebase-app-mock.ts',
    '^@react-native-async-storage/async-storage$':
      '@react-native-async-storage/async-storage/jest/async-storage-mock',
    '^expo-notifications$': '<rootDir>/jest/expo-notifications-mock.ts',
    '^expo-device$': '<rootDir>/jest/expo-device-mock.ts',
  },
  // expo-router's renderRouter() turns on fake timers. Leave Node's I/O
  // scheduling real so Node's fetch and MSW can still answer requests.
  fakeTimers: { doNotFake: ['nextTick', 'setImmediate', 'queueMicrotask'] },
  transform: {
    // Compile `.mjs` files with the same Babel setup jest-expo uses for `.js`.
    '\\.mjs$': expoPreset.transform['\\.[jt]sx?$'],
  },
  transformIgnorePatterns: [
    `/node_modules/(?!(${ESM_PACKAGES.join('|')}))`,
    '/node_modules/react-native-reanimated/plugin/',
    '/node_modules/@react-native/babel-preset/',
  ],
};
