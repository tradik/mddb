// @vitest-environment jsdom
// VectorSearchPanel: the semantic search view — the request it sends (index
// incl. "auto", explain, decomposition, relevance feedback), results and the
// plan, opening a result, the 503 handling (a loading index retried three
// times every 2s, a training one not), Cancel, Reindex and the Command modal.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    vectorSearch: vi.fn(),
    vectorReindex: vi.fn(),
    getDocument: vi.fn(),
    getMetaKeys: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: VectorSearchPanel } = await import('./VectorSearchPanel');

const initialStore = useStore.getState();

const results = [
  { rank: 1, score: 0.912, document: { key: 'alpha', lang: 'en', meta: { tag: ['a', 'b'], author: 'x', c: '1', d: '2' } } },
  { score: 0.5, document: { key: 'beta', lang: 'pl', meta: {} } },
  { score: 0.1 },
];

/** A 503 like the client throws for an index that loads or trains. */
function unavailable(body) {
  return Object.assign(new Error(`API Error (503): ${body.error}`), { status: 503, body });
}

const queryBox = () => screen.getByPlaceholderText("Describe what you're looking for...");
const searchButton = () => screen.getByRole('button', { name: 'Search' });

/** Renders the panel on collection `docs` with `query` typed in. */
function renderPanel(query = 'red apples') {
  useStore.setState({ currentCollection: 'docs', vectorQuery: query });
  return render(<VectorSearchPanel />);
}

/** The request object of the n-th vectorSearch call (0-based). */
const sent = (n = 0) => vi.mocked(mddbClient.vectorSearch).mock.calls[n][0];

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.vectorSearch).mockReset();
  vi.mocked(mddbClient.vectorReindex).mockReset();
  vi.mocked(mddbClient.getDocument).mockReset();
  vi.mocked(mddbClient.getMetaKeys).mockReset().mockResolvedValue({ meta: {} });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('VectorSearchPanel', () => {
  test('asks for a collection when none is selected', () => {
    render(<VectorSearchPanel />);
    expect(screen.getByText('Select a collection to search')).toBeTruthy();
    expect(screen.queryByText('Semantic Query')).toBeNull();
  });

  test('offers every index including auto', () => {
    renderPanel();
    const select = screen.getByDisplayValue('Flat (Exact)');
    expect([...select.options].map((o) => o.value)).toEqual([
      'flat', 'auto', 'hnsw', 'ivf', 'pq', 'opq', 'sq', 'sq4', 'bq',
    ]);
  });

  test('sends every control in the request and shows results, stats and the plan', async () => {
    vi.mocked(mddbClient.vectorSearch).mockResolvedValue({
      results,
      searchStats: { durationMs: 12, indexSize: 100 },
      plan: { algorithm: 'hnsw', candidates: 30, reasons: ['large collection'] },
    });
    useStore.setState({ searchFilterMeta: { tag: ['a'] } });
    renderPanel('  red apples  ');

    const [topK, threshold] = screen.getAllByRole('slider');
    fireEvent.change(topK, { target: { value: '25' } });
    fireEvent.change(threshold, { target: { value: '40' } });
    fireEvent.change(screen.getByDisplayValue('Flat (Exact)'), { target: { value: 'auto' } });
    fireEvent.change(screen.getByDisplayValue('Cosine Similarity'), { target: { value: 'euclidean' } });
    fireEvent.click(screen.getByLabelText('Include content'));
    fireEvent.click(screen.getByLabelText('Explain plan'));
    fireEvent.click(screen.getByLabelText('Split multi-part questions'));
    expect(screen.getByText('Top K: 25')).toBeTruthy();
    expect(screen.getByText('Threshold: 40%')).toBeTruthy();

    fireEvent.click(searchButton());
    expect(await screen.findByText('3 results found')).toBeTruthy();
    expect(sent()).toEqual({
      collection: 'docs',
      query: 'red apples',
      topK: 25,
      threshold: 0.4,
      includeContent: true,
      algorithm: 'auto',
      distanceMetric: 'euclidean',
      filterMeta: { tag: ['a'] },
      explain: true,
      queryDecomposition: 'auto',
      positive: [],
      negative: [],
      signal: expect.any(AbortSignal),
    });
    expect(screen.getByText('12ms | 100 indexed')).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Search plan' })).toBeTruthy();
    expect(screen.getByText('large collection')).toBeTruthy();
    expect(screen.getByText('91%')).toBeTruthy();
    // Only the first three meta entries, arrays joined.
    expect(screen.getByText('tag: a, b')).toBeTruthy();
    expect(screen.getByText('author: x')).toBeTruthy();
    expect(screen.getByText('c: 1')).toBeTruthy();
    expect(screen.queryByText('d: 2')).toBeNull();
    // Ranks fall back to the position; a result with no document has no thumbs.
    expect(screen.getByText('2')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: /^Mark / })).toHaveLength(4);
    expect(useStore.getState().vectorLoading).toBe(false);
  });

  test('a single result without stats, then an empty answer with no plan', async () => {
    vi.mocked(mddbClient.vectorSearch)
      .mockResolvedValueOnce({ results: [results[0]] })
      .mockResolvedValueOnce({});
    renderPanel();
    fireEvent.click(searchButton());
    expect(await screen.findByText('1 result found')).toBeTruthy();
    expect(screen.queryByText(/indexed/)).toBeNull();

    fireEvent.click(searchButton());
    expect(await screen.findByText('No results found')).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Search plan' })).toBeNull();
    expect(useStore.getState().vectorSearchStats).toBeNull();
  });

  test('Enter searches, Shift+Enter does not, and a blank query never searches', async () => {
    vi.mocked(mddbClient.vectorSearch).mockResolvedValue({ results: [] });
    renderPanel();
    fireEvent.keyDown(queryBox(), { key: 'Enter', shiftKey: true });
    fireEvent.keyDown(queryBox(), { key: 'a' });
    expect(mddbClient.vectorSearch).not.toHaveBeenCalled();
    fireEvent.keyDown(queryBox(), { key: 'Enter' });
    expect(await screen.findByText('No results found')).toBeTruthy();
    expect(mddbClient.vectorSearch).toHaveBeenCalledTimes(1);

    fireEvent.change(queryBox(), { target: { value: '   ' } });
    expect(useStore.getState().vectorQuery).toBe('   ');
    expect(searchButton().disabled).toBe(true);
    fireEvent.keyDown(queryBox(), { key: 'Enter' });
    expect(mddbClient.vectorSearch).toHaveBeenCalledTimes(1);
  });

  test('thumbs mark results, the summary counts them, the next search sends them, Clear drops them', async () => {
    vi.mocked(mddbClient.vectorSearch).mockResolvedValue({ results });
    renderPanel();
    fireEvent.click(searchButton());
    await screen.findByText('3 results found');
    expect(screen.queryByText(/Feedback for the next search/)).toBeNull();

    const up = screen.getByRole('button', { name: 'Mark alpha relevant' });
    const down = screen.getByRole('button', { name: 'Mark beta not relevant' });
    fireEvent.click(up);
    fireEvent.click(down);
    expect(up.getAttribute('aria-pressed')).toBe('true');
    expect(down.getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByText('Feedback for the next search: 1 relevant, 1 not relevant')).toBeTruthy();

    // A second mark on the same result switches it; the same mark again unsets it.
    fireEvent.click(screen.getByRole('button', { name: 'Mark beta relevant' }));
    expect(screen.getByText('Feedback for the next search: 2 relevant, 0 not relevant')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Mark beta relevant' }));
    fireEvent.click(screen.getByRole('button', { name: 'Mark beta not relevant' }));
    expect(screen.getByRole('button', { name: 'Mark beta relevant' }).getAttribute('aria-pressed')).toBe('false');

    fireEvent.click(searchButton());
    await screen.findByText('3 results found');
    expect(sent(1).positive).toEqual(['alpha']);
    expect(sent(1).negative).toEqual(['beta']);

    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    expect(screen.queryByText(/Feedback for the next search/)).toBeNull();
    expect(screen.getByRole('button', { name: 'Mark alpha relevant' }).getAttribute('aria-pressed')).toBe('false');
  });

  test('a failure shows in red and clears results, stats and plan', async () => {
    vi.mocked(mddbClient.vectorSearch)
      .mockResolvedValueOnce({ results, searchStats: { durationMs: 1, indexSize: 2 }, plan: { algorithm: 'flat', candidates: 3 } })
      .mockRejectedValueOnce(new Error('API Error (500): boom'));
    renderPanel();
    fireEvent.click(searchButton());
    await screen.findByText('3 results found');
    fireEvent.click(searchButton());
    const message = await screen.findByText('API Error (500): boom');
    expect(message.className).toContain('text-red-700');
    expect(screen.queryByText('3 results found')).toBeNull();
    expect(screen.queryByRole('region', { name: 'Search plan' })).toBeNull();
    expect(screen.queryByText('No results found')).toBeNull();
    expect(useStore.getState().vectorSearchStats).toBeNull();
  });

  test('a training index is not retried and shows in amber', async () => {
    vi.useFakeTimers();
    vi.mocked(mddbClient.vectorSearch).mockRejectedValue(
      unavailable({ error: 'ivf index is training; flat or hnsw answer now', training: true }),
    );
    renderPanel();
    await act(async () => fireEvent.click(searchButton()));
    const message = screen.getByText('ivf index is training; flat or hnsw answer now');
    expect(message.className).toContain('text-amber-800');
    await act(async () => vi.advanceTimersByTimeAsync(10000));
    expect(mddbClient.vectorSearch).toHaveBeenCalledTimes(1);
    expect(useStore.getState().vectorLoading).toBe(false);
  });

  test('a loading index is retried three times every 2s, then its message stays', async () => {
    vi.useFakeTimers();
    vi.mocked(mddbClient.vectorSearch).mockRejectedValue(unavailable({ error: 'vector index loading' }));
    renderPanel();
    await act(async () => fireEvent.click(searchButton()));

    for (let attempt = 1; attempt <= 3; attempt++) {
      const status = screen.getByText(`Vector index is loading... retrying (${attempt}/3)`);
      expect(status.className).toContain('text-amber-800');
      expect(useStore.getState().vectorLoading).toBe(true);
      expect(mddbClient.vectorSearch).toHaveBeenCalledTimes(attempt);
      await act(async () => vi.advanceTimersByTimeAsync(1999));
      expect(mddbClient.vectorSearch).toHaveBeenCalledTimes(attempt);
      await act(async () => vi.advanceTimersByTimeAsync(1));
    }

    expect(mddbClient.vectorSearch).toHaveBeenCalledTimes(4);
    // "loading" in the server's text keeps the amber style.
    expect(screen.getByText('vector index loading').className).toContain('text-amber-800');
    expect(useStore.getState().vectorLoading).toBe(false);
    await act(async () => vi.advanceTimersByTimeAsync(10000));
    expect(mddbClient.vectorSearch).toHaveBeenCalledTimes(4);
  });

  test('a 503 with no body falls back to the default message after the retries', async () => {
    vi.useFakeTimers();
    vi.mocked(mddbClient.vectorSearch).mockRejectedValue(
      Object.assign(new Error('API Error (503)'), { status: 503 }),
    );
    renderPanel();
    await act(async () => fireEvent.click(searchButton()));
    await act(async () => vi.advanceTimersByTimeAsync(6000));
    expect(mddbClient.vectorSearch).toHaveBeenCalledTimes(4);
    expect(screen.getByText('The vector index is still loading. Please retry in a moment.')).toBeTruthy();
  });

  test('a retry that succeeds shows the results and ends loading', async () => {
    vi.useFakeTimers();
    vi.mocked(mddbClient.vectorSearch)
      .mockRejectedValueOnce(unavailable({ error: 'loading' }))
      .mockResolvedValueOnce({ results: [results[0]] });
    renderPanel();
    await act(async () => fireEvent.click(searchButton()));
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeTruthy();
    await act(async () => vi.advanceTimersByTimeAsync(2000));
    expect(screen.getByText('1 result found')).toBeTruthy();
    expect(screen.queryByText(/retrying/)).toBeNull();
    expect(useStore.getState().vectorLoading).toBe(false);
    expect(searchButton()).toBeTruthy();
  });

  test('Cancel while a retry waits stops it', async () => {
    vi.useFakeTimers();
    vi.mocked(mddbClient.vectorSearch).mockRejectedValue(unavailable({ error: 'loading' }));
    renderPanel();
    await act(async () => fireEvent.click(searchButton()));
    expect(screen.getByText('Vector index is loading... retrying (1/3)')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByText(/retrying/)).toBeNull();
    expect(useStore.getState().vectorLoading).toBe(false);
    await act(async () => vi.advanceTimersByTimeAsync(10000));
    expect(mddbClient.vectorSearch).toHaveBeenCalledTimes(1);
  });

  test('Cancel aborts the request in flight without an error', async () => {
    vi.mocked(mddbClient.vectorSearch).mockImplementation(({ signal }) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    }));
    renderPanel();
    fireEvent.click(searchButton());
    expect(useStore.getState().vectorLoading).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(await screen.findByRole('button', { name: 'Search' })).toBeTruthy();
    expect(sent().signal.aborted).toBe(true);
    expect(useStore.getState().vectorError).toBeNull();
    expect(useStore.getState().vectorLoading).toBe(false);
  });

  test('a new search aborts the one still running', async () => {
    let first;
    vi.mocked(mddbClient.vectorSearch)
      .mockImplementationOnce(({ signal }) => new Promise((_resolve, reject) => {
        first = signal;
        signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
      }))
      .mockResolvedValueOnce({ results: [results[1]] });
    renderPanel();
    fireEvent.click(searchButton());
    // The Cancel button replaces Search; Enter still starts a new search.
    fireEvent.keyDown(queryBox(), { key: 'Enter' });
    expect(first.aborted).toBe(true);
    expect(await screen.findByText('1 result found')).toBeTruthy();
  });

  test('Cancel still stops a search that replaced another', async () => {
    const signals = [];
    vi.mocked(mddbClient.vectorSearch).mockImplementation(({ signal }) => new Promise((_resolve, reject) => {
      signals.push(signal);
      signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    }));
    renderPanel();
    fireEvent.click(searchButton());
    fireEvent.keyDown(queryBox(), { key: 'Enter' });
    // The first search's abort settles; it must not release the second one.
    await act(async () => {});
    expect(useStore.getState().vectorLoading).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(signals[1].aborted).toBe(true);
    expect(await screen.findByRole('button', { name: 'Search' })).toBeTruthy();
    expect(useStore.getState().vectorLoading).toBe(false);
  });

  test('opening a result loads the full document', async () => {
    vi.mocked(mddbClient.vectorSearch).mockResolvedValue({ results });
    let resolveDoc;
    vi.mocked(mddbClient.getDocument).mockReturnValue(new Promise((r) => { resolveDoc = r; }));
    renderPanel();
    fireEvent.click(searchButton());
    await screen.findByText('3 results found');

    fireEvent.click(screen.getByText('alpha').closest('button'));
    expect(useStore.getState().currentDocument).toMatchObject({ key: 'alpha', collection: 'docs', contentMd: 'Loading content...' });
    expect(mddbClient.getDocument).toHaveBeenCalledWith({ collection: 'docs', key: 'alpha', lang: 'en' });
    await act(async () => resolveDoc({ key: 'alpha', contentMd: '# Alpha' }));
    expect(useStore.getState().currentDocument).toEqual({ key: 'alpha', contentMd: '# Alpha', collection: 'docs' });
  });

  test('a result with content shows it at once, and a failed load says why', async () => {
    vi.mocked(mddbClient.vectorSearch).mockResolvedValue({
      results: [{ score: 1, document: { key: 'k', lang: 'en', contentMd: 'inline' } }],
    });
    vi.mocked(mddbClient.getDocument).mockRejectedValue(new Error('gone'));
    renderPanel();
    fireEvent.click(searchButton());
    await screen.findByText('1 result found');
    await act(async () => fireEvent.click(screen.getByText('k').closest('button')));
    expect(useStore.getState().currentDocument).toMatchObject({
      key: 'k', collection: 'docs', contentMd: 'Error loading content: gone',
    });
  });

  test('a result with no document opens nothing', async () => {
    vi.mocked(mddbClient.vectorSearch).mockResolvedValue({ results: [{ score: 0.3 }] });
    renderPanel();
    fireEvent.click(searchButton());
    await screen.findByText('1 result found');
    fireEvent.click(screen.getByText('30%').closest('button'));
    expect(mddbClient.getDocument).not.toHaveBeenCalled();
    expect(useStore.getState().currentDocument).toBeNull();
  });

  test.each([
    ['counts', { embedded: 4, skipped: 2, failed: 1 }, 'Embedded: 4, Skipped: 2, Failed: 1', 'text-green-700'],
    ['an empty answer', {}, 'Embedded: 0, Skipped: 0, Failed: 0', 'text-green-700'],
  ])('Reindex reports %s', async (_label, answer, text, cls) => {
    let resolve;
    vi.mocked(mddbClient.vectorReindex).mockReturnValue(new Promise((r) => { resolve = r; }));
    renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'Reindex Embeddings' }));
    const busy = screen.getByRole('button', { name: 'Reindexing...' });
    expect(busy.disabled).toBe(true);
    expect(mddbClient.vectorReindex).toHaveBeenCalledWith({ collection: 'docs', force: false });
    await act(async () => resolve(answer));
    expect(screen.getByText(text).className).toContain(cls);
    expect(screen.getByRole('button', { name: 'Reindex Embeddings' }).disabled).toBe(false);
  });

  test('Reindex reports a failure, and the next search clears the report', async () => {
    vi.mocked(mddbClient.vectorReindex).mockRejectedValue(new Error('no embedder'));
    vi.mocked(mddbClient.vectorSearch).mockResolvedValue({ results: [] });
    renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'Reindex Embeddings' }));
    const report = await screen.findByText('no embedder');
    expect(report.className).toContain('text-red-700');
    fireEvent.click(searchButton());
    await screen.findByText('No results found');
    expect(screen.queryByText('no embedder')).toBeNull();
  });

  test('Command opens the API command for the current query and closes again', () => {
    renderPanel('pears');
    fireEvent.click(screen.getByRole('button', { name: 'Command' }));
    const heading = screen.getByText('API Command');
    expect(document.body.textContent).toContain('pears');
    fireEvent.click(within(heading.parentElement).getByRole('button'));
    expect(screen.queryByText('API Command')).toBeNull();
  });
});
