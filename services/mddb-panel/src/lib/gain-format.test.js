import { test } from 'node:test';
import assert from 'node:assert/strict';

import { compactNumber, topShares, formatShares } from './gain-format.js';

test('large counts are shortened', () => {
  assert.equal(compactNumber(160700000), '160.7M');
  assert.equal(compactNumber(14922), '14.9k');
  assert.equal(compactNumber(2500000000), '2.5G');
  assert.equal(compactNumber(512), '512');
  assert.equal(compactNumber(undefined), '0');
});

test('shares are percentages of the whole, largest first', () => {
  assert.deepEqual(topShares({ flat: 12, hnsw: 65, pq: 8, sq: 15 }, 2), [
    { name: 'hnsw', percent: 65 },
    { name: 'sq', percent: 15 },
  ]);
  assert.deepEqual(topShares({}), []);
  assert.deepEqual(topShares(null), []);
  // Ties are broken by name so the card does not reorder between refreshes.
  assert.deepEqual(topShares({ b: 1, a: 1 }).map((s) => s.name), ['a', 'b']);
});

test('shares render as one line, unnamed as other', () => {
  assert.equal(formatShares({ rrf: 1, alpha: 3 }), 'alpha 75% · rrf 25%');
  assert.equal(formatShares({ '': 1 }), 'other 100%');
});
