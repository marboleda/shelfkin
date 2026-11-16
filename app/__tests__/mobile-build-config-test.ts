// Mobile build configuration (#41): EAS build profiles, the native app
// config in app.json, and app.config.js, which adds the values that come
// from EAS environment variables at build time.

// The app's tsconfig has no Node types, so these are typed here.
const { readFileSync } = jest.requireActual<{
  readFileSync(path: string): Uint8Array;
  readFileSync(path: string, encoding: 'utf8'): string;
}>('fs');
const { dirname, join } = jest.requireActual<{ dirname(path: string): string; join(...parts: string[]): string }>('path');

type Env = Record<string, string | undefined>;
type ExpoConfig = Record<string, any>;

const APP_DIR = join(dirname(expect.getState().testPath ?? ''), '..');
const appJson = jest.requireActual('../app.json').expo as ExpoConfig;
const easJson = jest.requireActual('../eas.json') as {
  cli: Record<string, string>;
  build: Record<string, Record<string, any>>;
  submit: Record<string, Record<string, any>>;
};
const appConfig = jest.requireActual('../app.config.js') as {
  (args: { config: ExpoConfig }): ExpoConfig;
  withBuildEnv(config: ExpoConfig, env: Env, fileExists: (path: string) => boolean): ExpoConfig;
  releaseEnvErrors(env: Env): string[];
  RELEASE_VARS: string[];
};

const APP_ID = 'com.marboleda.shelfkin';
const RELEASE_ENV: Env = {
  EXPO_PUBLIC_API_URL: 'https://shelfkin-api-abc-uc.a.run.app',
  EXPO_PUBLIC_FIREBASE_API_KEY: 'AIzaTest',
  EXPO_PUBLIC_FIREBASE_AUTH_DOMAIN: 'shelfkin-514420.firebaseapp.com',
  EXPO_PUBLIC_FIREBASE_PROJECT_ID: 'shelfkin-514420',
};
const noFile = () => false;

describe('eas.json', () => {
  test('build numbers come from EAS (remote version source)', () => {
    expect(easJson.cli.appVersionSource).toBe('remote');
    expect(easJson.cli.version).toMatch(/^>= \d+\.\d+\.\d+$/);
  });

  test('has exactly the development, preview and production profiles', () => {
    expect(Object.keys(easJson.build).sort()).toEqual(['development', 'preview', 'production']);
  });

  test('development: a development client, installed directly, using the EAS development environment', () => {
    const profile = easJson.build.development;
    expect(profile.developmentClient).toBe(true);
    expect(profile.distribution).toBe('internal');
    expect(profile.environment).toBe('development');
  });

  test('development: expo-dev-client is a dependency, so the profile builds with --non-interactive', () => {
    // Without it, eas build stops ("Install expo-dev-client manually") or
    // installs it interactively and leaves package.json changed.
    const packageJson = jest.requireActual('../package.json') as { dependencies: Record<string, string> };
    expect(packageJson.dependencies['expo-dev-client']).toMatch(/^~\d+\.\d+\.\d+$/);
  });

  test('preview: an internal build without the development client, using the EAS preview environment', () => {
    const profile = easJson.build.preview;
    expect(profile.developmentClient).toBeUndefined();
    expect(profile.distribution).toBe('internal');
    expect(profile.environment).toBe('preview');
    // An APK installs straight from the build page; an AAB only through Play.
    expect(profile.android.buildType).toBe('apk');
  });

  test('production: store builds with build numbers incremented remotely', () => {
    const profile = easJson.build.production;
    expect(profile.developmentClient).toBeUndefined();
    expect(profile.distribution).toBe('store');
    expect(profile.environment).toBe('production');
    expect(profile.autoIncrement).toBe(true);
    // Store builds must be an AAB, the default; an APK cannot go to Play.
    expect(profile.android?.buildType).toBeUndefined();
  });

  test('eas submit sends Android builds to the internal testing track', () => {
    expect(easJson.submit.production.android.track).toBe('internal');
  });

  test('no profile commits app configuration: it comes from EAS environment variables', () => {
    for (const profile of Object.values(easJson.build)) {
      expect(profile.env).toBeUndefined();
      for (const platform of ['ios', 'android']) expect(profile[platform]?.env).toBeUndefined();
    }
    expect(JSON.stringify(easJson)).not.toMatch(/EXPO_PUBLIC_|GOOGLE_SERVICES|AIza|run\.app/);
  });
});

describe('app.json', () => {
  test('bundle identifier and package are com.marboleda.shelfkin, version 1.0.0', () => {
    expect(appJson.ios.bundleIdentifier).toBe(APP_ID);
    expect(appJson.android.package).toBe(APP_ID);
    expect(appJson.version).toBe('1.0.0');
    // Build numbers live in EAS (appVersionSource: remote), not here.
    expect(appJson.ios.buildNumber).toBeUndefined();
    expect(appJson.android.versionCode).toBeUndefined();
  });

  test('declares iOS export compliance: only standard (exempt) encryption', () => {
    expect(appJson.ios.config.usesNonExemptEncryption).toBe(false);
  });

  test('orientation is not locked, because tablets are supported and rotate (#23)', () => {
    // iPad multitasking requires every orientation; a portrait lock with
    // supportsTablet is rejected by App Store Connect unless the app opts out
    // of Split View with requireFullScreen.
    expect(appJson.ios.supportsTablet).toBe(true);
    expect(appJson.orientation).toBe('default');
    expect(appJson.ios.requireFullScreen).toBeFalsy();
  });

  test('the iOS icon is a 1024x1024 PNG with no transparency', () => {
    const png = readFileSync(join(APP_DIR, appJson.ios.icon ?? appJson.icon));
    const view = new DataView(png.buffer, png.byteOffset, png.byteLength);
    expect(Array.from(png.slice(1, 4))).toEqual([0x50, 0x4e, 0x47]); // "PNG"
    expect(view.getUint32(16)).toBe(1024);
    expect(view.getUint32(20)).toBe(1024);
    // Colour type 2 (RGB) or 0 (grey) has no alpha channel...
    expect([0, 2]).toContain(png[25]);
    // ...and no tRNS chunk may add transparency.
    const chunks: string[] = [];
    for (let offset = 8; offset < png.length; offset += 12 + view.getUint32(offset)) {
      chunks.push(String.fromCharCode(...Array.from(png.slice(offset + 4, offset + 8))));
    }
    expect(chunks).toContain('IEND');
    expect(chunks).not.toContain('tRNS');
  });

  test('the Android adaptive icon layers are wired up and the same size', () => {
    const icon = appJson.android.adaptiveIcon;
    const size = (path: string) => {
      const png = readFileSync(join(APP_DIR, path));
      return new DataView(png.buffer, png.byteOffset, png.byteLength).getUint32(16);
    };
    expect(size(icon.foregroundImage)).toBe(size(icon.backgroundImage));
    expect(() => readFileSync(join(APP_DIR, icon.monochromeImage))).not.toThrow();
  });

  test('the splash shows the placeholder image in light and dark mode', () => {
    const [, splash] = appJson.plugins.find(
      (plugin: unknown) => Array.isArray(plugin) && plugin[0] === 'expo-splash-screen',
    );
    expect(splash.image).toBe('./assets/splash-icon.png');
    expect(splash.dark.image).toBe('./assets/splash-icon.png');
    expect(() => readFileSync(join(APP_DIR, splash.image))).not.toThrow();
  });

  test('google-services.json is not referenced by a committed path', () => {
    expect(appJson.android.googleServicesFile).toBeUndefined();
  });
});

describe('app.config.js', () => {
  test('passes app.json through unchanged without EAS variables or a local google-services.json', () => {
    expect(appConfig.withBuildEnv(appJson, {}, noFile)).toBe(appJson);
  });

  test('uses the GOOGLE_SERVICES_JSON file variable for android.googleServicesFile', () => {
    const config = appConfig.withBuildEnv(appJson, { GOOGLE_SERVICES_JSON: '/eas/tmp/google-services.json' }, noFile);
    expect(config.android.googleServicesFile).toBe('/eas/tmp/google-services.json');
    expect(config.android.package).toBe(APP_ID);
    expect(config.ios).toBe(appJson.ios);
  });

  test('falls back to an uncommitted ./google-services.json for local native builds', () => {
    const config = appConfig.withBuildEnv(appJson, {}, (path) => path === './google-services.json');
    expect(config.android.googleServicesFile).toBe('./google-services.json');
  });

  test('the default export reads app.json from Expo', () => {
    expect(appConfig({ config: appJson }).android.package).toBe(APP_ID);
  });

  test.each(['preview', 'production'])('an EAS %s build with the full environment passes', (profile) => {
    const env = { ...RELEASE_ENV, EAS_BUILD: 'true', EAS_BUILD_PROFILE: profile, EAS_BUILD_PLATFORM: 'ios' };
    expect(appConfig.releaseEnvErrors(env)).toEqual([]);
    expect(appConfig.releaseEnvErrors({ ...env, EAS_BUILD_PLATFORM: 'android', GOOGLE_SERVICES_JSON: '/x.json' })).toEqual([]);
  });

  test.each(['preview', 'production'])('an EAS %s build fails for each missing variable', (profile) => {
    for (const name of appConfig.RELEASE_VARS) {
      const env = { ...RELEASE_ENV, [name]: ' ', EAS_BUILD: 'true', EAS_BUILD_PROFILE: profile };
      expect(appConfig.releaseEnvErrors(env)).toEqual([`${name} is not set in the EAS "${profile}" environment.`]);
      expect(() => appConfig.withBuildEnv(appJson, env, noFile)).toThrow(`${name} is not set`);
    }
  });

  test('a release build fails when the Auth emulator is set', () => {
    const env = {
      ...RELEASE_ENV,
      EXPO_PUBLIC_FIREBASE_AUTH_EMULATOR_HOST: 'localhost:9099',
      EAS_BUILD: 'true',
      EAS_BUILD_PROFILE: 'production',
    };
    expect(appConfig.releaseEnvErrors(env)).toEqual([
      'EXPO_PUBLIC_FIREBASE_AUTH_EMULATOR_HOST must not be set in the EAS "production" environment.',
    ]);
  });

  test('a release Android build fails without the google-services.json file variable', () => {
    const env = { ...RELEASE_ENV, EAS_BUILD: 'true', EAS_BUILD_PROFILE: 'preview', EAS_BUILD_PLATFORM: 'android' };
    expect(appConfig.releaseEnvErrors(env)).toEqual([
      'The GOOGLE_SERVICES_JSON file variable is not set in the EAS "preview" environment; push notifications need it.',
    ]);
  });

  test('development builds, and EAS CLI on a developer machine, are not checked', () => {
    expect(appConfig.releaseEnvErrors({ EAS_BUILD: 'true', EAS_BUILD_PROFILE: 'development' })).toEqual([]);
    expect(appConfig.releaseEnvErrors({ EAS_BUILD_PROFILE: 'production' })).toEqual([]);
    expect(appConfig.releaseEnvErrors({})).toEqual([]);
  });
});

describe('app/.gitignore', () => {
  const ignored = readFileSync(join(APP_DIR, '.gitignore'), 'utf8').split(/\r?\n/);

  test.each(['google-services.json', 'GoogleService-Info.plist', '*.p8', '*.p12', '*.jks', '*.keystore', '*.mobileprovision', '*service-account*.json'])(
    'ignores %s',
    (pattern) => {
      expect(ignored).toContain(pattern);
    },
  );
});
