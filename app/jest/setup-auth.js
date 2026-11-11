// Every test starts signed out, with empty device storage and no sign-out tasks
// left over from an earlier test. Tests that need a user call signInTestUser()
// from test-utils/auth.ts.
const AsyncStorage = require('@react-native-async-storage/async-storage');
const { mockFirebase } = require('./firebase-auth-mock');

beforeEach(async () => {
  mockFirebase.reset();
  require('@/auth/session').resetAuthSessionForTests();
  await AsyncStorage.clear();
});
