/**
 * Server-side result cap (#368).
 *
 * mddbd returns at most MDDB_SEARCH_MAX_LIMIT results per search (lowered per
 * collection by `retrieval.maxTopK`) and says so: `limitCapped: true` and
 * `limit` in a JSON body, or `X-Limit-Capped` / `X-Limit` headers on
 * `/v1/search`, whose body is an array. The API client reports every search
 * answer here; one notice component shows the latest capped one, so every
 * search view gets the same message without each re-implementing it.
 */
import { useSyncExternalStore } from 'react';

let current = null;
const listeners = new Set();

/** Records the last search answer: `{ endpoint, limit }` when capped, else null. */
export function reportLimit(notice) {
  current = notice;
  listeners.forEach((listener) => listener());
}

/** The notice of the last capped search, or null. */
export function getLimitNotice() {
  return current;
}

/** Subscribes to changes; returns the unsubscribe function. */
export function subscribeLimitNotice(listener) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** React hook: the current notice, re-rendering when it changes. */
export function useLimitNotice() {
  return useSyncExternalStore(subscribeLimitNotice, getLimitNotice, getLimitNotice);
}

/**
 * Reads the cap from a parsed JSON body: `{ limit }` when the body says it was
 * capped, else null. Non-objects (arrays, null) carry no cap.
 */
export function limitFromBody(body) {
  if (body && typeof body === 'object' && body.limitCapped === true) {
    return { limit: Number(body.limit) || null };
  }
  return null;
}

/** Reads the cap from `/v1/search` response headers, else null. */
export function limitFromHeaders(headers) {
  if (headers?.get('X-Limit-Capped') === 'true') {
    return { limit: Number(headers.get('X-Limit')) || null };
  }
  return null;
}
