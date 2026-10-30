// limit-notice: the shared record of the last capped search (#368).
import { test, expect, vi } from 'vitest';
import {
  reportLimit, getLimitNotice, subscribeLimitNotice, limitFromBody, limitFromHeaders,
} from './limit-notice.js';

test('subscribers hear every report until they unsubscribe', () => {
  const listener = vi.fn();
  const unsubscribe = subscribeLimitNotice(listener);
  reportLimit({ endpoint: '/fts', limit: 5 });
  expect(getLimitNotice()).toEqual({ endpoint: '/fts', limit: 5 });
  expect(listener).toHaveBeenCalledTimes(1);
  unsubscribe();
  reportLimit(null);
  expect(listener).toHaveBeenCalledTimes(1);
  expect(getLimitNotice()).toBeNull();
});

test.each([
  [{ limitCapped: true, limit: 1000 }, { limit: 1000 }],
  [{ limitCapped: true }, { limit: null }],
  [{ limitCapped: false, limit: 3 }, null],
  [[], null],
  [null, null],
  ['text', null],
])('limitFromBody(%o)', (body, out) => {
  expect(limitFromBody(body)).toEqual(out);
});

test.each([
  [{ 'X-Limit-Capped': 'true', 'X-Limit': '50' }, { limit: 50 }],
  [{ 'X-Limit-Capped': 'true' }, { limit: null }],
  [{}, null],
])('limitFromHeaders(%o)', (headers, out) => {
  expect(limitFromHeaders(new Headers(headers))).toEqual(out);
});

test('limitFromHeaders without headers', () => {
  expect(limitFromHeaders(undefined)).toBeNull();
});
