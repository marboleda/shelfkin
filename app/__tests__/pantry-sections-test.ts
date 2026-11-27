import { summarizeGroup } from '@/pantry/format';
import type { Category, Location, PantryGroup } from '@/pantry/pantry';
import {
  activeFilterCount,
  buildSections,
  expiringSummary,
  filterGroups,
  filterNames,
  hasNoEntries,
  isExpiring,
  knownFilters,
  NO_FILTERS,
  rowKey,
  sectionsByCategory,
  sectionsByLocation,
  toggleId,
  type PantryFilters,
  type PantrySection,
} from '@/pantry/sections';
import { TEST_TODAY, testEntry, testGroup } from '../test-utils/pantry';

const PANTRY = 1;
const FRIDGE = 2;
const FREEZER = 3;
const SPICE_RACK = 4;

const LOCATIONS: Location[] = [
  { id: PANTRY, key: 'pantry', name: 'Pantry' },
  { id: FRIDGE, key: 'fridge', name: 'Fridge' },
  { id: FREEZER, key: 'freezer', name: 'Freezer' },
  { id: SPICE_RACK, key: 'spice_rack', name: 'Spice rack' },
];

const PRODUCE = 1;
const DAIRY = 2;
const GRAINS = 4;

const CATEGORIES: Category[] = [
  { id: PRODUCE, key: 'produce', name: 'Produce' },
  { id: DAIRY, key: 'dairy', name: 'Dairy' },
  { id: GRAINS, key: 'grains', name: 'Grains' },
];

/** Each section as `[title, [row name ×entries, …]]`, for compact assertions. */
function outline(sections: PantrySection[]): [string, string[]][] {
  return sections.map((section) => [
    section.title,
    section.data.map((group) => (group.entries.length > 1 ? `${group.name} ×${group.entries.length}` : group.name)),
  ]);
}

function ids(entries: { id: string }[]): string[] {
  return entries.map((entry) => entry.id);
}

function filters(locationIds: number[] = [], categoryIds: number[] = [], expiring = false): PantryFilters {
  return { locationIds, categoryIds, expiring };
}

// Groups in the API's order (by name).
const BUTTER = testGroup('Butter', [testEntry({ locationId: FRIDGE })], { categoryId: DAIRY });
const MILK_FRIDGE_A = testEntry({ locationId: FRIDGE });
const MILK_FREEZER = testEntry({ locationId: FREEZER });
const MILK_FRIDGE_B = testEntry({ locationId: FRIDGE });
const MILK = testGroup('Milk', [MILK_FRIDGE_A, MILK_FREEZER, MILK_FRIDGE_B], { categoryId: DAIRY });
const RICE = testGroup('Rice', [testEntry({ locationId: PANTRY })], { categoryId: GRAINS });
const SPINACH = testGroup('Spinach', [testEntry({ locationId: FRIDGE })], { categoryId: PRODUCE });
const GROUPS = [BUTTER, MILK, RICE, SPINACH];

describe('sectionsByLocation', () => {
  test('one section per location with entries, in the locations\' order; empty locations are left out', () => {
    expect(outline(sectionsByLocation(GROUPS, LOCATIONS))).toEqual([
      ['Pantry', ['Rice']],
      ['Fridge', ['Butter', 'Milk ×2', 'Spinach']],
      ['Freezer', ['Milk']],
    ]);
  });

  test('an item split across two locations is a single-entry row in each', () => {
    const fridge = testEntry({ locationId: FRIDGE });
    const freezer = testEntry({ locationId: FREEZER });
    const milk = testGroup('Milk', [fridge, freezer]);
    const sections = sectionsByLocation([milk], LOCATIONS);

    expect(outline(sections)).toEqual([
      ['Fridge', ['Milk']],
      ['Freezer', ['Milk']],
    ]);
    expect(ids(sections[0].data[0].entries)).toEqual(ids([fridge]));
    expect(ids(sections[1].data[0].entries)).toEqual(ids([freezer]));
    expect(sections.map((section) => section.key)).toEqual(['location-2', 'location-3']);
  });

  test('two in one location and one in another is "×2" and a single row, entries in the API\'s order', () => {
    const sections = sectionsByLocation([MILK], LOCATIONS);
    expect(outline(sections)).toEqual([
      ['Fridge', ['Milk ×2']],
      ['Freezer', ['Milk']],
    ]);
    expect(ids(sections[0].data[0].entries)).toEqual(ids([MILK_FRIDGE_A, MILK_FRIDGE_B]));
  });

  test('rows keep the API\'s order within each section', () => {
    const groups = ['Apples', 'bananas', 'Cherries'].map((name) => testGroup(name, [testEntry({ locationId: FRIDGE })]));
    expect(outline(sectionsByLocation(groups, LOCATIONS))).toEqual([['Fridge', ['Apples', 'bananas', 'Cherries']]]);
  });

  test('entries whose location is not in the list go in a final "Unknown" section', () => {
    const lost = testEntry({ locationId: 99 });
    const also = testEntry({ locationId: 98 });
    const group = testGroup('Mystery', [testEntry({ locationId: PANTRY }), lost, also]);
    const sections = sectionsByLocation([group, BUTTER], LOCATIONS);

    expect(outline(sections)).toEqual([
      ['Pantry', ['Mystery']],
      ['Fridge', ['Butter']],
      ['Unknown', ['Mystery ×2']],
    ]);
    expect(sections[2].key).toBe('unknown');
    expect(ids(sections[2].data[0].entries)).toEqual(ids([lost, also]));
  });

  test('no groups give no sections', () => {
    expect(sectionsByLocation([], LOCATIONS)).toEqual([]);
  });
});

describe('sectionsByCategory', () => {
  test('one section per category with items, in the categories\' order, each item once', () => {
    expect(outline(sectionsByCategory(GROUPS, CATEGORIES))).toEqual([
      ['Produce', ['Spinach']],
      ['Dairy', ['Butter', 'Milk ×3']],
      ['Grains', ['Rice']],
    ]);
  });

  test('uses the group\'s (effective) categoryId and keeps all its entries', () => {
    // A household override (#31) arrives as the group's categoryId.
    const overridden = testGroup('Milk', [testEntry({ locationId: FRIDGE }), testEntry({ locationId: FREEZER })], {
      categoryId: GRAINS,
    });
    const sections = sectionsByCategory([overridden], CATEGORIES);
    expect(outline(sections)).toEqual([['Grains', ['Milk ×2']]]);
    expect(sections[0].key).toBe('category-4');
  });

  test('a category that is not in the list goes in a final "Unknown" section', () => {
    const odd = testGroup('Odd', [testEntry()], { categoryId: 77 });
    expect(outline(sectionsByCategory([odd, RICE], CATEGORIES))).toEqual([
      ['Grains', ['Rice']],
      ['Unknown', ['Odd']],
    ]);
  });
});

describe('filters', () => {
  test('no filters keep everything', () => {
    expect(filterGroups(GROUPS, NO_FILTERS)).toEqual(GROUPS);
  });

  test('one location keeps only that location\'s entries and drops items with none there', () => {
    const kept = filterGroups(GROUPS, filters([FRIDGE]));
    expect(kept.map((group) => group.name)).toEqual(['Butter', 'Milk', 'Spinach']);
    expect(ids(kept[1].entries)).toEqual(ids([MILK_FRIDGE_A, MILK_FRIDGE_B]));
  });

  test('a location filter splits a group: one milk in the Fridge and one in the Freezer is a single-entry Milk', () => {
    const fridge = testEntry({ locationId: FRIDGE });
    const milk = testGroup('Milk', [fridge, testEntry({ locationId: FREEZER })]);
    for (const mode of ['location', 'category'] as const) {
      const sections = buildSections([milk], { mode, filters: filters([FRIDGE]), locations: LOCATIONS, categories: CATEGORIES });
      expect(sections).toHaveLength(1);
      expect(ids(sections[0].data[0].entries)).toEqual(ids([fridge]));
    }
  });

  test('two locations match either', () => {
    expect(outline(buildSections(GROUPS, { mode: 'location', filters: filters([FREEZER, PANTRY]), locations: LOCATIONS, categories: CATEGORIES }))).toEqual([
      ['Pantry', ['Rice']],
      ['Freezer', ['Milk']],
    ]);
  });

  test('one category keeps or drops whole items', () => {
    const kept = filterGroups(GROUPS, filters([], [DAIRY]));
    expect(kept).toEqual([BUTTER, MILK]);
  });

  test('a location and a category must both match', () => {
    expect(outline(buildSections(GROUPS, { mode: 'location', filters: filters([FRIDGE], [DAIRY]), locations: LOCATIONS, categories: CATEGORIES }))).toEqual([
      ['Fridge', ['Butter', 'Milk ×2']],
    ]);
    expect(outline(buildSections(GROUPS, { mode: 'category', filters: filters([FREEZER], [DAIRY]), locations: LOCATIONS, categories: CATEGORIES }))).toEqual([
      ['Dairy', ['Milk']],
    ]);
  });

  test('filters that match nothing give no sections', () => {
    expect(buildSections(GROUPS, { mode: 'location', filters: filters([SPICE_RACK]), locations: LOCATIONS, categories: CATEGORIES })).toEqual([]);
    expect(buildSections(GROUPS, { mode: 'category', filters: filters([PANTRY], [DAIRY]), locations: LOCATIONS, categories: CATEGORIES })).toEqual([]);
  });

  test('knownFilters drops unknown IDs and puts the rest in chip order', () => {
    expect(knownFilters(filters([FREEZER, 42, FRIDGE], [9, DAIRY, PRODUCE]), LOCATIONS, CATEGORIES, 0)).toEqual(
      filters([FRIDGE, FREEZER], [PRODUCE, DAIRY]),
    );
  });

  test('knownFilters keeps Expiring only while something is expiring', () => {
    expect(knownFilters(filters([], [], true), LOCATIONS, CATEGORIES, 2).expiring).toBe(true);
    expect(knownFilters(filters([], [], true), LOCATIONS, CATEGORIES, 0).expiring).toBe(false);
    expect(knownFilters(filters([], [], false), LOCATIONS, CATEGORIES, 2).expiring).toBe(false);
  });

  test('filterNames lists locations first, in chip order', () => {
    expect(filterNames(filters([FREEZER, FRIDGE], [DAIRY]), LOCATIONS, CATEGORIES)).toEqual(['Fridge', 'Freezer', 'Dairy']);
  });

  test('filterNames puts "Expiring" first', () => {
    expect(filterNames(filters([FRIDGE], [DAIRY], true), LOCATIONS, CATEGORIES)).toEqual(['Expiring', 'Fridge', 'Dairy']);
    expect(filterNames(filters([], [], true), LOCATIONS, CATEGORIES)).toEqual(['Expiring']);
  });

  test('activeFilterCount counts the panel\'s chips, not Expiring, and toggleId', () => {
    expect(activeFilterCount(filters([FRIDGE], [DAIRY, GRAINS]))).toBe(3);
    expect(activeFilterCount(filters([FRIDGE], [], true))).toBe(1);
    expect(toggleId([1, 2], 3)).toEqual([1, 2, 3]);
    expect(toggleId([1, 2], 1)).toEqual([2]);
  });
});

describe('expiring', () => {
  const YOGURT_SOON = testEntry({ locationId: FRIDGE, expiresOn: '2026-11-04', expiryStatus: 'soon' });
  const YOGURT_LATER = testEntry({ locationId: FRIDGE, expiresOn: '2026-12-01' });
  const YOGURT_FREEZER_EXPIRED = testEntry({ locationId: FREEZER, expiresOn: '2026-10-30', expiryStatus: 'expired' });
  const YOGURT = testGroup('Yogurt', [YOGURT_SOON, YOGURT_LATER, YOGURT_FREEZER_EXPIRED], { categoryId: DAIRY });
  const BREAD_EXPIRED = testEntry({ locationId: PANTRY, expiresOn: '2026-11-01', expiryStatus: 'expired' });
  const BREAD = testGroup('Bread', [BREAD_EXPIRED], { categoryId: GRAINS });
  // Out entries are never expiring, whatever their status says.
  const KALE = testGroup('Kale', [
    testEntry({ locationId: FRIDGE, level: 'out', expiresOn: '2026-11-03', expiryStatus: 'soon' }),
  ]);
  const EXPIRING_GROUPS = [BREAD, ...GROUPS, KALE, YOGURT];

  function expiringSections(mode: 'location' | 'category', active: PantryFilters) {
    return buildSections(EXPIRING_GROUPS, { mode, filters: active, locations: LOCATIONS, categories: CATEGORIES });
  }

  test('isExpiring uses the API\'s expiryStatus only, and never for an out entry', () => {
    expect(isExpiring(YOGURT_SOON)).toBe(true);
    expect(isExpiring(BREAD_EXPIRED)).toBe(true);
    expect(isExpiring(YOGURT_LATER)).toBe(false);
    // A past date with no status is not counted: the app does no date maths.
    expect(isExpiring(testEntry({ expiresOn: '2020-01-01', expiryStatus: null }))).toBe(false);
    expect(isExpiring(KALE.entries[0])).toBe(false);
  });

  test('counting: items with an expiring entry, not entries, each name once, soonest first', () => {
    // Yogurt has two expiring entries (counted once); its soonest, expired on
    // 30 Oct, puts it before Bread (expired 1 Nov). Kale is out.
    expect(expiringSummary(EXPIRING_GROUPS)).toEqual({ count: 2, names: ['Yogurt', 'Bread'] });
    expect(expiringSummary(GROUPS)).toEqual({ count: 0, names: [] });
    expect(expiringSummary([])).toEqual({ count: 0, names: [] });
  });

  test('one item with one expiring entry, and one item with two, both count 1', () => {
    expect(expiringSummary([BREAD])).toEqual({ count: 1, names: ['Bread'] });
    const milk = testGroup('Milk', [
      testEntry({ locationId: FRIDGE, expiresOn: '2026-11-03', expiryStatus: 'soon' }),
      testEntry({ locationId: FREEZER, expiresOn: '2026-11-04', expiryStatus: 'soon' }),
    ]);
    expect(expiringSummary([milk])).toEqual({ count: 1, names: ['Milk'] });
  });

  test('names are sorted by each item\'s soonest expiring entry: expired, then today, tomorrow and so on', () => {
    const group = (name: string, ...entries: [string, 'soon' | 'expired' | null][]) =>
      testGroup(
        name,
        entries.map(([expiresOn, expiryStatus]) => testEntry({ expiresOn, expiryStatus })),
      );
    // In the API's order (by name). TEST_TODAY is 2 Nov.
    const groups = [
      group('Apples', ['2026-11-04', 'soon']),
      group('Bread', ['2026-11-03', 'soon']),
      // Its soonest expiring entry (today) counts, not its later one; its
      // entry with no status is not expiring, so its earlier date is ignored.
      group('Cheese', ['2026-11-05', 'soon'], ['2026-11-02', 'soon'], ['2026-11-01', null]),
      group('Eggs', ['2026-10-28', 'expired']),
      group('Ham', ['2026-11-03', 'soon']),
      group('Milk', ['2026-12-01', null]),
    ];
    expect(expiringSummary(groups)).toEqual({ count: 5, names: ['Eggs', 'Cheese', 'Bread', 'Ham', 'Apples'] });
  });

  test('items with the same soonest date keep the API\'s order', () => {
    const groups = ['Apples', 'Bread', 'Cheese', 'Dates'].map((name, index) =>
      testGroup(name, [testEntry({ expiresOn: index === 2 ? '2026-11-02' : '2026-11-04', expiryStatus: 'soon' })]),
    );
    expect(expiringSummary(groups).names).toEqual(['Cheese', 'Apples', 'Bread', 'Dates']);
  });

  test('Expiring alone keeps only the expiring entries and drops groups left with none', () => {
    const kept = filterGroups(EXPIRING_GROUPS, filters([], [], true));
    expect(kept.map((group) => group.name)).toEqual(['Bread', 'Yogurt']);
    expect(kept[0]).toBe(BREAD);
    expect(ids(kept[1].entries)).toEqual(ids([YOGURT_SOON, YOGURT_FREEZER_EXPIRED]));
  });

  test('Expiring combined with a location filter: both must match', () => {
    const kept = filterGroups(EXPIRING_GROUPS, filters([FRIDGE], [], true));
    expect(kept.map((group) => group.name)).toEqual(['Yogurt']);
    expect(ids(kept[0].entries)).toEqual(ids([YOGURT_SOON]));
    expect(outline(expiringSections('location', filters([FRIDGE, FREEZER], [], true)))).toEqual([
      ['Fridge', ['Yogurt']],
      ['Freezer', ['Yogurt']],
    ]);
  });

  test('Expiring combined with a category filter, and a combination that matches nothing', () => {
    expect(outline(expiringSections('category', filters([], [DAIRY], true)))).toEqual([['Dairy', ['Yogurt ×2']]]);
    expect(expiringSections('location', filters([PANTRY], [DAIRY], true))).toEqual([]);
  });
});

describe('highlight per section', () => {
  test('an item expired in one location and fresh in another is highlighted only in the first section', () => {
    const milk = testGroup('Milk', [
      testEntry({ locationId: FRIDGE, expiresOn: '2026-10-25', expiryStatus: 'expired' }),
      testEntry({ locationId: FREEZER, expiresOn: '2027-01-10' }),
      testEntry({ locationId: FREEZER, expiresOn: '2027-02-10' }),
    ]);
    const [fridge, freezer] = sectionsByLocation([milk], LOCATIONS);
    expect(summarizeGroup(fridge.data[0].entries, TEST_TODAY).highlighted).toBe(true);
    expect(summarizeGroup(freezer.data[0].entries, TEST_TODAY).highlighted).toBe(false);
  });
});

describe('helpers', () => {
  test('row keys include the section, so one item can appear in two sections', () => {
    expect(rowKey('location-2', 'abc')).not.toBe(rowKey('location-3', 'abc'));
  });

  test('hasNoEntries', () => {
    expect(hasNoEntries([])).toBe(true);
    expect(hasNoEntries([{ ...BUTTER, entries: [] } as PantryGroup])).toBe(true);
    expect(hasNoEntries([BUTTER])).toBe(false);
  });
});
