// @vitest-environment jsdom
// Duplicates page: the scan parameters /v1/find-duplicates takes, the groups
// of every mode (exact, similar, minhash), opening a document in the viewer
// and deleting one after a confirmation naming the collection and key.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';
import { duplicateGroups, langOfDocId, formatScore } from '../lib/duplicates-format';

vi.mock('../lib/mddb-client', () => ({ default: { findDuplicates: vi.fn(), getDocument: vi.fn(), deleteDocument: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: DuplicatesPanel } = await import('./DuplicatesPanel');

const initialStore = useStore.getState();
const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: { error: text } });
const answer = {
  totalDocuments: 5,
  totalEmbedded: 4,
  exactGroups: [{ groupId: 1, type: 'exact', score: 1, documents: [
    { docId: 'kb|d1|en', key: 'd1', score: 1 }, { docId: 'kb|d2|pl', key: 'd2', score: 1 },
  ] }],
  minhashGroups: [{ groupId: 1, type: 'minhash', documents: [{ docId: 'kb|a|b|en', key: 'a|b' }, { docId: 'kb|c|en', key: 'c', score: 0.867 }] }],
};

async function scan() {
  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Find duplicates' })));
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  useStore.setState({ stats: { collections: [{ name: 'kb' }, { name: 'docs' }] } });
  for (const fn of Object.values(mddbClient)) fn.mockReset();
  mddbClient.findDuplicates.mockResolvedValue(answer);
});
afterEach(cleanup);

describe('duplicates-format', () => {
  test('groups of every mode, the language from the id, scores as percentages', () => {
    expect(duplicateGroups(null)).toEqual([]);
    expect(duplicateGroups({ similarGroups: [{ groupId: 2, type: 'similar', documents: [] }] })).toEqual([{ groupId: 2, type: 'similar', documents: [], id: 'similar-2' }]);
    expect(langOfDocId('kb|a|b|en_GB')).toBe('en_GB');
    expect(formatScore(0.98246)).toBe('98.2%');
    expect(formatScore(undefined)).toBe('');
  });
});

describe('DuplicatesPanel', () => {
  test('scans with the chosen parameters and lists the groups', async () => {
    render(<DuplicatesPanel />);
    fireEvent.change(screen.getByLabelText('Mode'), { target: { value: 'minhash' } });
    fireEvent.change(screen.getByLabelText('Threshold (0–1)'), { target: { value: '0.5' } });
    fireEvent.change(screen.getByLabelText('Max documents'), { target: { value: '100' } });
    fireEvent.change(screen.getByLabelText('Distance'), { target: { value: 'euclidean' } });
    await scan();
    expect(mddbClient.findDuplicates).toHaveBeenCalledWith({ collection: 'kb', mode: 'minhash', threshold: 0.5, maxDocs: 100, distanceMetric: 'euclidean' });
    expect(screen.getByRole('status').textContent).toBe('Scanned 5 documents (4 with embeddings): 2 groups.');
    const exact = screen.getByRole('region', { name: 'Identical content, group 1' });
    expect(within(exact).getByText('average 100.0%')).toBeTruthy();
    expect(within(exact).getByText('(pl)')).toBeTruthy();
    const minhash = screen.getByRole('region', { name: 'Overlapping text, group 1' });
    expect(within(minhash).getByText('86.7%')).toBeTruthy();
    expect(within(minhash).queryByText(/average/)).toBeNull();
  });

  test('starts from the current collection; nothing to scan without collections; unknown group types keep their name', async () => {
    useStore.setState({ currentCollection: 'docs' });
    mddbClient.findDuplicates.mockResolvedValueOnce({ totalDocuments: 1, totalEmbedded: 0, similarGroups: [{ groupId: 3, type: 'other', documents: [{ docId: 'docs|x|en', key: 'x' }] }] });
    render(<DuplicatesPanel />);
    await scan();
    expect(mddbClient.findDuplicates.mock.lastCall[0].collection).toBe('docs');
    expect(screen.getByRole('region', { name: 'other, group 3' })).toBeTruthy();
    cleanup();
    useStore.setState({ stats: null, currentCollection: null });
    render(<DuplicatesPanel />);
    expect(screen.getByRole('button', { name: 'Find duplicates' }).disabled).toBe(true);
  });

  test('a scan the server refuses shows its words', async () => {
    mddbClient.findDuplicates.mockRejectedValueOnce(apiError(400, 'similar mode needs an embedding provider'));
    render(<DuplicatesPanel />);
    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'docs' } });
    await scan();
    expect(screen.getByRole('alert').textContent).toBe('API Error (400): similar mode needs an embedding provider');
  });

  test('opens a document in the viewer with its language from the id', async () => {
    mddbClient.getDocument.mockResolvedValueOnce({ key: 'd2', lang: 'pl', contentMd: '# x' }).mockRejectedValueOnce(apiError(404, 'not found'));
    render(<DuplicatesPanel />);
    await scan();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Open d2' })));
    expect(mddbClient.getDocument).toHaveBeenCalledWith({ collection: 'kb', key: 'd2', lang: 'pl' });
    expect(useStore.getState().currentDocument).toEqual({ key: 'd2', lang: 'pl', contentMd: '# x', collection: 'kb' });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Open d1' })));
    expect(screen.getByText('API Error (404): not found')).toBeTruthy();
  });

  test('deletes after a confirmation; the document leaves its group and the viewer; an emptied group disappears', async () => {
    mddbClient.deleteDocument.mockResolvedValue({});
    render(<DuplicatesPanel />);
    await scan();
    act(() => useStore.setState({ currentDocument: { key: 'd1', collection: 'kb' } }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete d1' }));
    const dialog = screen.getByRole('alertdialog', { name: 'Delete document' });
    expect(dialog.textContent).toContain('Delete d1 (en) from kb?');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(mddbClient.deleteDocument).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Delete d1' }));
    await act(async () => fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete' })));
    expect(mddbClient.deleteDocument).toHaveBeenCalledWith({ collection: 'kb', key: 'd1', lang: 'en' });
    expect(screen.queryByRole('button', { name: 'Delete d1' })).toBeNull();
    expect(useStore.getState().currentDocument).toBeNull();

    act(() => useStore.setState({ currentDocument: { key: 'other', collection: 'kb' } }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete d2' }));
    await act(async () => fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete' })));
    expect(screen.queryByRole('region', { name: 'Identical content, group 1' })).toBeNull();
    expect(useStore.getState().currentDocument).toEqual({ key: 'other', collection: 'kb' });
    expect(screen.getByRole('status').textContent).toBe('Scanned 5 documents (4 with embeddings): 1 groups.');
  });
});
