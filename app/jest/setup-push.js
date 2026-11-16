// Every test starts on a physical phone with notifications not yet decided,
// Expo returning the same push token, no taps on reminders and no push work
// left over from an earlier test. The build has an EAS project ID, so Expo can
// issue a token (tests that need it missing remove it).
const Constants = require('expo-constants').default;
const { mockNotifications } = require('./expo-notifications-mock');
const { mockDevice } = require('./expo-device-mock');

const TEST_PROJECT_ID = '00000000-0000-4000-8000-000000000034';

beforeEach(() => {
  mockNotifications.reset();
  mockDevice.reset();
  // `expoConfig` is a getter that always returns the same object, so it is changed in place.
  Constants.expoConfig.extra = { ...Constants.expoConfig.extra, eas: { projectId: TEST_PROJECT_ID } };
  require('@/push/registration').resetPushQueueForTests();
  require('@/push/taps').resetHandledRemindersForTests();
});
