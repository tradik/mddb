// @vitest-environment jsdom
// EvalPanel (SRCH-019): the stored evaluation set of a collection (list, add,
// delete) and a run under chosen configurations, cancellable, with the best
// nDCG highlighted and per-configuration errors shown.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { listEvalQueries: vi.fn(), saveEvalQuery: vi.fn(), deleteEvalQuery: vi.fn(), runEval: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: EvalPanel } = await import('./EvalPanel');

const initialStore = useStore.getState();

const stored = {
  collection: 'docs',
  queries: [
    { id: 'backup', query: 'how to back up', expectedKeys: ['BACKUP.md'], grades: { 'BACKUP.md': 3 } },
    { id: 'plain', query: 'plain one', expectedKeys: ['A.md'] },
    { id: 'bare', query: 'no keys' },
  ],
};

const runReply = {
  collection: 'docs', k: 5, queries: 2,
  results: [
    { config: 'fts/bm25', recallAtK: 0.825, ndcgAtK: 0.739, mrr: 0.723, p50Ms: 0.9, p99Ms: 1.9, tokens: 1580031 },
    { config: 'hybrid/rrf', recallAtK: 0.875, ndcgAtK: 0.783, mrr: 0.765, p50Ms: 10.7, p99Ms: 17.3 },
    { config: 'vector', error: 'no embedding provider' },
  ],
};

beforeEach(() => {
  useStore.setState(initialStore, true);
  useStore.setState({ currentCollection: 'docs', stats: { collections: [{ name: 'docs' }, { name: 'kb' }] } });
  for (const fn of Object.values(mddbClient)) vi.mocked(fn).mockReset();
  vi.mocked(mddbClient.listEvalQueries).mockResolvedValue(stored);
});
afterEach(cleanup);

const fillForm = ({ id = 'q1', query = 'what', keys = 'A.md, B.md', grades = '' } = {}) => {
  fireEvent.change(screen.getByLabelText('Id (an existing id is replaced)'), { target: { value: id } });
  fireEvent.change(screen.getByLabelText('Query'), { target: { value: query } });
  fireEvent.change(screen.getByLabelText('Expected keys (comma-separated)'), { target: { value: keys } });
  fireEvent.change(screen.getByLabelText('Grades for nDCG (optional, key=0..3)'), { target: { value: grades } });
};

describe('EvalPanel', () => {
  test('lists the stored set of the current collection', async () => {
    render(<EvalPanel />);
    expect(await screen.findByText('backup')).toBeTruthy();
    expect(screen.getByText('expects BACKUP.md · grades BACKUP.md=3')).toBeTruthy();
    expect(screen.getByText('expects A.md')).toBeTruthy();
    expect(mddbClient.listEvalQueries).toHaveBeenCalledWith('docs');
  });

  test('no collection chosen: only the picker', () => {
    useStore.setState({ currentCollection: null, stats: null });
    render(<EvalPanel />);
    expect(screen.queryByText('Stored queries')).toBeNull();
    expect(mddbClient.listEvalQueries).not.toHaveBeenCalled();
  });

  test.each([[{ queries: [] }], [{}]])('an empty set (%o)', async (reply) => {
    vi.mocked(mddbClient.listEvalQueries).mockResolvedValue(reply);
    render(<EvalPanel />);
    expect(await screen.findByText('No stored queries. Add some below, then run.')).toBeTruthy();
  });

  test('a failed listing is shown', async () => {
    vi.mocked(mddbClient.listEvalQueries).mockRejectedValue(new Error('API Error (403): forbidden'));
    render(<EvalPanel />);
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (403): forbidden');
  });

  test('switching collection reloads the set', async () => {
    render(<EvalPanel />);
    await screen.findByText('backup');
    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'kb' } });
    await waitFor(() => expect(mddbClient.listEvalQueries).toHaveBeenCalledWith('kb'));
  });

  test('saves a query with keys and grades, then reloads', async () => {
    vi.mocked(mddbClient.saveEvalQuery).mockResolvedValue({});
    render(<EvalPanel />);
    await screen.findByText('backup');
    const save = screen.getByRole('button', { name: 'Save query' });
    expect(save.disabled).toBe(true);
    fillForm({ id: ' q1 ', query: ' what ', grades: 'A.md=2' });
    fireEvent.click(save);
    await waitFor(() => expect(mddbClient.listEvalQueries).toHaveBeenCalledTimes(2));
    expect(mddbClient.saveEvalQuery).toHaveBeenCalledWith({
      collection: 'docs', id: 'q1', query: 'what', expectedKeys: ['A.md', 'B.md'], grades: { 'A.md': 2 },
    });
    expect(screen.getByLabelText('Query').value).toBe('');
  });

  test('a bad grade is reported and nothing is saved', async () => {
    render(<EvalPanel />);
    await screen.findByText('backup');
    fillForm({ grades: 'A.md=9' });
    fireEvent.click(screen.getByRole('button', { name: 'Save query' }));
    expect((await screen.findByRole('alert')).textContent).toBe('Grade "A.md=9" must be key=0..3');
    expect(mddbClient.saveEvalQuery).not.toHaveBeenCalled();
  });

  test('deletes after confirmation; a declined confirm deletes nothing', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false);
    vi.mocked(mddbClient.deleteEvalQuery).mockResolvedValue({});
    render(<EvalPanel />);
    await screen.findByText('backup');
    fireEvent.click(screen.getByRole('button', { name: 'Delete query backup' }));
    expect(mddbClient.deleteEvalQuery).not.toHaveBeenCalled();
    confirmSpy.mockReturnValue(true);
    fireEvent.click(screen.getByRole('button', { name: 'Delete query backup' }));
    await waitFor(() => expect(mddbClient.deleteEvalQuery).toHaveBeenCalledWith('docs', 'backup'));
  });

  test('runs the chosen configurations and highlights the best nDCG', async () => {
    vi.mocked(mddbClient.runEval).mockResolvedValue(runReply);
    render(<EvalPanel />);
    await screen.findByText('backup');
    fireEvent.click(screen.getByLabelText('Vector'));
    fireEvent.click(screen.getByLabelText('Hybrid (alpha 0.5)'));
    fireEvent.click(screen.getByLabelText('Vector'));
    fireEvent.change(screen.getByLabelText('k (1–100)'), { target: { value: '500' } });
    fireEvent.click(screen.getByRole('button', { name: 'Run evaluation' }));
    expect(await screen.findByText('2 queries, k = 5')).toBeTruthy();
    const args = vi.mocked(mddbClient.runEval).mock.calls[0][0];
    expect(args.k).toBe(100);
    expect(args.configs.map((c) => c.name)).toEqual(['fts/bm25', 'hybrid/rrf', 'vector']);
    expect(args.signal).toBeInstanceOf(AbortSignal);
    expect(screen.getByText('best nDCG').closest('tr').textContent).toContain('hybrid/rrf');
    expect(screen.getByText('no embedding provider')).toBeTruthy();
    expect(screen.getByText('1,580,031')).toBeTruthy();
    expect(screen.getByText('0.739')).toBeTruthy();
  });

  test('a run with no results renders an empty table', async () => {
    vi.mocked(mddbClient.runEval).mockResolvedValue({ k: 10, queries: 0 });
    render(<EvalPanel />);
    await screen.findByText('backup');
    fireEvent.click(screen.getByRole('button', { name: 'Run evaluation' }));
    expect(await screen.findByText('0 queries, k = 10')).toBeTruthy();
  });

  test('no configuration chosen: run disabled', async () => {
    render(<EvalPanel />);
    await screen.findByText('backup');
    for (const label of ['Full text (BM25)', 'Hybrid (RRF)', 'Hybrid (alpha 0.5)', 'Vector']) {
      fireEvent.click(screen.getByLabelText(label));
    }
    expect(screen.getByRole('button', { name: 'Run evaluation' }).disabled).toBe(true);
  });

  test('a failed run is shown', async () => {
    vi.mocked(mddbClient.runEval).mockRejectedValue(new Error('no evaluation set'));
    render(<EvalPanel />);
    await screen.findByText('backup');
    fireEvent.click(screen.getByRole('button', { name: 'Run evaluation' }));
    expect((await screen.findByRole('alert')).textContent).toBe('no evaluation set');
  });

  test('cancel aborts the run without an error', async () => {
    vi.mocked(mddbClient.runEval).mockImplementation(({ signal }) => new Promise((_, reject) => {
      signal.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), { name: 'AbortError' })));
    }));
    render(<EvalPanel />);
    await screen.findByText('backup');
    fireEvent.click(screen.getByRole('button', { name: 'Run evaluation' }));
    expect(screen.getByText('Running…')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(await screen.findByRole('button', { name: 'Run evaluation' })).toBeTruthy();
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
