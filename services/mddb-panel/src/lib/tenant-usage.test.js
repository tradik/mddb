// tenant-usage (GO-054): the bars' limits, the edit form's values both ways,
// refusals and queue summaries.
import { describe, test, expect } from 'vitest';
import {
  TENANT_USAGE_FIELDS, QUOTA_INPUTS, LIMIT_INPUTS, barLimits, toForm, fromForm,
  refusalList, refusalTotal, queueSummary, limitText,
} from './tenant-usage';
import { usageRows } from './quota-usage';

describe('usage bars', () => {
  test('the RAM share and the collections join the collection limits', () => {
    const report = { quota: { maxCollections: 4 }, vectorRamLimitBytes: 2048, usage: { collections: 2, vectorRamBytes: 1024 } };
    const rows = usageRows(barLimits(report), report.usage, TENANT_USAGE_FIELDS);
    expect(rows.map((r) => [r.key, r.percent])).toEqual([['maxCollections', 50], ['vectorRamLimitBytes', 50]]);
    expect(rows[1].text).toBe('1.0 KB / 2.0 KB of vector RAM');
  });
  test('no RAM limit is 0', () => {
    expect(barLimits({ quota: undefined }).vectorRamLimitBytes).toBe(0);
  });
});

describe('form values', () => {
  test('toForm leaves unset fields empty', () => {
    const f = toForm(QUOTA_INPUTS, { maxDocuments: 10, vectorRamShare: 0.5 });
    expect(f.maxDocuments).toBe('10');
    expect(f.vectorRamShare).toBe('0.5');
    expect(f.maxBytes).toBe('');
    expect(toForm(LIMIT_INPUTS, null).readPerSecond).toBe('');
  });
  test('fromForm keeps what is set and drops zeros', () => {
    expect(fromForm(QUOTA_INPUTS, { maxDocuments: ' 5 ', maxBytes: '0', vectorRamShare: '0.25' }, 0))
      .toEqual({ part: { maxDocuments: 5, vectorRamShare: 0.25 } });
    expect(fromForm(LIMIT_INPUTS, { readPerSecond: '-1' }, -1)).toEqual({ part: { readPerSecond: -1 } });
  });
  test('nothing set is null: the part is removed', () => {
    expect(fromForm(QUOTA_INPUTS, {}, 0)).toEqual({ part: null });
  });
  test('bad values name the field', () => {
    expect(fromForm(QUOTA_INPUTS, { maxDocuments: '-1' }, 0).error).toBe('Documents: must be a whole number from 0.');
    expect(fromForm(QUOTA_INPUTS, { maxVectors: '1.5' }, 0).error).toMatch(/^Vectors/);
    expect(fromForm(QUOTA_INPUTS, { vectorRamShare: '2' }, 0).error).toMatch(/a number from 0 to 1/);
    expect(fromForm(LIMIT_INPUTS, { writePerSecond: '-2' }, -1).error).toMatch(/from -1/);
    expect(fromForm(LIMIT_INPUTS, { writePerSecond: 'x' }, -1).error).toMatch(/^Writes per second/);
  });
});

describe('refusals, queues, limits', () => {
  test('refusals sorted by count, then reason', () => {
    const r = { 'tenant.readRate': 2, 'quota.documents': 5, 'key.writeRate': 2 };
    expect(refusalList(r).map((x) => x.reason)).toEqual(['quota.documents', 'key.writeRate', 'tenant.readRate']);
    expect(refusalTotal(r)).toBe(9);
    expect(refusalList(undefined)).toEqual([]);
    expect(refusalTotal(undefined)).toBe(0);
  });
  test('queue summary', () => {
    expect(queueSummary({ taken: { jobs: 4, waitSecondsTotal: 2, maxWaitSeconds: 1.25 } }))
      .toBe('4 jobs taken, average wait 0.5 s, longest 1.3 s');
    expect(queueSummary({ taken: { jobs: 0 } })).toBe('no jobs taken yet');
    expect(queueSummary(undefined)).toBe('no jobs taken yet');
  });
  test('a limit of 0 is none', () => {
    expect(limitText(0)).toBe('none');
    expect(limitText(1500)).toBe('1,500');
  });
});
