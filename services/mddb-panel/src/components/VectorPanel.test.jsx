// @vitest-environment jsdom
// VectorPanel: the Vector Search page — loading, error with retry, the
// disabled-provider screen, provider and totals, per-collection coverage and
// its colours, the GO-047 training notice and badges, and Reindex with its
// success/failure note that clears after five seconds.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getVectorStats: vi.fn(), reindexVectors: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: VectorPanel } = await import('./VectorPanel');

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
  vi.mocked(mddbClient.reindexVectors).mockReset();
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

  test('Reindex reports the count, reloads, and the note clears after 5 s', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await renderWith();
    let finish;
    vi.mocked(mddbClient.reindexVectors).mockReturnValue(new Promise((resolve) => (finish = resolve)));
    fireEvent.click(within(row('notes')).getByRole('button', { name: 'Reindex' }));

    const busy = within(row('notes')).getByRole('button', { name: 'Reindexing...' });
    expect(busy.disabled).toBe(true);
    expect(within(row('blog')).getByRole('button', { name: 'Reindex' }).disabled).toBe(false);
    expect(mddbClient.reindexVectors).toHaveBeenCalledWith('notes');

    vi.mocked(mddbClient.getVectorStats).mockResolvedValue({
      ...stats,
      collections: { ...stats.collections, notes: { total_documents: 4, embedded_documents: 4 } },
    });
    await act(async () => finish({ indexed: 3 }));
    const note = await within(row('notes')).findByText('Reindexed 3 documents');
    expect(note.className).toContain('text-green-600');
    expect(valueOf('Coverage', row('notes'))).toBe('100.0%');
    expect(within(row('notes')).getByRole('button', { name: 'Reindex' }).disabled).toBe(false);
    expect(mddbClient.getVectorStats).toHaveBeenCalledTimes(2);

    await act(async () => vi.advanceTimersByTime(5000));
    expect(within(row('notes')).queryByText('Reindexed 3 documents')).toBeNull();
  });

  test('a failed Reindex shows the error in red and clears it after 5 s', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await renderWith();
    vi.mocked(mddbClient.reindexVectors).mockRejectedValue(new Error('provider unreachable'));
    fireEvent.click(within(row('blog')).getByRole('button', { name: 'Reindex' }));

    const note = await within(row('blog')).findByText('provider unreachable');
    expect(note.className).toContain('text-red-600');
    expect(mddbClient.getVectorStats).toHaveBeenCalledTimes(1);

    // shouldAdvanceTime lets a few real milliseconds through, so stop short of 5 s.
    await act(async () => vi.advanceTimersByTime(4500));
    expect(within(row('blog')).getByText('provider unreachable')).toBeTruthy();
    await act(async () => vi.advanceTimersByTime(500));
    expect(within(row('blog')).queryByText('provider unreachable')).toBeNull();
  });
});
