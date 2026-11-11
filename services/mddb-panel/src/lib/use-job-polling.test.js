// @vitest-environment jsdom
// use-job-polling: nothing runs without jobs; each running job is fetched
// every POLL_MS and lands in onUpdate or onFinish; a failed poll finishes it;
// the timer stops when the jobs are gone.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useJobPolling, POLL_MS } from './use-job-polling';

afterEach(() => vi.useRealTimers());

function setup(initialJobs, fetchJob) {
  const options = {
    getId: (job) => job.id,
    fetchJob,
    isDone: (job) => job.status === 'completed',
    onUpdate: vi.fn(),
    onFinish: vi.fn(),
  };
  const hook = renderHook(({ jobs }) => useJobPolling(jobs, options), { initialProps: { jobs: initialJobs } });
  return { ...hook, options };
}

describe('useJobPolling', () => {
  test('polls each job and routes progress and completion', async () => {
    vi.useFakeTimers();
    const fetchJob = vi.fn()
      .mockResolvedValueOnce({ id: 'a', status: 'processing', processed: 1 })
      .mockResolvedValueOnce({ id: 'a', status: 'completed' });
    const { options, rerender } = setup({ a: { id: 'a', status: 'pending' } }, fetchJob);
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    expect(fetchJob).toHaveBeenCalledWith('a');
    expect(options.onUpdate).toHaveBeenCalledWith('a', { id: 'a', status: 'processing', processed: 1 });
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    expect(options.onFinish).toHaveBeenCalledWith('a', { id: 'a', status: 'completed' });

    rerender({ jobs: {} });
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS * 3));
    expect(fetchJob).toHaveBeenCalledTimes(2);
  });

  test('a failed poll finishes the job with the error', async () => {
    vi.useFakeTimers();
    const fetchJob = vi.fn().mockRejectedValue(new Error('job x not found'));
    const { options } = setup({ x: { id: 'x', collection: 'c' } }, fetchJob);
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    expect(options.onFinish).toHaveBeenCalledWith('x', { id: 'x', collection: 'c', status: 'failed', error: 'job x not found' });
  });

  test('without jobs no timer runs', async () => {
    vi.useFakeTimers();
    const fetchJob = vi.fn();
    setup({}, fetchJob);
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS * 2));
    expect(fetchJob).not.toHaveBeenCalled();
  });
});
