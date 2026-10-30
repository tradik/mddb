// @vitest-environment jsdom
// CrossSearchPanel: one semantic search over several collections — text vs
// document source, target collections, the request it sends (explain via the
// real planningOptions), results with per-collection plans, opening a result,
// failures incl. the 503 message (GO-047), and Cancel.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', async (importOriginal) => ({
  ...(await importOriginal()),
  default: { crossSearch: vi.fn(), getDocument: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: CrossSearchPanel } = await import('./CrossSearchPanel');

const initialStore = useStore.getState();

const results = [
  {
    rank: 1,
    score: 0.87,
    collection: 'docs',
    document: { key: 'alpha', lang: 'en', meta: { tag: ['a', 'b'], author: 'x', c: '1', d: '2' } },
  },
  { score: 0.42, collection: 'blog', document: { key: 'beta', lang: 'pl', meta: {} } },
];

const searchButton = () => screen.getByRole('button', { name: 'Search' });
const queryBox = () => screen.getByPlaceholderText("Describe what you're looking for across collections...");
const sent = (n = 0) => vi.mocked(mddbClient.crossSearch).mock.calls[n];

/** Renders the panel with collections `docs` and `blog`. */
function renderPanel(collections = [{ name: 'docs' }, { name: 'blog' }]) {
  useStore.setState({ stats: { collections } });
  return render(<CrossSearchPanel />);
}

/** A request that only settles when aborted. */
function hanging({ signal }) {
  return new Promise((_resolve, reject) => {
    signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
  });
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.crossSearch).mockReset();
  vi.mocked(mddbClient.getDocument).mockReset();
});
afterEach(cleanup);

describe('CrossSearchPanel', () => {
  test('with no stats there are no collections and the empty state shows', () => {
    render(<CrossSearchPanel />);
    expect(screen.getByText('No collections available')).toBeTruthy();
    expect(screen.queryByText('All collections will be searched if none selected')).toBeNull();
    expect(screen.getByText('Search across multiple collections')).toBeTruthy();
    expect(searchButton().disabled).toBe(true);
  });

  test('text query: sends every control, explain, and shows results with per-collection plans', async () => {
    vi.mocked(mddbClient.crossSearch).mockResolvedValue({
      results,
      total: 2,
      durationMs: 14,
      collectionsSearched: 2,
      plan: { collections: { docs: { algorithm: 'hnsw', candidates: 20 }, blog: { algorithm: 'flat', candidates: 5 } } },
    });
    renderPanel();
    expect(screen.getByText('All collections will be searched if none selected')).toBeTruthy();
    expect(screen.queryByText('Split multi-part questions')).toBeNull();

    fireEvent.change(queryBox(), { target: { value: '  red apple  ' } });
    fireEvent.click(screen.getByLabelText('blog'));
    expect(screen.queryByText('All collections will be searched if none selected')).toBeNull();
    expect(screen.getByText('blog').className).toContain('bg-primary-100');
    expect(screen.getByText('docs').className).toContain('bg-gray-50');
    const [topK, threshold] = screen.getAllByRole('slider');
    fireEvent.change(topK, { target: { value: '30' } });
    fireEvent.change(threshold, { target: { value: '25' } });
    fireEvent.change(screen.getByDisplayValue('Flat (Exact)'), { target: { value: 'auto' } });
    fireEvent.change(screen.getByDisplayValue('Cosine Similarity'), { target: { value: 'euclidean' } });
    fireEvent.click(screen.getByLabelText('Include content'));
    fireEvent.click(screen.getByLabelText('Explain plan'));
    expect(screen.getByText('Top K: 30')).toBeTruthy();
    expect(screen.getByText('Threshold: 25%')).toBeTruthy();

    fireEvent.click(searchButton());
    expect(await screen.findByText('2 results found')).toBeTruthy();
    const [params, options] = sent();
    expect(params).toEqual({
      targetCollections: ['blog'],
      topK: 30,
      threshold: 0.25,
      algorithm: 'auto',
      distanceMetric: 'euclidean',
      includeContent: true,
      explain: true,
      query: 'red apple',
    });
    expect(options.signal).toBeInstanceOf(AbortSignal);
    expect(screen.getByText('14ms | 2 collections searched')).toBeTruthy();
    const plan = screen.getByRole('region', { name: 'Search plan' });
    expect(plan.textContent).toContain('hnsw');
    expect(plan.textContent).toContain('flat');
    expect(screen.getByText('87%')).toBeTruthy();
    expect(screen.getByText('tag: a, b')).toBeTruthy();
    expect(screen.getByText('author: x')).toBeTruthy();
    expect(screen.queryByText('d: 2')).toBeNull();
    expect(screen.getByText('2')).toBeTruthy();
    expect(useStore.getState().crossSearchLoading).toBe(false);
  });

  test('unticking a target and leaving explain off sends neither', async () => {
    vi.mocked(mddbClient.crossSearch).mockResolvedValue({});
    renderPanel();
    fireEvent.change(queryBox(), { target: { value: 'q' } });
    fireEvent.click(screen.getByLabelText('docs'));
    fireEvent.click(screen.getByLabelText('blog'));
    fireEvent.click(screen.getByLabelText('docs'));
    fireEvent.click(searchButton());
    expect(await screen.findByText('No results found')).toBeTruthy();
    expect(sent()[0]).toEqual({
      targetCollections: ['blog'],
      topK: 10,
      threshold: 0,
      algorithm: 'flat',
      distanceMetric: 'cosine',
      includeContent: false,
      query: 'q',
    });
    expect(screen.queryByRole('region', { name: 'Search plan' })).toBeNull();
  });

  test('Enter searches, Shift+Enter does not, and a blank query never searches', async () => {
    vi.mocked(mddbClient.crossSearch).mockResolvedValue({
      results: [results[1]], durationMs: 2, collectionsSearched: 1,
    });
    renderPanel();
    fireEvent.change(queryBox(), { target: { value: '   ' } });
    expect(searchButton().disabled).toBe(true);
    fireEvent.keyDown(queryBox(), { key: 'Enter' });
    expect(mddbClient.crossSearch).not.toHaveBeenCalled();

    fireEvent.change(queryBox(), { target: { value: 'pears' } });
    fireEvent.keyDown(queryBox(), { key: 'Enter', shiftKey: true });
    fireEvent.keyDown(queryBox(), { key: 'x' });
    expect(mddbClient.crossSearch).not.toHaveBeenCalled();
    fireEvent.keyDown(queryBox(), { key: 'Enter' });
    expect(await screen.findByText('1 result found')).toBeTruthy();
    expect(screen.getByText('2ms | 1 collection searched')).toBeTruthy();
    expect(sent()[0].targetCollections).toBeUndefined();
  });

  test('document source: needs a collection and a key, then sends them instead of a query', async () => {
    vi.mocked(mddbClient.crossSearch).mockResolvedValue({ results: [] });
    renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'Document' }));
    expect(screen.queryByPlaceholderText(/across collections/)).toBeNull();
    expect(screen.getByRole('button', { name: 'Document' }).className).toContain('bg-primary-100');
    const select = screen.getByDisplayValue('Select...');
    expect([...select.options].map((o) => o.value)).toEqual(['', 'docs', 'blog']);
    expect(searchButton().disabled).toBe(true);

    fireEvent.change(select, { target: { value: 'docs' } });
    expect(searchButton().disabled).toBe(true);
    const docId = screen.getByPlaceholderText('doc-key');
    fireEvent.change(docId, { target: { value: '   ' } });
    expect(searchButton().disabled).toBe(true);
    fireEvent.click(searchButton());
    expect(mddbClient.crossSearch).not.toHaveBeenCalled();

    fireEvent.change(docId, { target: { value: ' guide ' } });
    fireEvent.click(searchButton());
    expect(await screen.findByText('No results found')).toBeTruthy();
    expect(sent()[0]).toMatchObject({ sourceCollection: 'docs', sourceDocID: 'guide' });
    expect(sent()[0].query).toBeUndefined();

    fireEvent.click(screen.getByRole('button', { name: 'Text Query' }));
    expect(queryBox()).toBeTruthy();
  });

  test('document source with no collection never searches', () => {
    renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'Document' }));
    fireEvent.change(screen.getByPlaceholderText('doc-key'), { target: { value: 'guide' } });
    expect(searchButton().disabled).toBe(true);
    fireEvent.click(searchButton());
    expect(mddbClient.crossSearch).not.toHaveBeenCalled();
  });

  test.each([
    ['a training index (503)', Object.assign(new Error('API Error (503): training'), {
      status: 503, body: { error: 'pq is training; use flat', training: true },
    }), 'pq is training; use flat'],
    ['any other failure', new Error('API Error (500): boom'), 'API Error (500): boom'],
  ])('%s shows its message and clears the results and plan', async (_label, error, message) => {
    vi.mocked(mddbClient.crossSearch)
      .mockResolvedValueOnce({ results, plan: { collections: { docs: { algorithm: 'flat', candidates: 1 } } } })
      .mockRejectedValueOnce(error);
    renderPanel();
    fireEvent.change(queryBox(), { target: { value: 'q' } });
    fireEvent.click(searchButton());
    await screen.findByText('2 results found');
    fireEvent.click(searchButton());
    expect(await screen.findByText(message)).toBeTruthy();
    expect(screen.queryByText('2 results found')).toBeNull();
    expect(screen.queryByRole('region', { name: 'Search plan' })).toBeNull();
    expect(screen.queryByText('No results found')).toBeNull();
  });

  test('Cancel aborts the request in flight without an error', async () => {
    vi.mocked(mddbClient.crossSearch).mockImplementation((_params, options) => hanging(options));
    renderPanel();
    fireEvent.change(queryBox(), { target: { value: 'q' } });
    fireEvent.click(searchButton());
    expect(useStore.getState().crossSearchLoading).toBe(true);
    expect(screen.queryByText('No results found')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(await screen.findByRole('button', { name: 'Search' })).toBeTruthy();
    expect(sent()[1].signal.aborted).toBe(true);
    expect(useStore.getState().crossSearchError).toBeNull();
  });

  test('a new search aborts the one still running', async () => {
    let first;
    vi.mocked(mddbClient.crossSearch)
      .mockImplementationOnce((_params, options) => { first = options.signal; return hanging(options); })
      .mockResolvedValueOnce({ results: [results[1]] });
    renderPanel();
    fireEvent.change(queryBox(), { target: { value: 'q' } });
    fireEvent.click(searchButton());
    fireEvent.keyDown(queryBox(), { key: 'Enter' });
    expect(first.aborted).toBe(true);
    expect(await screen.findByText('1 result found')).toBeTruthy();
  });

  test('Cancel still stops a search that replaced another', async () => {
    const signals = [];
    vi.mocked(mddbClient.crossSearch).mockImplementation((_params, options) => {
      signals.push(options.signal);
      return hanging(options);
    });
    renderPanel();
    fireEvent.change(queryBox(), { target: { value: 'q' } });
    fireEvent.click(searchButton());
    fireEvent.keyDown(queryBox(), { key: 'Enter' });
    // The first search's abort settles; it must not release the second one.
    await act(async () => {});
    expect(useStore.getState().crossSearchLoading).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(signals[1].aborted).toBe(true);
    expect(await screen.findByRole('button', { name: 'Search' })).toBeTruthy();
    expect(useStore.getState().crossSearchLoading).toBe(false);
  });

  test('opening a result loads it from its own collection', async () => {
    vi.mocked(mddbClient.crossSearch).mockResolvedValue({ results });
    let resolveDoc;
    vi.mocked(mddbClient.getDocument).mockReturnValue(new Promise((r) => { resolveDoc = r; }));
    renderPanel();
    fireEvent.change(queryBox(), { target: { value: 'q' } });
    fireEvent.click(searchButton());
    await screen.findByText('2 results found');
    fireEvent.click(screen.getByText('beta').closest('button'));
    expect(useStore.getState().currentDocument).toMatchObject({ key: 'beta', collection: 'blog', contentMd: 'Loading content...' });
    expect(mddbClient.getDocument).toHaveBeenCalledWith({ collection: 'blog', key: 'beta', lang: 'pl' });
    await act(async () => resolveDoc({ key: 'beta', contentMd: '# B' }));
    expect(useStore.getState().currentDocument).toEqual({ key: 'beta', contentMd: '# B', collection: 'blog' });
  });

  test('a result with content shows it at once, and a failed load says why', async () => {
    vi.mocked(mddbClient.crossSearch).mockResolvedValue({
      results: [{ score: 1, collection: 'docs', document: { key: 'k', lang: 'en', contentMd: 'inline' } }],
    });
    vi.mocked(mddbClient.getDocument).mockRejectedValue(new Error('gone'));
    renderPanel();
    fireEvent.change(queryBox(), { target: { value: 'q' } });
    fireEvent.click(searchButton());
    await screen.findByText('1 result found');
    await act(async () => fireEvent.click(screen.getByText('k').closest('button')));
    expect(useStore.getState().currentDocument).toMatchObject({
      key: 'k', collection: 'docs', contentMd: 'Error loading content: gone',
    });
  });

  test('a result with no document opens nothing', async () => {
    vi.mocked(mddbClient.crossSearch).mockResolvedValue({ results: [{ score: 0.3, collection: 'docs' }] });
    renderPanel();
    fireEvent.change(queryBox(), { target: { value: 'q' } });
    fireEvent.click(searchButton());
    await screen.findByText('1 result found');
    fireEvent.click(screen.getByText('30%').closest('button'));
    expect(mddbClient.getDocument).not.toHaveBeenCalled();
    expect(useStore.getState().currentDocument).toBeNull();
  });
});
