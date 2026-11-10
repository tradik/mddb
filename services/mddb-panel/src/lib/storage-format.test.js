// storage-format: byte, percentage, duration and ETA formatting for the
// storage breakdown, the indexing status and vector reindex jobs.
import { describe, test, expect } from 'vitest';
import {
  formatBytes, percentOf, categoryRows, categorySummary, sortedCollections,
  sortedBuckets, formatDuration, formatEta, progressPercent, STORAGE_CATEGORIES,
} from './storage-format';

describe('formatBytes', () => {
  test.each([
    [0, '0 B'], [-5, '0 B'], [undefined, '0 B'], ['x', '0 B'],
    [512, '512 B'], [1536, '1.5 KB'], [1048576, '1.0 MB'], [150 * 1024 * 1024, '150 MB'],
    [2690252800, '2.5 GB'], [5 * 1024 ** 5, '5120 TB'],
  ])('%s -> %s', (input, out) => expect(formatBytes(input)).toBe(out));
});

describe('percentOf', () => {
  test('rounds to one decimal and guards a zero total', () => {
    expect(percentOf(1, 3)).toBe(33.3);
    expect(percentOf(undefined, 10)).toBe(0);
    expect(percentOf(5, 0)).toBe(0);
    expect(percentOf(5, undefined)).toBe(0);
  });
});

describe('categoryRows / categorySummary', () => {
  test('keeps categories with bytes, in display order', () => {
    const rows = categoryRows({ totalBytes: 200, byCategory: { vectors: 50, content: 150, metadata: 0 } });
    expect(rows.map((r) => r.key)).toEqual(['content', 'vectors']);
    expect(rows[0]).toEqual({ key: 'content', label: 'Content', bytes: 150, percent: 75 });
    expect(categorySummary(rows)).toBe('Content 150 B (75%), Vectors 50 B (25%)');
  });

  test('an entry without a breakdown has no rows', () => {
    expect(categoryRows()).toEqual([]);
    expect(categoryRows({})).toEqual([]);
    expect(categorySummary([])).toBe('No bytes recorded');
    expect(STORAGE_CATEGORIES).toHaveLength(5);
  });
});

describe('sorting', () => {
  test('collections and buckets largest first, tolerating gaps', () => {
    expect(sortedCollections({ a: { totalBytes: 1 }, b: { totalBytes: 9 }, c: null }).map(([n]) => n)).toEqual(['b', 'a', 'c']);
    expect(sortedCollections(null)).toEqual([]);
    expect(sortedCollections()).toEqual([]);
    expect(sortedBuckets({ x: { inUseBytes: 2 }, y: { inUseBytes: 5 }, z: {} }).map(([n]) => n)).toEqual(['y', 'x', 'z']);
    expect(sortedBuckets(null)).toEqual([]);
    expect(sortedCollections({ n: null, m: null, k: {} }).map(([n]) => n)).toHaveLength(3);
    expect(sortedBuckets({ n: null, m: null, k: {} }).map(([n]) => n)).toHaveLength(3);
    expect(sortedBuckets()).toEqual([]);
  });
});

describe('durations', () => {
  test.each([[0, '0 ms'], [undefined, '0 ms'], [850.4, '850 ms'], [41210, '41.2 s']])('formatDuration(%s)', (ms, out) => {
    expect(formatDuration(ms)).toBe(out);
  });
  test.each([[0, ''], [undefined, ''], [-3, ''], [45, '45 s'], [720, '12 min'], [7500, '2 h 5 min']])('formatEta(%s)', (s, out) => {
    expect(formatEta(s)).toBe(out);
  });
  test.each([[0.975, 98], [1.4, 100], [-1, 0], [undefined, 0], ['x', 0]])('progressPercent(%s)', (p, out) => {
    expect(progressPercent(p)).toBe(out);
  });
});
