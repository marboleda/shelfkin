// Catalog and entry fixtures and handlers for tests of Add item.

import { http, HttpResponse } from 'msw';

import type { components } from '@/api/schema';

import { apiUrl } from './api';
import { testId } from './pantry';

type CatalogItem = components['schemas']['CatalogItem'];
type CreateCatalogItem = components['schemas']['CreateCatalogItem'];
type CreateEntry = components['schemas']['CreateEntry'];
type PantryEntry = components['schemas']['PantryEntry'];

export function testCatalogItem(name: string, overrides: Partial<CatalogItem> = {}): CatalogItem {
  return {
    id: testId(),
    name,
    isCustom: false,
    categoryId: 1,
    locationId: 1,
    shelfLifeDays: null,
    categoryOverridden: false,
    locationOverridden: false,
    ...overrides,
  };
}

/** An API error response in the spec's format. */
export function apiError(status: number, code: string, message = code) {
  return HttpResponse.json({ error: { code, message } }, { status });
}

type SearchAnswer = CatalogItem[] | Response;

/**
 * `GET /households/:id/catalog/search`. `answer` gives the results for a query
 * (a list or a whole response), or a promise of them to answer later (tests run
 * with fake timers, so hold a promise rather than a delay). The returned
 * `queries` lists every `q` received, in order.
 */
export function catalogSearchHandler(answer: (query: string) => SearchAnswer | Promise<SearchAnswer> = () => []) {
  const queries: string[] = [];
  const handler = http.get(apiUrl('/households/:householdId/catalog/search'), async ({ request }) => {
    const query = new URL(request.url).searchParams.get('q') ?? '';
    queries.push(query);
    const result = await answer(query);
    return result instanceof Response ? result : HttpResponse.json({ items: result });
  });
  return { handler, queries };
}

/** Results whose names contain the query, ignoring case and extra spaces, like a simple catalog search. */
export function searchIn(items: CatalogItem[]) {
  const clean = (text: string) => text.trim().replace(/\s+/g, ' ').toLowerCase();
  return (query: string) => items.filter((item) => clean(item.name).includes(clean(query)));
}

type Recorded<Body> = { householdId: string; body: Body };

/**
 * `POST /households/:id/catalog-items`. By default it creates a custom item
 * from the request; `answer` can return another response instead.
 */
export function createCatalogItemHandler(answer?: (body: CreateCatalogItem, call: number) => Response | undefined) {
  const requests: Recorded<CreateCatalogItem>[] = [];
  const created: CatalogItem[] = [];
  const handler = http.post(apiUrl('/households/:householdId/catalog-items'), async ({ request, params }) => {
    const body = (await request.json()) as CreateCatalogItem;
    requests.push({ householdId: String(params.householdId), body });
    const custom = answer?.(body, requests.length);
    if (custom) return custom;
    const item = testCatalogItem(body.name.trim().replace(/\s+/g, ' '), {
      isCustom: true,
      categoryId: body.categoryId,
      locationId: body.locationId,
      shelfLifeDays: body.shelfLifeDays ?? null,
    });
    created.push(item);
    return HttpResponse.json(item, { status: 201 });
  });
  return { handler, requests, created };
}

/**
 * `POST /households/:id/entries`. By default it creates the entry from the
 * request (201); `answer` can return another response instead, e.g. an error
 * for the first call.
 */
export function createEntryHandler(
  answer?: (body: CreateEntry, call: number) => Response | undefined | Promise<Response | undefined>,
) {
  const requests: Recorded<CreateEntry>[] = [];
  const handler = http.post(apiUrl('/households/:householdId/entries'), async ({ request, params }) => {
    const body = (await request.json()) as CreateEntry;
    requests.push({ householdId: String(params.householdId), body });
    const custom = await answer?.(body, requests.length);
    if (custom) return custom;
    return HttpResponse.json(entryFrom(body), { status: 201 });
  });
  return { handler, requests };
}

/** The entry the API would return for a create request. */
export function entryFrom(body: CreateEntry): PantryEntry {
  return {
    id: body.id,
    catalogItemId: body.catalogItemId,
    locationId: body.locationId ?? 1,
    level: body.level ?? 'full',
    purchasedOn: body.purchasedOn ?? '2026-11-02',
    expiresOn: body.expiresOn ?? null,
    createdAt: '2026-11-02T15:00:00Z',
    updatedAt: '2026-11-02T15:00:00Z',
  };
}
