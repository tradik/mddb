// @vitest-environment jsdom
// SearchAdvisorPanel: measuring a collection (SRCH-010) — the profile stats
// (sampled, embedded share, length, vocabulary, RAG-009 summaries, SRCH-014
// lexical provider), every recommended setting with its reasons, warnings,
// failures, and storing the advice as the collection's profile.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { searchAdvisor: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: SearchAdvisorPanel } = await import('./SearchAdvisorPanel');

const initialStore = useStore.getState();

const fullAdvice = {
  profile: {
    documents: 1000,
    sampled: 200,
    embeddedDocuments: 750,
    estimatedVectorBytes: 3 * 1024 * 1024,
    medianWords: 420,
    longDocumentRatio: 0.256,
    termsPerDocument: 1.5,
    summaryDocuments: 120,
    summaryRatio: 0.12,
    semantic: false,
  },
  searchType: 'hybrid',
  ftsAlgorithm: 'bm25',
  vectorAlgorithm: 'hnsw',
  hybridStrategy: 'rrf',
  hybridAlpha: 0,
  retrievalMode: 'chunks',
  topK: 8,
  summaryMode: 'prefer',
  signals: { diversity: 'low' },
  reasons: ['Most documents are embedded.', 'Vocabulary is narrow.'],
  warnings: ['Only a sample was measured.', 'Lexical vectors only.'],
};

/** The value printed under a stat label such as "Documents". */
function stat(label) {
  const box = screen.getByText(label).parentElement;
  const [, value, hint] = box.children;
  return { value: value.textContent, hint: hint?.textContent };
}

/** The recommended settings, as [label, value] pairs. */
function settings() {
  const panel = screen.getByRole('heading', { name: 'Recommended' }).parentElement;
  return [...panel.querySelectorAll('code')].map((code) => [code.previousElementSibling.textContent, code.textContent]);
}

/** Renders the panel for `collection`, measures it with `advice` and waits for the result. */
async function measureWith(advice, collection = 'blog') {
  useStore.setState({ currentCollection: collection });
  vi.mocked(mddbClient.searchAdvisor).mockResolvedValue(advice);
  render(<SearchAdvisorPanel />);
  fireEvent.click(screen.getByRole('button', { name: 'Measure' }));
  await screen.findByRole('button', { name: 'Apply to collection' });
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.searchAdvisor).mockReset();
});
afterEach(cleanup);

describe('SearchAdvisorPanel', () => {
  test('with no collection, Measure is disabled and the panel asks for one', () => {
    useStore.setState({ currentCollection: null });
    render(<SearchAdvisorPanel />);
    expect(screen.getByLabelText('Collection').value).toBe('');
    expect(screen.getByRole('button', { name: 'Measure' }).disabled).toBe(true);
    expect(screen.getByText('Pick a collection and measure it.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Apply to collection' })).toBeNull();
  });

  test('measures the typed collection and shows Measuring… meanwhile', async () => {
    let finish;
    vi.mocked(mddbClient.searchAdvisor).mockReturnValue(new Promise((resolve) => (finish = resolve)));
    render(<SearchAdvisorPanel />);
    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'docs' } });
    fireEvent.click(screen.getByRole('button', { name: 'Measure' }));

    const busy = screen.getByRole('button', { name: 'Measuring…' });
    expect(busy.disabled).toBe(true);
    expect(busy.querySelector('svg').getAttribute('class')).toContain('animate-spin');
    expect(screen.queryByText('Pick a collection and measure it.')).toBeNull();
    expect(mddbClient.searchAdvisor).toHaveBeenCalledWith('docs', { apply: false });

    await act(async () => finish(fullAdvice));
    expect(screen.getByRole('button', { name: 'Measure' }).disabled).toBe(false);
  });

  test('shows the full profile: sampled, embedded share, length, vocabulary, summaries, lexical', async () => {
    await measureWith(fullAdvice);
    expect(screen.getByText('What this collection looks like')).toBeTruthy();
    expect(stat('Documents')).toEqual({ value: '1000', hint: '200 sampled' });
    expect(stat('Embedded')).toEqual({ value: '750', hint: '75% of the collection, 3.0 MB of vectors' });
    expect(stat('Median length')).toEqual({ value: '420 words', hint: '26% over 500 words' });
    expect(stat('New terms per document')).toEqual({
      value: '1.50',
      hint: 'Documents look alike to a keyword ranker; query expansion helps',
    });
    expect(stat('Summaries')).toEqual({ value: '120', hint: '12% of documents' });
    expect(stat('Matches by meaning')).toEqual({ value: 'No', hint: 'lexical vectors match spelling, not meaning' });
  });

  test('lists every recommended setting and the reasons', async () => {
    await measureWith(fullAdvice);
    expect(settings()).toEqual([
      ['Search type', 'hybrid'],
      ['Ranking algorithm', 'bm25'],
      ['Vector index', 'hnsw'],
      ['Fusion strategy', 'rrf'],
      ['Alpha', '0'],
      ['Retrieval mode', 'chunks'],
      ['Top K', '8'],
      ['Summary mode', 'prefer'],
      ['Diversity signal', 'low'],
    ]);
    const reasons = screen.getByRole('heading', { name: 'Why' }).parentElement.querySelectorAll('li');
    expect([...reasons].map((li) => li.textContent)).toEqual(fullAdvice.reasons);
  });

  test('shows each warning', async () => {
    await measureWith(fullAdvice);
    const warnings = screen.getAllByRole('alert');
    expect(warnings).toHaveLength(1);
    expect([...warnings[0].querySelectorAll('span')].map((s) => s.textContent)).toEqual(fullAdvice.warnings);
  });

  test('a sparse answer leaves out hints, optional stats and empty settings', async () => {
    await measureWith({ profile: {}, hybridAlpha: 0.5, topK: 0, warnings: [] });
    expect(stat('Documents')).toEqual({ value: '0', hint: undefined });
    expect(stat('Embedded')).toEqual({ value: '0', hint: undefined });
    expect(stat('Median length')).toEqual({ value: '0 words', hint: undefined });
    expect(stat('New terms per document')).toEqual({ value: '0.00', hint: undefined });
    expect(screen.queryByText('Summaries')).toBeNull();
    expect(screen.queryByText('Matches by meaning')).toBeNull();
    expect(settings()).toEqual([['Top K', '0']]);
    expect(screen.getByRole('heading', { name: 'Why' }).parentElement.querySelectorAll('li')).toHaveLength(0);
    expect(screen.queryByRole('alert')).toBeNull();
  });

  test.each([
    [
      'an unsampled, fully embedded profile without a vector size',
      { documents: 10, sampled: 10, embeddedDocuments: 10, termsPerDocument: 8, summaryDocuments: 3, semantic: true },
      { embedded: '100% of the collection', documents: undefined, vocab: 'Varied vocabulary; exact term matching discriminates well', summaries: '0% of documents' },
    ],
    [
      'a moderately varied profile',
      { documents: 4, embeddedDocuments: 1, termsPerDocument: 3, summaryDocuments: 4, summaryRatio: 1 },
      { embedded: '25% of the collection', documents: undefined, vocab: 'Moderately varied vocabulary', summaries: '100% of documents' },
    ],
  ])('hints for %s', async (_label, profile, expected) => {
    await measureWith({ profile });
    expect(stat('Embedded').hint).toBe(expected.embedded);
    expect(stat('Documents').hint).toBe(expected.documents);
    expect(stat('New terms per document').hint).toBe(expected.vocab);
    expect(stat('Summaries').hint).toBe(expected.summaries);
    expect(screen.queryByText('Matches by meaning')).toBeNull();
  });

  test('an answer without a profile shows only the Apply button', async () => {
    await measureWith({ searchType: 'fts' });
    expect(screen.queryByText('What this collection looks like')).toBeNull();
    expect(screen.queryByText('Pick a collection and measure it.')).toBeNull();
  });

  test('Apply stores the advice as the profile; editing the name clears the mark', async () => {
    await measureWith(fullAdvice);
    let finish;
    vi.mocked(mddbClient.searchAdvisor).mockReturnValue(new Promise((resolve) => (finish = resolve)));
    fireEvent.click(screen.getByRole('button', { name: 'Apply to collection' }));

    expect(screen.getByRole('button', { name: 'Storing…' }).disabled).toBe(true);
    expect(mddbClient.searchAdvisor).toHaveBeenLastCalledWith('blog', { apply: true });

    await act(async () => finish({ ...fullAdvice, topK: 12 }));
    const stored = screen.getByRole('button', { name: 'Stored as the profile' });
    expect(stored.querySelector('svg')).not.toBeNull();
    expect(settings()).toContainEqual(['Top K', '12']);

    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'blog2' } });
    const again = screen.getByRole('button', { name: 'Apply to collection' });
    expect(again.querySelector('svg')).toBeNull();
  });

  test.each([
    ['the error message', new Error('API Error (404): collection not found'), 'API Error (404): collection not found'],
    ['a non-Error rejection as text', 'connection reset', 'connection reset'],
  ])('reports %s', async (_label, failure, shown) => {
    useStore.setState({ currentCollection: 'blog' });
    vi.mocked(mddbClient.searchAdvisor).mockRejectedValue(failure);
    render(<SearchAdvisorPanel />);
    fireEvent.click(screen.getByRole('button', { name: 'Measure' }));
    expect((await screen.findByRole('alert')).textContent).toBe(shown);
    expect(screen.queryByText('Pick a collection and measure it.')).toBeNull();
    expect(screen.getByRole('button', { name: 'Measure' }).disabled).toBe(false);
  });

  test('switching the current collection drops the previous advice', async () => {
    await measureWith(fullAdvice);
    await act(async () => useStore.setState({ currentCollection: 'docs' }));
    expect(screen.getByLabelText('Collection').value).toBe('docs');
    expect(screen.queryByText('What this collection looks like')).toBeNull();
    expect(screen.getByText('Pick a collection and measure it.')).toBeTruthy();
  });
});
