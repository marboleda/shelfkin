// Stand-in for `firebase/app` in tests (see jest.config.js).
const apps: { name: string; options: unknown }[] = [];

export const initializeApp = jest.fn((options: unknown) => {
  const app = { name: '[DEFAULT]', options };
  apps.push(app);
  return app;
});

export const getApps = jest.fn(() => apps);
export const getApp = jest.fn(() => apps[0]);
