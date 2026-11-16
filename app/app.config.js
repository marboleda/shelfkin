// The dynamic part of the app config (#41). Everything static lives in
// app.json; Expo passes it in as `config`, and this file only adds what has to
// come from EAS environment variables at build time.
//
// - android.googleServicesFile: google-services.json is never committed. On
//   EAS it is the file environment variable GOOGLE_SERVICES_JSON (a path on the
//   build server). Locally, an uncommitted ./google-services.json is used if
//   present, so `npx expo prebuild` / `npx expo run:android` still work.
// - Preview and production builds on EAS fail at the start, instead of
//   producing an app that only shows "App not configured", when the API URL or
//   the production Firebase web config is missing, or the Auth emulator is set.
//   See "Mobile releases" in the repository README.
const { existsSync } = require('fs');
const { join } = require('path');

/** EAS environment variables a preview or production build must have. */
const RELEASE_VARS = [
  'EXPO_PUBLIC_API_URL',
  'EXPO_PUBLIC_FIREBASE_API_KEY',
  'EXPO_PUBLIC_FIREBASE_AUTH_DOMAIN',
  'EXPO_PUBLIC_FIREBASE_PROJECT_ID',
];
const RELEASE_PROFILES = ['preview', 'production'];
const LOCAL_GOOGLE_SERVICES = './google-services.json';

function isSet(value) {
  return typeof value === 'string' && value.trim() !== '';
}

/** Path of google-services.json for this build, or undefined when there is none. */
function googleServicesFile(env, fileExists) {
  if (isSet(env.GOOGLE_SERVICES_JSON)) return env.GOOGLE_SERVICES_JSON;
  return fileExists(LOCAL_GOOGLE_SERVICES) ? LOCAL_GOOGLE_SERVICES : undefined;
}

/**
 * What is wrong with the environment of this build. Only checked on EAS build
 * servers (EAS_BUILD=true) for the preview and production profiles: EAS CLI
 * also reads this file on your computer, where the EAS variables are not all
 * loaded, and development builds use the local API and Auth emulator.
 */
function releaseEnvErrors(env) {
  if (env.EAS_BUILD !== 'true' || !RELEASE_PROFILES.includes(env.EAS_BUILD_PROFILE)) return [];

  const where = `the EAS "${env.EAS_BUILD_PROFILE}" environment`;
  const errors = RELEASE_VARS.filter((name) => !isSet(env[name])).map((name) => `${name} is not set in ${where}.`);
  if (isSet(env.EXPO_PUBLIC_FIREBASE_AUTH_EMULATOR_HOST)) {
    errors.push(`EXPO_PUBLIC_FIREBASE_AUTH_EMULATOR_HOST must not be set in ${where}.`);
  }
  if (env.EAS_BUILD_PLATFORM === 'android' && !isSet(env.GOOGLE_SERVICES_JSON)) {
    errors.push(`The GOOGLE_SERVICES_JSON file variable is not set in ${where}; push notifications need it.`);
  }
  return errors;
}

/** app.json's config plus the build-time values above. */
function withBuildEnv(config, env, fileExists) {
  const errors = releaseEnvErrors(env);
  if (errors.length > 0) {
    throw new Error(`This build is missing its configuration (see "Mobile releases" in README.md):\n- ${errors.join('\n- ')}`);
  }

  const file = googleServicesFile(env, fileExists);
  if (!file) return config;
  return { ...config, android: { ...config.android, googleServicesFile: file } };
}

module.exports = ({ config }) => withBuildEnv(config, process.env, (file) => existsSync(join(__dirname, file)));
module.exports.withBuildEnv = withBuildEnv;
module.exports.releaseEnvErrors = releaseEnvErrors;
module.exports.RELEASE_VARS = RELEASE_VARS;
