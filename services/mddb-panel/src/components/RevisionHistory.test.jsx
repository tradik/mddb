// @vitest-environment jsdom
// RevisionHistory: loading, a failed or empty list, the revisions with their
// first metadata, picking one for RevisionPreview, a restore that updates the
// open document and closes, and a refused restore.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getRevisions: vi.fn(), restoreRevision: vi.fn() } }));
vi.mock('./RevisionPreview', () => ({
  default: ({ revision, current, restoring, onRestore }) => (
    <div>
      preview {revision.timestamp} of {current.key} {restoring ? 'busy' : 'idle'}
      <button onClick={() => onRestore(revision.timestamp)}>restore</button>
    </div>
  ),
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: RevisionHistory } = await import('./RevisionHistory');

const initialStore = useStore.getState();
const doc = { collection: 'docs', key: 'guide', lang: 'en', contentMd: 'now' };
const revisions = [
  { timestamp: 300, contentMd: 'c', meta: { a: ['1', '2'], b: 'plain', c: ['3'], d: ['hidden'] } },
  { timestamp: 200, contentMd: 'b', meta: {} },
  { timestamp: 100, contentMd: 'a' },
];

beforeEach(() => {
  useStore.setState(initialStore, true);
  mddbClient.getRevisions.mockReset();
  mddbClient.restoreRevision.mockReset();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

async function renderHistory(onClose = vi.fn()) {
  await act(async () => {
    render(<RevisionHistory document={doc} onClose={onClose} />);
  });
  return onClose;
}

describe('RevisionHistory', () => {
  test('shows the loading state, then the revisions newest first', async () => {
    let resolve;
    mddbClient.getRevisions.mockReturnValue(new Promise((r) => { resolve = r; }));
    render(<RevisionHistory document={doc} onClose={() => {}} />);
    expect(screen.getByText('Loading revisions...')).toBeTruthy();
    await act(async () => resolve({ revisions }));
    expect(mddbClient.getRevisions).toHaveBeenCalledWith({ collection: 'docs', key: 'guide', lang: 'en' });
    expect(screen.getByText('docs / guide (en)')).toBeTruthy();
    expect(screen.getByText('Latest')).toBeTruthy();
    expect(screen.getByText('Rev #2')).toBeTruthy();
    expect(screen.getByText('Rev #1')).toBeTruthy();
    expect(screen.getByText('a: 1')).toBeTruthy();
    expect(screen.getByText('b: plain')).toBeTruthy();
    expect(screen.queryByText('d: hidden')).toBeNull();
    expect(screen.getByText('Select a revision to preview')).toBeTruthy();
  });

  test('a failed load shows the error; an empty answer says so', async () => {
    mddbClient.getRevisions.mockRejectedValue(new Error('boom'));
    await renderHistory();
    expect(screen.getByText('boom')).toBeTruthy();
    cleanup();
    mddbClient.getRevisions.mockResolvedValue({});
    await renderHistory();
    expect(screen.getByText('No revisions found')).toBeTruthy();
  });

  test('picking a revision opens its preview against the open document', async () => {
    mddbClient.getRevisions.mockResolvedValue({ revisions });
    await renderHistory();
    fireEvent.click(screen.getByText('Rev #2').closest('button'));
    expect(screen.getByText('preview 200 of guide idle')).toBeTruthy();
    expect(screen.getByText('Rev #2').closest('button').className).toContain('bg-info-soft');
  });

  test('a restore updates the open document, reloads and closes', async () => {
    mddbClient.getRevisions.mockResolvedValue({ revisions });
    mddbClient.restoreRevision.mockResolvedValue({ key: 'guide', lang: 'en', contentMd: 'b' });
    const onClose = await renderHistory();
    vi.useFakeTimers();
    fireEvent.click(screen.getByText('Rev #2').closest('button'));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'restore' })));
    expect(mddbClient.restoreRevision).toHaveBeenCalledWith({ collection: 'docs', key: 'guide', lang: 'en', timestamp: 200 });
    expect(useStore.getState().currentDocument).toEqual({ key: 'guide', lang: 'en', contentMd: 'b', collection: 'docs' });
    expect(screen.getByText('Revision restored!')).toBeTruthy();
    expect(mddbClient.getRevisions).toHaveBeenCalledTimes(2);
    act(() => vi.advanceTimersByTime(1500));
    expect(onClose).toHaveBeenCalled();
    expect(screen.queryByText('Revision restored!')).toBeNull();
  });

  test('a refused restore shows the error and stays open', async () => {
    mddbClient.getRevisions.mockResolvedValue({ revisions });
    mddbClient.restoreRevision.mockRejectedValue(new Error('API Error (403): forbidden'));
    const onClose = await renderHistory();
    fireEvent.click(screen.getByText('Latest').closest('button'));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'restore' })));
    expect(screen.getByText('API Error (403): forbidden')).toBeTruthy();
    expect(screen.getByText('preview 300 of guide idle')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Close revision history' }));
    expect(onClose).toHaveBeenCalled();
  });
});
