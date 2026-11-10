// @vitest-environment jsdom
// VectorPanel: the Vector Search page — loading, error with retry, the
// disabled-provider screen, provider and totals, per-collection coverage and
// its colours, the GO-047 training notice and badges, and Reindex as a
// background job (2.18.4): start with or without force, poll its progress,
// the outcome note, a 409 for a running job, and the recent-jobs table.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    getVectorStats: vi.fn(), startVectorReindexJob: vi.fn(), getVectorReindexJob: vi.fn(), listVectorReindexJobs: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: VectorPanel } = await import('./VectorPanel');
const { POLL_MS } = await import('../lib/use-reindex-jobs');

const stats = {
  enabled: true,
  provider: 'openai',
  model: 'text-embedding-3-small',
  dimensions: 1536,
  collections: {
    blog: { total_documents: 10, embedded_documents: 10 },
    docs: { total_documents: 8, embedded_documents: 5 },
    notes: { total_documents: 4, embedded_documents: 1 },
    empty: { total_documents: 0, embedded_documents: 0 },
    ghost: null,
  },
  indexTraining: { current: 'docs', pending: ['docs', 'notes'], indexes: ['ivf', 'pq'] },
};

/** The value printed under a label such as "Total Documents". */
function valueOf(label, root = document.body) {
  return within(root).getByText(label).nextElementSibling.textContent;
}

/** The row of `collection`. */
function row(collection) {
  return screen.getByRole('heading', { name: new RegExp(`^${collection}`), level: 3 }).closest('.p-6');
}

/** The coverage bar fill of a collection row. */
function bar(collection) {
  return row(collection).querySelector('.h-2.rounded-full.transition-all');
}

/** Renders the panel with `data` and waits for the page. */
async function renderWith(data = stats) {
  vi.mocked(mddbClient.getVectorStats).mockResolvedValue(data);
  render(<VectorPanel />);
  await screen.findByText('Manage vector embeddings and search');
}

beforeEach(() => {
  vi.mocked(mddbClient.getVectorStats).mockReset();
  vi.mocked(mddbClient.startVectorReindexJob).mockReset();
  vi.mocked(mddbClient.getVectorReindexJob).mockReset();
  vi.mocked(mddbClient.listVectorReindexJobs).mockReset().mockResolvedValue({ jobs: [] });
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('VectorPanel', () => {
  test('shows a spinner while the stats load', () => {
    vi.mocked(mddbClient.getVectorStats).mockReturnValue(new Promise(() => {}));
    render(<VectorPanel />);
    expect(screen.getByText('Loading vector search stats...')).toBeTruthy();
  });

  test('reports a failure and loads again on Retry', async () => {
    vi.mocked(mddbClient.getVectorStats)
      .mockRejectedValueOnce(new Error('API Error (500): down'))
      .mockResolvedValueOnce(stats);
    render(<VectorPanel />);
    expect(await screen.findByText('Error loading vector stats')).toBeTruthy();
    expect(screen.getByText('API Error (500): down')).toBeTruthy();
    expect(console.error).toHaveBeenCalledWith('Failed to load vector stats:', expect.any(Error));

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Embedding Provider')).toBeTruthy();
    expect(screen.queryByText('Error loading vector stats')).toBeNull();
    expect(mddbClient.getVectorStats).toHaveBeenCalledTimes(2);
  });

  test.each([
    ['a disabled provider', { enabled: false }],
    ['an empty answer', null],
  ])('explains how to enable vector search for %s', async (_label, data) => {
    await renderWith(data);
    expect(screen.getByText('Vector Search Disabled')).toBeTruthy();
    for (const name of [
      'MDDB_EMBEDDING_PROVIDER',
      'MDDB_EMBEDDING_MODEL',
      'MDDB_EMBEDDING_DIMENSIONS',
      'MDDB_EMBEDDING_API_KEY',
      'MDDB_EMBEDDING_API_URL',
    ]) {
      expect(screen.getByText(name)).toBeTruthy();
    }
    expect(screen.queryByRole('button', { name: /Refresh/ })).toBeNull();
  });

  test('shows the provider and the totals across collections', async () => {
    await renderWith();
    expect(valueOf('Provider')).toBe('openai');
    expect(valueOf('Model')).toBe('text-embedding-3-small');
    expect(valueOf('Dimensions')).toBe('1536');
    expect(valueOf('Total Collections')).toBe('5');
    expect(valueOf('Total Documents')).toBe('22');
    expect(valueOf('Indexed Vectors')).toBe('16');
  });

  test.each([
    ['blog', '10', '10', '100.0%', 'bg-green-600', '100%'],
    ['docs', '8', '5', '62.5%', 'bg-blue-600', '62.5%'],
    ['notes', '4', '1', '25.0%', 'bg-yellow-600', '25%'],
    ['empty', '0', '0', '0%', 'bg-yellow-600', '0%'],
    ['ghost', '0', '0', '0%', 'bg-yellow-600', '0%'],
  ])('%s: %s documents, %s indexed, %s coverage', async (name, docs, indexed, coverage, colour, width) => {
    await renderWith();
    const r = row(name);
    expect(valueOf('Documents', r)).toBe(docs);
    expect(valueOf('Indexed', r)).toBe(indexed);
    expect(valueOf('Coverage', r)).toBe(coverage);
    expect(bar(name).className).toContain(colour);
    expect(bar(name).style.width).toBe(width);
  });

  test('shows the training notice and a badge per queued collection', async () => {
    await renderWith();
    expect(screen.getByRole('status').textContent).toContain('Training ivf, pq for 2 collections (now: docs)');
    expect(row('docs').querySelector('h3').textContent).toBe('docstraining now');
    expect(row('notes').querySelector('h3').textContent).toBe('noteswaiting to train');
    expect(row('blog').querySelector('h3').textContent).toBe('blog');
  });

  test('falls back for missing provider fields, no collections and no training', async () => {
    await renderWith({ enabled: true });
    expect(valueOf('Provider')).toBe('Unknown');
    expect(valueOf('Model')).toBe('N/A');
    expect(valueOf('Dimensions')).toBe('N/A');
    expect(valueOf('Total Collections')).toBe('0');
    expect(screen.getByText('No collections found')).toBeTruthy();
    expect(screen.queryByRole('status')).toBeNull();
  });

  test('Refresh loads the stats again', async () => {
    await renderWith();
    vi.mocked(mddbClient.getVectorStats).mockResolvedValue({ ...stats, model: 'nomic-embed-text' });
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await screen.findByText('nomic-embed-text');
    expect(mddbClient.getVectorStats).toHaveBeenCalledTimes(2);
  });

  test('Reindex starts a job, polls it, reports the counts and reloads the stats', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await renderWith();
    vi.mocked(mddbClient.startVectorReindexJob).mockResolvedValue({ jobId: 'j1', collection: 'notes', status: 'running', percent: 0, done: 0 });
    fireEvent.click(screen.getByLabelText(/Force re-embed/));
    fireEvent.click(within(row('notes')).getByRole('button', { name: 'Reindex' }));
    expect(mddbClient.startVectorReindexJob).toHaveBeenCalledWith({ collection: 'notes', force: true });

    expect((await within(row('notes')).findByRole('status')).textContent).toBe('Reindexing… 0% (0 done)');
    expect(within(row('notes')).getByRole('button', { name: 'Reindexing...' }).disabled).toBe(true);
    expect(within(row('blog')).getByRole('button', { name: 'Reindex' }).disabled).toBe(false);

    vi.mocked(mddbClient.getVectorReindexJob)
      .mockResolvedValueOnce({ jobId: 'j1', status: 'running', percent: 41.3, done: 12, etaSeconds: 2280 })
      .mockResolvedValueOnce({ jobId: 'j1', status: 'completed', done: 4, result: { total: 4, embedded: 3, skipped: 1 } });
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    expect(within(row('notes')).getByRole('status').textContent).toBe('Reindexing… 41% (12 done, about 38 min left)');
    expect(mddbClient.getVectorReindexJob).toHaveBeenCalledWith('j1');

    vi.mocked(mddbClient.getVectorStats).mockResolvedValue({
      ...stats,
      collections: { ...stats.collections, notes: { total_documents: 4, embedded_documents: 4 } },
    });
    vi.mocked(mddbClient.listVectorReindexJobs).mockResolvedValue({
      jobs: [{ jobId: 'j1', collection: 'notes', force: true, status: 'completed', startedAt: '2026-11-10T10:00:00Z', result: { total: 4, embedded: 3, skipped: 1 } }],
    });
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    const note = await within(row('notes')).findByText('Reindexed: 3 embedded, 1 unchanged of 4');
    expect(note.className).toContain('text-green-700');
    expect(within(row('notes')).queryByRole('status')).toBeNull();
    expect(await screen.findByText('(force)')).toBeTruthy();
    await waitFor(() => expect(valueOf('Coverage', row('notes'))).toBe('100.0%'));
    expect(mddbClient.getVectorStats).toHaveBeenCalledTimes(2);
    // The stats reload quietly: no spinner replaces the page.
    expect(screen.queryByText('Loading vector search stats...')).toBeNull();
  });

  test('a 409 says a reindex is already running and follows that job', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await renderWith();
    vi.mocked(mddbClient.startVectorReindexJob).mockRejectedValue(
      Object.assign(new Error('API Error (409): running'), { status: 409, body: { jobId: 'j0', status: 'running', percent: 10, done: 1 } }),
    );
    fireEvent.click(within(row('blog')).getByRole('button', { name: 'Reindex' }));
    expect(mddbClient.startVectorReindexJob).toHaveBeenCalledWith({ collection: 'blog', force: false });
    expect(await within(row('blog')).findByText('A reindex of blog is already running.')).toBeTruthy();
    expect(within(row('blog')).getByRole('status').textContent).toBe('Reindexing… 10% (1 done)');

    vi.mocked(mddbClient.getVectorReindexJob).mockResolvedValue({ jobId: 'j0', status: 'failed', error: 'provider unreachable' });
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    const note = await within(row('blog')).findByText('Reindex failed: provider unreachable');
    expect(note.className).toContain('text-red-700');
  });

  test('a 409 without a job body and other refusals only show the message', async () => {
    await renderWith();
    vi.mocked(mddbClient.startVectorReindexJob)
      .mockRejectedValueOnce(Object.assign(new Error('busy'), { status: 409 }))
      .mockRejectedValueOnce(new Error('API Error (403): forbidden'));
    fireEvent.click(within(row('docs')).getByRole('button', { name: 'Reindex' }));
    expect(await within(row('docs')).findByText('A reindex of docs is already running.')).toBeTruthy();
    expect(within(row('docs')).queryByRole('status')).toBeNull();
    fireEvent.click(within(row('docs')).getByRole('button', { name: 'Reindex' }));
    expect(await within(row('docs')).findByText('API Error (403): forbidden')).toBeTruthy();
  });

  test('a job that answers finished at once, and a poll that fails', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await renderWith();
    vi.mocked(mddbClient.startVectorReindexJob)
      .mockResolvedValueOnce({ jobId: 'j2', status: 'completed', done: 2 })
      .mockResolvedValueOnce({ jobId: 'j3', status: 'running' });
    fireEvent.click(within(row('blog')).getByRole('button', { name: 'Reindex' }));
    expect(await within(row('blog')).findByText('Reindexed: 2 embedded of 2')).toBeTruthy();

    vi.mocked(mddbClient.getVectorReindexJob).mockRejectedValue(new Error('API Error (404): no such job'));
    fireEvent.click(within(row('notes')).getByRole('button', { name: 'Reindex' }));
    await within(row('notes')).findByRole('status');
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    expect(await within(row('notes')).findByText('Reindex failed: API Error (404): no such job')).toBeTruthy();
  });

  test('the recent jobs table reports a failure to list and refreshes', async () => {
    vi.mocked(mddbClient.listVectorReindexJobs)
      .mockRejectedValueOnce(new Error('API Error (403): forbidden'))
      .mockResolvedValueOnce({});
    await renderWith();
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (403): forbidden');
    fireEvent.click(screen.getByRole('button', { name: 'Refresh reindex jobs' }));
    expect(await screen.findByText('No reindex jobs since the server started.')).toBeTruthy();
  });
});
