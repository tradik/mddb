// @vitest-environment jsdom
// HybridSearchPanel: keyword + semantic search — the request it sends for each
// strategy (alpha, rrf, weighted signals), explain / decomposition and the
// plan, the boost editor, results and their scores, opening a result, the 503
// message (GO-047), other failures, Cancel and the Command modal.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, waitFor, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    hybridSearch: vi.fn(),
    ftsLanguages: vi.fn(),
    getDocument: vi.fn(),
    getMetaKeys: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: HybridSearchPanel } = await import('./HybridSearchPanel');

const initialStore = useStore.getState();

const results = [
  {
    rank: 1,
    combinedScore: 0.8,
    ftsScore: 1.25,
    vectorScore: 0.5,
    matchedTerms: ['red', 'apple'],
    document: { key: 'alpha', lang: 'en', meta: { tag: ['a', 'b'], author: 'x', c: '1', d: '2' } },
  },
  { combinedScore: 0.4, matchedTerms: [], document: { key: 'beta', lang: 'pl' } },
];

const searchButton = () => screen.getByRole('button', { name: 'Search' });
const queryBox = () => screen.getByPlaceholderText('Search with keyword + semantic matching...');
const sent = (n = 0) => vi.mocked(mddbClient.hybridSearch).mock.calls[n][0];

/** Renders the panel on collection `docs` with `query` typed in. */
function renderPanel(query = 'red apple', state = {}) {
  useStore.setState({ currentCollection: 'docs', hybridQuery: query, ...state });
  return render(<HybridSearchPanel />);
}

/** Renders the panel and waits for the language list to load. */
async function renderLoaded(query, state) {
  const view = renderPanel(query, state);
  await screen.findByText('Polish (pl)');
  return view;
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.hybridSearch).mockReset();
  vi.mocked(mddbClient.getDocument).mockReset();
  vi.mocked(mddbClient.getMetaKeys).mockReset().mockResolvedValue({ meta: {} });
  vi.mocked(mddbClient.ftsLanguages).mockReset().mockResolvedValue({
    languages: [{ code: 'en', name: 'English' }, { code: 'pl', name: 'Polish' }],
  });
});
afterEach(cleanup);

describe('HybridSearchPanel', () => {
  test('asks for a collection when none is selected', () => {
    render(<HybridSearchPanel />);
    expect(screen.getByText('Select a collection to search')).toBeTruthy();
  });

  test('a language list that fails or comes back empty leaves only Auto', async () => {
    vi.mocked(mddbClient.ftsLanguages)
      .mockRejectedValueOnce(new Error('down'))
      .mockResolvedValueOnce({});
    renderPanel();
    await act(async () => {});
    const select = screen.getByDisplayValue('Auto (default)');
    expect(select.options).toHaveLength(1);
    cleanup();
    renderPanel();
    await act(async () => {});
    expect(screen.getByDisplayValue('Auto (default)').options).toHaveLength(1);
  });

  test('alpha strategy: sends every control, explain and decomposition, and shows the plan', async () => {
    vi.mocked(mddbClient.hybridSearch).mockResolvedValue({
      results,
      searchStats: { durationMs: 9, totalTokens: 2, queryTerms: ['red', 'apple'] },
      plan: { algorithm: 'auto', candidates: 20, reasons: ['picked hnsw'] },
    });
    await renderLoaded('  red apple  ', { searchFilterMeta: { tag: ['a'] } });

    expect(screen.queryByText('RRF K')).toBeNull();
    expect(screen.queryByText(/Diversity/)).toBeNull();
    const [alpha, topK, threshold] = screen.getAllByRole('slider');
    fireEvent.change(alpha, { target: { value: '70' } });
    fireEvent.change(topK, { target: { value: '15' } });
    fireEvent.change(threshold, { target: { value: '20' } });
    fireEvent.change(screen.getByDisplayValue('Auto (default)'), { target: { value: 'pl' } });
    fireEvent.change(screen.getByDisplayValue('BM25'), { target: { value: 'bm25f' } });
    fireEvent.change(screen.getByDisplayValue('Flat (Exact)'), { target: { value: 'auto' } });
    fireEvent.change(screen.getByDisplayValue('Cosine Similarity'), { target: { value: 'dot_product' } });
    fireEvent.change(screen.getByDisplayValue('Off'), { target: { value: '2' } });
    fireEvent.click(screen.getByLabelText('Include content'));
    fireEvent.click(screen.getByLabelText('Explain plan'));
    fireEvent.click(screen.getByLabelText('Split multi-part questions'));
    expect(screen.getByText('Alpha: 0.70')).toBeTruthy();
    expect(screen.getByText('Top K: 15')).toBeTruthy();
    expect(screen.getByText('Threshold: 20%')).toBeTruthy();

    fireEvent.click(searchButton());
    expect(await screen.findByText('2 results found')).toBeTruthy();
    expect(sent()).toEqual({
      collection: 'docs',
      query: 'red apple',
      topK: 15,
      algorithm: 'bm25f',
      vectorAlgorithm: 'auto',
      alpha: 0.7,
      strategy: 'alpha',
      rrfK: 60,
      signals: undefined,
      fuzzy: 2,
      threshold: 0.2,
      includeContent: true,
      distanceMetric: 'dot_product',
      filterMeta: { tag: ['a'] },
      lang: 'pl',
      boost: {},
      explain: true,
      queryDecomposition: 'auto',
      signal: expect.any(AbortSignal),
    });
    expect(screen.getByText('9ms | 2 tokens | red, apple')).toBeTruthy();
    expect(screen.getByText('picked hnsw')).toBeTruthy();
    // Scores: combined, the two halves, the bar relative to the best result.
    expect(screen.getByText('0.8000')).toBeTruthy();
    expect(screen.getByText('FTS: 1.2500')).toBeTruthy();
    expect(screen.getByText('Vector: 0.5000')).toBeTruthy();
    const bars = document.querySelectorAll('.bg-primary-500.h-1\\.5');
    expect([...bars].map((b) => b.style.width)).toEqual(['100%', '50%']);
    expect(screen.getByText('apple')).toBeTruthy();
    expect(screen.getByText('tag: a, b')).toBeTruthy();
    expect(screen.getByText('author: x')).toBeTruthy();
    expect(screen.queryByText('d: 2')).toBeNull();
    // Rank falls back to the position.
    expect(screen.getByText('2')).toBeTruthy();
  });

  test('rrf strategy: RRF K replaces alpha, and a cleared value falls back to 60', async () => {
    vi.mocked(mddbClient.hybridSearch).mockResolvedValue({ results: [] });
    await renderLoaded();
    fireEvent.change(screen.getByDisplayValue('Alpha Blending'), { target: { value: 'rrf' } });
    expect(screen.queryByText(/Alpha:/)).toBeNull();
    const rrf = screen.getByRole('spinbutton');
    fireEvent.change(rrf, { target: { value: '30' } });
    expect(useStore.getState().hybridRrfK).toBe(30);
    fireEvent.change(rrf, { target: { value: '' } });
    expect(useStore.getState().hybridRrfK).toBe(60);
    fireEvent.change(rrf, { target: { value: '45' } });
    fireEvent.click(searchButton());
    await screen.findByText('No results found');
    expect(sent()).toMatchObject({ strategy: 'rrf', rrfK: 45, signals: undefined, lang: undefined });
  });

  test('weighted strategy: alpha plus the three signals, all sent', async () => {
    vi.mocked(mddbClient.hybridSearch).mockResolvedValue({ results: [] });
    await renderLoaded();
    fireEvent.change(screen.getByDisplayValue('Alpha Blending'), { target: { value: 'weighted' } });
    expect(screen.getByText('Alpha: 0.50')).toBeTruthy();
    const sliders = screen.getAllByRole('slider');
    const byHelp = (id) => sliders.find((s) => s.getAttribute('aria-describedby') === id);
    fireEvent.change(byHelp('signal-diversity-help'), { target: { value: '30' } });
    fireEvent.change(byHelp('signal-proximity-help'), { target: { value: '20' } });
    fireEvent.change(byHelp('signal-freshness-help'), { target: { value: '10' } });
    expect(screen.getByText('Diversity: 0.30')).toBeTruthy();
    expect(screen.getByText('Proximity: 0.20')).toBeTruthy();
    expect(screen.getByText('Freshness: 0.10')).toBeTruthy();
    fireEvent.click(searchButton());
    await screen.findByText('No results found');
    expect(sent()).toMatchObject({
      strategy: 'weighted',
      alpha: 0.5,
      signals: { diversity: 0.3, proximity: 0.2, freshness: 0.1 },
    });
  });

  test('Enter searches; a blank query never does', async () => {
    vi.mocked(mddbClient.hybridSearch).mockResolvedValue({});
    await renderLoaded();
    fireEvent.keyDown(queryBox(), { key: 'a' });
    expect(mddbClient.hybridSearch).not.toHaveBeenCalled();
    fireEvent.keyDown(queryBox(), { key: 'Enter' });
    expect(await screen.findByText('No results found')).toBeTruthy();
    expect(useStore.getState().hybridSearchStats).toBeNull();
    expect(screen.queryByRole('region', { name: 'Search plan' })).toBeNull();

    fireEvent.change(queryBox(), { target: { value: '  ' } });
    expect(searchButton().disabled).toBe(true);
    fireEvent.keyDown(queryBox(), { key: 'Enter' });
    expect(mddbClient.hybridSearch).toHaveBeenCalledTimes(1);
  });

  test('one result, one token, no query terms; an all-zero score draws empty bars', async () => {
    vi.mocked(mddbClient.hybridSearch).mockResolvedValue({
      results: [{ combinedScore: 0, document: { key: 'z', lang: 'en', meta: {} } }],
      searchStats: { durationMs: 3, totalTokens: 1, queryTerms: [] },
    });
    await renderLoaded();
    fireEvent.click(searchButton());
    expect(await screen.findByText('1 result found')).toBeTruthy();
    expect(screen.getByText('3ms | 1 token')).toBeTruthy();
    expect(screen.queryByText(/FTS:/)).toBeNull();
    expect(screen.queryByText(/Vector:/)).toBeNull();
    expect(document.querySelector('.bg-primary-500.h-1\\.5').style.width).toBe('0%');
  });

  test('stats with no query terms field', async () => {
    vi.mocked(mddbClient.hybridSearch).mockResolvedValue({
      results: [results[1]],
      searchStats: { durationMs: 4, totalTokens: 3 },
    });
    await renderLoaded();
    fireEvent.click(searchButton());
    expect(await screen.findByText('4ms | 3 tokens')).toBeTruthy();
  });

  test.each([
    ['a training index (503)', Object.assign(new Error('API Error (503): ivf training'), {
      status: 503, body: { error: 'ivf is training; use flat', training: true },
    }), 'ivf is training; use flat'],
    ['a loading index (503, no body)', Object.assign(new Error('API Error (503)'), { status: 503 }),
      'The vector index is still loading. Please retry in a moment.'],
    ['any other failure', new Error('API Error (500): boom'), 'API Error (500): boom'],
  ])('%s shows its message and clears the results and plan', async (_label, error, message) => {
    vi.mocked(mddbClient.hybridSearch)
      .mockResolvedValueOnce({ results, plan: { algorithm: 'flat', candidates: 1 } })
      .mockRejectedValueOnce(error);
    await renderLoaded();
    fireEvent.click(searchButton());
    await screen.findByText('2 results found');
    fireEvent.click(searchButton());
    expect(await screen.findByText(message)).toBeTruthy();
    expect(screen.queryByText('2 results found')).toBeNull();
    expect(screen.queryByRole('region', { name: 'Search plan' })).toBeNull();
    expect(screen.queryByText('No results found')).toBeNull();
    expect(mddbClient.hybridSearch).toHaveBeenCalledTimes(2);
  });

  test('Cancel aborts the request in flight without an error', async () => {
    vi.mocked(mddbClient.hybridSearch).mockImplementation(({ signal }) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    }));
    await renderLoaded();
    fireEvent.click(searchButton());
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(await screen.findByRole('button', { name: 'Search' })).toBeTruthy();
    expect(sent().signal.aborted).toBe(true);
    expect(useStore.getState().hybridError).toBeNull();
    expect(useStore.getState().hybridLoading).toBe(false);
  });

  test('a new search aborts the one still running', async () => {
    let first;
    vi.mocked(mddbClient.hybridSearch)
      .mockImplementationOnce(({ signal }) => new Promise((_resolve, reject) => {
        first = signal;
        signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
      }))
      .mockResolvedValueOnce({ results: [results[1]] });
    await renderLoaded();
    fireEvent.click(searchButton());
    fireEvent.keyDown(queryBox(), { key: 'Enter' });
    expect(first.aborted).toBe(true);
    expect(await screen.findByText('1 result found')).toBeTruthy();
  });

  test('Cancel still stops a search that replaced another', async () => {
    const signals = [];
    vi.mocked(mddbClient.hybridSearch).mockImplementation(({ signal }) => new Promise((_resolve, reject) => {
      signals.push(signal);
      signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    }));
    await renderLoaded();
    fireEvent.click(searchButton());
    fireEvent.keyDown(queryBox(), { key: 'Enter' });
    // The first search's abort settles; it must not release the second one.
    await act(async () => {});
    expect(useStore.getState().hybridLoading).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(signals[1].aborted).toBe(true);
    expect(await screen.findByRole('button', { name: 'Search' })).toBeTruthy();
    expect(useStore.getState().hybridLoading).toBe(false);
  });

  describe('boost editor', () => {
    const keyBox = () => screen.getByPlaceholderText('tag:featured');
    const valueBox = () => keyBox().nextElementSibling;
    const addButton = () => screen.getByRole('button', { name: 'Add' });

    test('opens and closes', async () => {
      await renderLoaded();
      const toggle = screen.getByRole('button', { name: /Boost \/ Demote/ });
      expect(screen.queryByPlaceholderText('tag:featured')).toBeNull();
      fireEvent.click(toggle);
      expect(keyBox()).toBeTruthy();
      expect(addButton().disabled).toBe(true);
      fireEvent.click(toggle);
      expect(screen.queryByPlaceholderText('tag:featured')).toBeNull();
    });

    test.each([
      ['a key with no colon', 'featured', '2'],
      ['a key starting with a colon', ':featured', '2'],
      ['a key ending with a colon', 'tag:', '2'],
      ['a value that is not a number', 'tag:x', 'abc'],
      ['a zero value', 'tag:x', '0'],
    ])('refuses %s', async (_label, key, value) => {
      await renderLoaded();
      fireEvent.click(screen.getByRole('button', { name: /Boost \/ Demote/ }));
      fireEvent.change(keyBox(), { target: { value: key } });
      fireEvent.change(valueBox(), { target: { value } });
      fireEvent.keyDown(keyBox(), { key: 'Enter' });
      expect(useStore.getState().hybridBoost).toEqual({});
      expect(keyBox().value).toBe(key);
    });

    test('adds with the button and Enter, edits, removes, and the search sends the map', async () => {
      vi.mocked(mddbClient.hybridSearch).mockResolvedValue({ results: [] });
      await renderLoaded();
      fireEvent.click(screen.getByRole('button', { name: /Boost \/ Demote/ }));

      fireEvent.change(keyBox(), { target: { value: ' tag:featured ' } });
      fireEvent.change(valueBox(), { target: { value: '2.5' } });
      fireEvent.click(addButton());
      expect(useStore.getState().hybridBoost).toEqual({ 'tag:featured': 2.5 });
      expect(keyBox().value).toBe('');
      expect(valueBox().value).toBe('1');
      expect(screen.getByText('1 active')).toBeTruthy();

      fireEvent.change(keyBox(), { target: { value: 'status:draft' } });
      fireEvent.change(valueBox(), { target: { value: '-1' } });
      fireEvent.keyDown(valueBox(), { key: 'a' });
      expect(useStore.getState().hybridBoost['status:draft']).toBeUndefined();
      fireEvent.keyDown(valueBox(), { key: 'Enter' });
      expect(useStore.getState().hybridBoost).toEqual({ 'tag:featured': 2.5, 'status:draft': -1 });
      expect(screen.getByText('2 active')).toBeTruthy();

      const row = screen.getByTitle('status:draft').parentElement;
      const edit = within(row).getByRole('spinbutton');
      fireEvent.change(edit, { target: { value: '3' } });
      expect(useStore.getState().hybridBoost['status:draft']).toBe(3);
      fireEvent.change(edit, { target: { value: '' } });
      expect(useStore.getState().hybridBoost['status:draft']).toBe(0);

      fireEvent.click(within(screen.getByTitle('tag:featured').parentElement).getByTitle('Remove boost entry'));
      expect(useStore.getState().hybridBoost).toEqual({ 'status:draft': 0 });

      fireEvent.click(searchButton());
      await screen.findByText('No results found');
      expect(sent().boost).toEqual({ 'status:draft': 0 });
    });
  });

  test('opening a result loads the full document', async () => {
    vi.mocked(mddbClient.hybridSearch).mockResolvedValue({ results });
    let resolveDoc;
    vi.mocked(mddbClient.getDocument).mockReturnValue(new Promise((r) => { resolveDoc = r; }));
    await renderLoaded();
    fireEvent.click(searchButton());
    await screen.findByText('2 results found');
    fireEvent.click(screen.getByText('alpha').closest('button'));
    expect(useStore.getState().currentDocument).toMatchObject({ key: 'alpha', collection: 'docs', contentMd: 'Loading content...' });
    expect(mddbClient.getDocument).toHaveBeenCalledWith({ collection: 'docs', key: 'alpha', lang: 'en' });
    await act(async () => resolveDoc({ key: 'alpha', contentMd: '# A' }));
    expect(useStore.getState().currentDocument).toEqual({ key: 'alpha', contentMd: '# A', collection: 'docs' });
  });

  test('a result with content shows it at once, and a failed load says why', async () => {
    vi.mocked(mddbClient.hybridSearch).mockResolvedValue({
      results: [{ combinedScore: 1, document: { key: 'k', lang: 'en', contentMd: 'inline' } }],
    });
    vi.mocked(mddbClient.getDocument).mockRejectedValue(new Error('gone'));
    await renderLoaded();
    fireEvent.click(searchButton());
    await screen.findByText('1 result found');
    fireEvent.click(screen.getByText('k').closest('button'));
    await waitFor(() => expect(useStore.getState().currentDocument).toMatchObject({ key: 'k', contentMd: 'Error loading content: gone' }));
  });

  test('a result with no document opens nothing', async () => {
    vi.mocked(mddbClient.hybridSearch).mockResolvedValue({ results: [{ combinedScore: 0.5 }] });
    await renderLoaded();
    fireEvent.click(searchButton());
    await screen.findByText('1 result found');
    fireEvent.click(screen.getByText('0.5000').closest('button'));
    expect(mddbClient.getDocument).not.toHaveBeenCalled();
    expect(useStore.getState().currentDocument).toBeNull();
  });

  test('Command opens the API command for the current query and closes again', async () => {
    await renderLoaded('pears', { hybridStrategy: 'weighted', hybridSignals: { diversity: 0.4, proximity: 0, freshness: 0 } });
    fireEvent.click(screen.getByRole('button', { name: 'Command' }));
    const heading = screen.getByText('API Command');
    expect(document.body.textContent).toContain('pears');
    fireEvent.click(within(heading.parentElement).getByRole('button'));
    expect(screen.queryByText('API Command')).toBeNull();
  });
});
