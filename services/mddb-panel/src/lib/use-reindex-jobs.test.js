// use-reindex-jobs: describeJob turns a job into one line and isFinished
// tells a stopped job; the hook itself is exercised by VectorPanel.test.jsx.
import { describe, test, expect } from 'vitest';
import { describeJob, isFinished } from './use-reindex-jobs';

describe('describeJob', () => {
  test.each([
    [{ status: 'failed', error: 'x' }, 'Reindex failed: x'],
    [{ status: 'failed' }, 'Reindex failed: unknown error'],
    [{ status: 'completed', result: { total: 9, embedded: 5, skipped: 3, failed: 1 } }, 'Reindexed: 5 embedded, 3 unchanged, 1 failed of 9'],
    [{ status: 'completed', done: 2 }, 'Reindexed: 2 embedded of 2'],
    [{ status: 'completed' }, 'Reindexed: 0 embedded of 0'],
    [{ status: 'running' }, 'Reindexing… 0% (0 done)'],
    [{ status: 'running', percent: 50.6, done: 3, etaSeconds: 30 }, 'Reindexing… 51% (3 done, about 30 s left)'],
  ])('%o', (job, line) => expect(describeJob(job)).toBe(line));
});

describe('isFinished', () => {
  test('completed and failed stop; anything else runs', () => {
    expect(isFinished({ status: 'completed' })).toBe(true);
    expect(isFinished({ status: 'failed' })).toBe(true);
    expect(isFinished({ status: 'running' })).toBe(false);
    expect(isFinished(undefined)).toBe(false);
  });
});
