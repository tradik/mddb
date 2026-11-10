// @vitest-environment jsdom
// ReindexJobsTable: one row per job with its status badge, start time and
// outcome, the empty state, and an error in place of the table.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import ReindexJobsTable from './ReindexJobsTable';

afterEach(cleanup);

describe('ReindexJobsTable', () => {
  test('rows for running, failed and unknown-status jobs', () => {
    const onRefresh = vi.fn();
    render(<ReindexJobsTable onRefresh={onRefresh} jobs={[
      { jobId: 'a', collection: 'docs', status: 'running', percent: 5, done: 1, startedAt: '2026-11-10T10:00:00Z' },
      { jobId: 'b', collection: 'blog', status: 'failed' },
      { jobId: 'c', collection: 'odd', status: 'queued' },
    ]} />);
    const rows = screen.getAllByRole('row').slice(1);
    expect(rows).toHaveLength(3);
    expect(rows[0].textContent).toContain('Reindexing… 5% (1 done)');
    expect(rows[1].textContent).toContain('Reindex failed: unknown error');
    expect(rows[1].textContent).toContain('—');
    expect(screen.getByText('failed').className).toContain('bg-red-100');
    expect(screen.getByText('queued').className).toContain('bg-gray-100');
    fireEvent.click(screen.getByRole('button', { name: 'Refresh reindex jobs' }));
    expect(onRefresh).toHaveBeenCalled();
  });

  test('empty and error states', () => {
    const { rerender } = render(<ReindexJobsTable jobs={[]} onRefresh={() => {}} />);
    expect(screen.getByText('No reindex jobs since the server started.')).toBeTruthy();
    rerender(<ReindexJobsTable jobs={[]} error="boom" onRefresh={() => {}} />);
    expect(screen.getByRole('alert').textContent).toBe('boom');
    expect(screen.queryByText(/No reindex jobs/)).toBeNull();
  });
});
