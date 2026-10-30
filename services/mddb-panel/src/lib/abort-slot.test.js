// releaseAbort: a settling search empties the abort slot only while it holds it.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { releaseAbort } from './abort-slot.js';

test('the search that holds the slot empties it', () => {
  const mine = new AbortController();
  const ref = { current: mine };
  assert.equal(releaseAbort(ref, mine), false);
  assert.equal(ref.current, null);
});

test('a search replaced by a newer one leaves the slot to it', () => {
  const newer = new AbortController();
  const ref = { current: newer };
  assert.equal(releaseAbort(ref, new AbortController()), true);
  assert.equal(ref.current, newer);
});

test('a cancelled search finds the slot empty and is not superseded', () => {
  const ref = { current: null };
  assert.equal(releaseAbort(ref, new AbortController()), false);
  assert.equal(ref.current, null);
});
