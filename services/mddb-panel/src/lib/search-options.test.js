// search-options: the vector index list shared by the search views and the
// 503 reading (GO-047) that tells a loading index from a training one.
import { describe, test, expect } from 'vitest';
import { VECTOR_ALGORITHMS, indexUnavailable } from './search-options';

describe('VECTOR_ALGORITHMS', () => {
  test('lists every server index with flat first and auto second', () => {
    expect(VECTOR_ALGORITHMS.map((a) => a.value)).toEqual([
      'flat', 'auto', 'hnsw', 'ivf', 'pq', 'opq', 'sq', 'sq4', 'bq',
    ]);
    expect(VECTOR_ALGORITHMS[1].label).toBe('Auto (server chooses)');
    for (const a of VECTOR_ALGORITHMS) expect(a.label).toBeTruthy();
  });
});

describe('indexUnavailable', () => {
  const err = (status, body) => Object.assign(new Error(`API Error (${status})`), { status, body });

  test.each([
    ['no error', undefined],
    ['a plain Error', new Error('network down')],
    ['a 500', err(500, { error: 'boom' })],
    ['a 404', err(404, { error: 'not found', training: true })],
  ])('returns null for %s', (_label, error) => {
    expect(indexUnavailable(error)).toBeNull();
  });

  test.each([
    ['a training index', { error: 'ivf is training; flat answers now', training: true },
      { message: 'ivf is training; flat answers now', training: true }],
    ['a loading index with a message', { error: 'index loading' },
      { message: 'index loading', training: false }],
    ['a truthy but non-boolean training flag', { error: 'x', training: 'yes' },
      { message: 'x', training: false }],
    ['an empty body', {},
      { message: 'The vector index is still loading. Please retry in a moment.', training: false }],
    ['no body', undefined,
      { message: 'The vector index is still loading. Please retry in a moment.', training: false }],
  ])('reads a 503 from %s', (_label, body, expected) => {
    expect(indexUnavailable(err(503, body))).toEqual(expected);
  });
});
