// The universal link and app link files (#33), served from
// https://<host>/.well-known/ by Firebase Hosting (#39). Only /join/* may be
// claimed, so every other path on the host stays in the browser.

// The app's tsconfig has no Node types, so these are typed here.
const { readFileSync } = jest.requireActual<{ readFileSync(path: string, encoding: 'utf8'): string }>('fs');
const { dirname, join } = jest.requireActual<{ dirname(path: string): string; join(...parts: string[]): string }>('path');

const appConfig = require('../app.json').expo;

const WELL_KNOWN = join(dirname(expect.getState().testPath ?? ''), '..', 'public', '.well-known');
const APP_ID = 'com.marboleda.shelfkin';
// Until #41 provides the real values, the files hold these marked placeholders.
const TEAM_ID = /^([A-Z0-9]{10}|APPLE_TEAM_ID_PLACEHOLDER_SEE_ISSUE_41)$/;
const FINGERPRINT = /^([0-9A-F]{2}:){31}[0-9A-F]{2}$|^[A-Z0-9_]+_PLACEHOLDER_SEE_ISSUE_41$/;

function readJson(name: string): unknown {
  return JSON.parse(readFileSync(join(WELL_KNOWN, name), 'utf8'));
}

/** The host the app claims, from `ios.associatedDomains`. */
function host(): string {
  const domains: string[] = appConfig.ios.associatedDomains;
  expect(domains).toHaveLength(1);
  expect(domains[0]).toMatch(/^applinks:[a-z0-9.-]+$/);
  return domains[0].slice('applinks:'.length);
}

describe('apple-app-site-association', () => {
  const file = readJson('apple-app-site-association') as {
    applinks: { details: { appIDs: string[]; appID: string; components: Record<string, unknown>[]; paths: string[] }[] };
  };

  test('lists <Team ID>.<bundle identifier> for /join/* only', () => {
    expect(Object.keys(file)).toEqual(['applinks']);
    expect(file.applinks.details).toHaveLength(1);
    const [detail] = file.applinks.details;

    for (const appID of [...detail.appIDs, detail.appID]) {
      const [teamId, ...bundle] = appID.split('.');
      expect(teamId).toMatch(TEAM_ID);
      expect(bundle.join('.')).toBe(APP_ID);
    }
    // The iOS 13+ form and the older form claim the same, single path pattern.
    expect(detail.components).toEqual([{ '/': '/join/*' }]);
    expect(detail.paths).toEqual(['/join/*']);
  });
});

describe('assetlinks.json', () => {
  const file = readJson('assetlinks.json') as {
    relation: string[];
    target: { namespace: string; package_name: string; sha256_cert_fingerprints: string[] };
  }[];

  test('lists the Android package with the upload key and the Play app signing key', () => {
    expect(file).toHaveLength(1);
    const [statement] = file;
    expect(statement.relation).toEqual(['delegate_permission/common.handle_all_urls']);
    expect(statement.target.namespace).toBe('android_app');
    expect(statement.target.package_name).toBe(APP_ID);

    const fingerprints = statement.target.sha256_cert_fingerprints;
    expect(fingerprints).toHaveLength(2);
    expect(new Set(fingerprints).size).toBe(2);
    for (const fingerprint of fingerprints) expect(fingerprint).toMatch(FINGERPRINT);
  });
});

describe('app.json', () => {
  test('the custom scheme is shelfkin', () => {
    expect(appConfig.scheme).toBe('shelfkin');
  });

  test('Android verifies https links on the same host for /join/ only', () => {
    const filters = appConfig.android.intentFilters as {
      action: string;
      autoVerify: boolean;
      data: { scheme: string; host: string; pathPrefix?: string }[];
      category: string[];
    }[];
    expect(filters).toHaveLength(1);
    const [filter] = filters;
    expect(filter.action).toBe('VIEW');
    expect(filter.autoVerify).toBe(true);
    expect(filter.category).toEqual(expect.arrayContaining(['BROWSABLE', 'DEFAULT']));
    expect(filter.data).toEqual([{ scheme: 'https', host: host(), pathPrefix: '/join/' }]);
  });
});
