// usageRows: a collection's use of its quota as the settings dialog shows it.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { usageRows, formatAmount, NEAR_LIMIT } from './quota-usage.js';

test('no quota or no usage gives no rows', () => {
  assert.deepEqual(usageRows(undefined, { documents: 1 }), []);
  assert.deepEqual(usageRows({ maxDocuments: 5 }, undefined), []);
  assert.deepEqual(usageRows({}, { documents: 1 }), []);
});

test('one row per limit set, in the dialog order, with text and level', () => {
  const rows = usageRows(
    { maxDocuments: 5000, maxBytes: 4 * 1024 * 1024, embeddingTokensPerDay: 1000 },
    { documents: 1234, bytes: 3.5 * 1024 * 1024, vectors: 9, embeddingCalls: 2, embeddingTokens: 1500 },
  );
  assert.deepEqual(rows.map((r) => [r.key, r.text, r.percent, r.level]), [
    ['maxDocuments', '1,234 / 5,000 documents', 25, 'ok'],
    ['maxBytes', '3.5 MB / 4.0 MB stored', 88, 'near'],
    ['embeddingTokensPerDay', '1,500 / 1,000 embedding tokens today', 150, 'over'],
  ]);
  assert.equal(rows[0].label, 'Documents');
  assert.equal(rows[0].used, 1234);
  assert.equal(rows[0].limit, 5000);
});

test('a usage field the server did not send counts as zero', () => {
  const [row] = usageRows({ maxVectors: 10 }, {});
  assert.equal(row.text, '0 / 10 vectors');
  assert.equal(row.level, 'ok');
});

test('exactly at the thresholds', () => {
  assert.equal(usageRows({ maxDocuments: 100 }, { documents: NEAR_LIMIT })[0].level, 'near');
  assert.equal(usageRows({ maxDocuments: 100 }, { documents: 100 })[0].level, 'over');
});

test('formatAmount', () => {
  assert.equal(formatAmount(0, true), '0 B');
  assert.equal(formatAmount(2048, true), '2.0 KB');
  assert.equal(formatAmount(1234567), '1,234,567');
});
