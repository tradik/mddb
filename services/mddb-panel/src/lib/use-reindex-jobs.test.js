// @vitest-environment jsdom
// use-reindex-jobs: describeJob turns a job into one line, isFinished tells a
// stopped job, and the hook drops a stale poll answer for a job that finished
// meanwhile; the rest of the hook is exercised by VectorPanel.test.jsx.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';

vi.mock('./mddb-client', () => ({
  default: { listVectorReindexJobs: vi.fn(), startVectorReindexJob: vi.fn(), getVectorReindexJob: vi.fn() },
}));

const { default: mddbClient } = await import('./mddb-client');
const { describeJob, isFinished, useReindexJobs, POLL_MS } = await import('./use-reindex-jobs');

afterEach(() => vi.useRealTimers());

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

describe('useReindexJobs', () => {
  test('a late progress answer for a job that already finished does not revive it', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.mocked(mddbClient.listVectorReindexJobs).mockResolvedValue({ jobs: [] });
    vi.mocked(mddbClient.startVectorReindexJob).mockResolvedValue({ jobId: 'j1', status: 'running' });
    const polls = [];
    vi.mocked(mddbClient.getVectorReindexJob).mockImplementation(() => new Promise((resolve) => polls.push(resolve)));
    const onFinished = vi.fn();
    const { result } = renderHook(() => useReindexJobs(onFinished));

    await act(() => result.current.start('notes', false));
    expect(result.current.active.notes.status).toBe('running');
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    expect(polls).toHaveLength(2);

    // The second poll answers first: the job is done and leaves `active`.
    await act(async () => polls[1]({ jobId: 'j1', status: 'completed', done: 1 }));
    expect(result.current.active).toEqual({});
    expect(onFinished).toHaveBeenCalledTimes(1);

    // The first poll's stale progress must not bring the job back.
    await act(async () => polls[0]({ jobId: 'j1', status: 'running', percent: 50 }));
    expect(result.current.active).toEqual({});
    expect(result.current.messages.notes).toEqual({ success: true, message: 'Reindexed: 1 embedded of 1' });
  });
});
