import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  embeddedCoverage,
  isSampled,
  formatBytes,
  vocabularyVerdict,
} from './advisor-format.js';

// SRCH-010. The advisor's numbers are raw; these are the edge cases that turn
// them into something a person can read without dividing by zero on the way.

test('coverage is a percentage of the collection', () => {
  assert.equal(embeddedCoverage({ documents: 200, embeddedDocuments: 100 }), 50);
  assert.equal(embeddedCoverage({ documents: 3, embeddedDocuments: 3 }), 100);
});

test('coverage of an empty collection is absent, not zero', () => {
  // 0% would claim nothing is embedded; there is nothing to embed.
  assert.equal(embeddedCoverage({ documents: 0, embeddedDocuments: 0 }), null);
  assert.equal(embeddedCoverage(null), null);
  assert.equal(embeddedCoverage(undefined), null);
});

test('a missing embedded count reads as none embedded', () => {
  assert.equal(embeddedCoverage({ documents: 100 }), 0);
});

test('sampling is reported only when it actually happened', () => {
  assert.equal(isSampled({ documents: 5000, sampled: 2000 }), true);
  assert.equal(isSampled({ documents: 100, sampled: 100 }), false);
  assert.equal(isSampled({ documents: 0, sampled: 0 }), false);
  assert.equal(isSampled(null), false);
});

test('bytes are formatted at a readable scale', () => {
  assert.equal(formatBytes(512), '512 B');
  assert.equal(formatBytes(2048), '2.0 KB');
  assert.equal(formatBytes(5 * 1024 * 1024), '5.0 MB');
  assert.equal(formatBytes(1536 * 1024 * 1024), '1.5 GB');
});

test('a missing byte count is a dash, not zero bytes', () => {
  assert.equal(formatBytes(0), '—');
  assert.equal(formatBytes(undefined), '—');
  assert.equal(formatBytes(-1), '—');
});

test('the vocabulary number is explained, not just shown', () => {
  // 0.18 is not obviously bad and 8 is not obviously fine to anyone who has
  // not read the docs.
  assert.match(vocabularyVerdict(0.18), /look alike/);
  assert.match(vocabularyVerdict(3), /Moderately/);
  assert.match(vocabularyVerdict(12), /discriminates well/);
  assert.equal(vocabularyVerdict(undefined), null);
  assert.equal(vocabularyVerdict(null), null);
});

test('the boundary values fall on the documented side', () => {
  // Below 2 is where the advisor switches to query expansion, so the verdict
  // has to agree with the recommendation the user is reading beside it.
  assert.match(vocabularyVerdict(1.99), /look alike/);
  assert.match(vocabularyVerdict(2), /Moderately/);
});
