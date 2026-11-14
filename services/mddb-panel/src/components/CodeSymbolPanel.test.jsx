// @vitest-environment jsdom
// CodeSymbolPanel (2.18.8): looking a symbol up, the relations and their
// passages, truncation, errors, cancel, and the lazily loaded language table.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { codeSymbol: vi.fn(), codeLanguages: vi.fn() } }));
vi.mock('./CodeGraphCard', () => ({ default: ({ collection }) => <div>code graph for {collection}</div> }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: CodeSymbolPanel } = await import('./CodeSymbolPanel');
const { passageLocation } = await import('../lib/code-symbol');
const client = vi.mocked(mddbClient);
const initialStore = useStore.getState();

const answer = {
  collection: 'repo',
  symbol: 'SearchHybrid',
  definitions: [{ path: 'internal/search/query.go', lines: { start: 4, end: 7 }, symbol: 'SearchHybrid', kind: 'function', content: 'func SearchHybrid() {}' }],
  callers: [{ path: 'cmd/tool.go', lines: { start: 3, end: 3 }, symbol: 'run' }],
  tests: [],
  truncated: true,
};

beforeEach(() => {
  useStore.setState(initialStore, true);
  useStore.setState({ stats: { collections: [{ name: 'repo' }, { name: 'docs' }] }, currentCollection: '' });
  client.codeSymbol.mockReset();
  client.codeLanguages.mockReset();
});
afterEach(cleanup);

const lookUp = (name = 'SearchHybrid') => {
  fireEvent.change(screen.getByLabelText('Symbol'), { target: { value: name } });
  fireEvent.click(screen.getByRole('button', { name: /Look up/ }));
};

describe('passageLocation', () => {
  test.each([
    [{ path: 'a.go', lines: { start: 1, end: 5 } }, 'a.go:1-5'],
    [{ path: 'a.go', lines: { start: 2, end: 2 } }, 'a.go:2'],
    [{ path: 'a.go', lines: { start: 2 } }, 'a.go:2'],
    [{ path: 'a.go' }, 'a.go'],
  ])('%o → %s', (p, want) => expect(passageLocation(p)).toBe(want));
});

describe('CodeSymbolPanel', () => {
  test('looks a symbol up and shows each relation', async () => {
    client.codeSymbol.mockResolvedValue(answer);
    render(<CodeSymbolPanel />);
    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'docs' } });
    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'repo' } });
    expect(screen.getByText('code graph for repo')).toBeTruthy();
    fireEvent.click(screen.getByLabelText('Siblings'));
    fireEvent.click(screen.getByLabelText('Siblings'));
    fireEvent.click(screen.getByLabelText('References'));
    fireEvent.click(screen.getByLabelText('Include source text'));
    fireEvent.change(screen.getByLabelText('Limit per relation'), { target: { value: '150' } });
    lookUp(' SearchHybrid ');
    expect(await screen.findByText('internal/search/query.go:4-7')).toBeTruthy();
    const call = client.codeSymbol.mock.calls[0][0];
    expect(call).toMatchObject({
      collection: 'repo', name: 'SearchHybrid', limit: 150, includeContent: true,
      include: ['definitions', 'callers', 'callees', 'tests', 'siblings'],
    });
    expect(call.signal).toBeInstanceOf(AbortSignal);
    expect(screen.getByText('func SearchHybrid() {}')).toBeTruthy();
    expect(screen.getByText('cmd/tool.go:3')).toBeTruthy();
    expect(screen.getByText('in run')).toBeTruthy();
    expect(screen.getByText('None.')).toBeTruthy();
    expect(screen.getByText('Some relations were cut at the limit.')).toBeTruthy();
    expect(screen.queryByText('Siblings', { selector: 'h3' })).toBeNull();
  });

  test('a heuristic answer says so in words', async () => {
    client.codeSymbol.mockResolvedValue(answer);
    render(<CodeSymbolPanel />);
    lookUp();
    expect((await screen.findByText(/Precision:/)).textContent).toBe('Precision: heuristic');
    expect(screen.queryByRole('region', { name: 'Candidates' })).toBeNull();
  });

  test('semantic answer: precision, source, implementations and a candidate to pick (2.19.3)', async () => {
    client.codeSymbol
      .mockResolvedValueOnce({
        collection: 'repo', symbol: 'Close', precision: 'semantic', semanticSource: 'gopls/0.24.0', ambiguous: true,
        definitions: [{ path: 'net/conn.go', lines: { start: 5, end: 7 }, symbol: 'Conn.Close', kind: 'method' }],
        references: [{ path: 'app/main.go', lines: { start: 4, end: 4 }, symbol: 'run', target: 'net/conn.go#Conn.Close' }],
        candidates: [
          { path: 'file/file.go', name: 'File.Close', kind: 'method' },
          { path: 'net/conn.go', name: 'Conn.Close', kind: 'method' },
        ],
      })
      .mockResolvedValueOnce({ collection: 'repo', symbol: 'Conn.Close', precision: 'mixed', definitions: [] });
    render(<CodeSymbolPanel />);
    fireEvent.click(screen.getByLabelText('Implementations'));
    fireEvent.change(screen.getByLabelText('Definition file (optional)'), { target: { value: ' ' } });
    lookUp('Close');
    expect((await screen.findByText(/Precision:/)).textContent).toBe('Precision: semantic(gopls/0.24.0)');
    expect(client.codeSymbol.mock.calls[0][0]).toMatchObject({ path: '', include: expect.arrayContaining(['implementations']) });
    expect(screen.getByText('net/conn.go#Conn.Close')).toBeTruthy();
    // Asked for but absent from the answer: shown as none.
    expect(screen.getByText('Implementations', { selector: 'h3' })).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Candidates' }).textContent).toContain('This name has 2 definitions');
    fireEvent.click(screen.getByRole('button', { name: /File\.Close/ }));
    expect(await screen.findByText('Precision: mixed')).toBeTruthy();
    expect(client.codeSymbol.mock.calls[1][0]).toMatchObject({ name: 'File.Close', path: 'file/file.go' });
    expect(screen.getByLabelText('Symbol').value).toBe('File.Close');
    expect(screen.getByLabelText('Definition file (optional)').value).toBe('file/file.go');
  });

  test('ambiguous without candidates shows no picker', async () => {
    client.codeSymbol.mockResolvedValue({ collection: 'repo', symbol: 'X', ambiguous: true, definitions: [] });
    render(<CodeSymbolPanel />);
    lookUp('X');
    await screen.findByText(/Precision:/);
    expect(screen.queryByRole('region', { name: 'Candidates' })).toBeNull();
  });

  test('an empty limit falls back to 20; no truncation note', async () => {
    client.codeSymbol.mockResolvedValue({ collection: 'repo', symbol: 'X', definitions: [] });
    useStore.setState({ currentCollection: 'docs' });
    render(<CodeSymbolPanel />);
    fireEvent.change(screen.getByLabelText('Limit per relation'), { target: { value: '' } });
    lookUp('X');
    await screen.findByText('None.');
    expect(client.codeSymbol.mock.calls[0][0]).toMatchObject({ collection: 'docs', limit: 20 });
    expect(screen.queryByText(/cut at the limit/)).toBeNull();
  });

  test('look up is off without a name, a collection or a relation', () => {
    useStore.setState({ stats: {} });
    render(<CodeSymbolPanel />);
    const button = screen.getByRole('button', { name: /Look up/ });
    expect(button.disabled).toBe(true);
  });

  test('look up is off when no relation is chosen', () => {
    render(<CodeSymbolPanel />);
    fireEvent.change(screen.getByLabelText('Symbol'), { target: { value: 'X' } });
    for (const label of ['Definitions', 'References', 'Callers', 'Callees', 'Tests', 'Siblings']) {
      fireEvent.click(screen.getByLabelText(label));
    }
    expect(screen.getByRole('button', { name: /Look up/ }).disabled).toBe(true);
  });

  test('shows an error', async () => {
    client.codeSymbol.mockRejectedValue(new Error('Forbidden'));
    render(<CodeSymbolPanel />);
    lookUp();
    expect((await screen.findByRole('alert')).textContent).toBe('Forbidden');
  });

  test('cancel aborts the lookup without an error', async () => {
    client.codeSymbol.mockImplementation(({ signal }) => new Promise((_, reject) => {
      signal.addEventListener('abort', () => {
        const err = new Error('aborted');
        err.name = 'AbortError';
        reject(err);
      });
    }));
    render(<CodeSymbolPanel />);
    lookUp();
    fireEvent.click(await screen.findByRole('button', { name: /Cancel/ }));
    await waitFor(() => expect(screen.queryByRole('button', { name: /Cancel/ })).toBeNull());
    expect(screen.queryByRole('alert')).toBeNull();
  });

  test('a second lookup replaces the first', async () => {
    let first;
    client.codeSymbol
      .mockReturnValueOnce(new Promise((r) => { first = r; }))
      .mockResolvedValueOnce({ collection: 'repo', symbol: 'Second', definitions: [] });
    render(<CodeSymbolPanel />);
    lookUp('First');
    lookUp('Second');
    expect(await screen.findByText('Second')).toBeTruthy();
    first({ collection: 'repo', symbol: 'First' });
    await Promise.resolve();
    expect(client.codeSymbol.mock.calls[0][0].signal.aborted).toBe(true);
  });

  test('loads the language table once, when opened', async () => {
    client.codeLanguages.mockResolvedValue({ extensions: { '.tsx': 'typescript', '.go': 'go' } });
    const { container } = render(<CodeSymbolPanel />);
    const details = container.querySelector('details');
    details.open = true;
    fireEvent(details, new Event('toggle'));
    expect(screen.getByText('Loading…')).toBeTruthy();
    expect(await screen.findByText('.go')).toBeTruthy();
    expect(screen.getByText('.tsx')).toBeTruthy();
    fireEvent(details, new Event('toggle'));
    details.open = false;
    fireEvent(details, new Event('toggle'));
    expect(client.codeLanguages).toHaveBeenCalledTimes(1);
  });

  test('an empty language table and a failed one', async () => {
    client.codeLanguages.mockResolvedValueOnce({}).mockRejectedValueOnce(new Error('down'));
    const first = render(<CodeSymbolPanel />);
    let details = first.container.querySelector('details');
    details.open = true;
    fireEvent(details, new Event('toggle'));
    await waitFor(() => expect(screen.queryByText('Loading…')).toBeNull());
    first.unmount();
    const second = render(<CodeSymbolPanel />);
    details = second.container.querySelector('details');
    details.open = true;
    fireEvent(details, new Event('toggle'));
    expect((await screen.findByRole('alert')).textContent).toBe('down');
  });
});
