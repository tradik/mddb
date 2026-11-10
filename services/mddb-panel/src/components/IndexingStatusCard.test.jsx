// @vitest-environment jsdom
// IndexingStatusCard (GET /v1/indexing-status): server-wide or per collection,
// the progress bar and its text, images and training notes, refresh by hand,
// automatic refresh while work is queued (stopped once indexed or unmounted),
// and errors.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getIndexingStatus: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: IndexingStatusCard, REFRESH_MS } = await import('./IndexingStatusCard');

const initialStore = useStore.getState();

const busy = {
  collection: 'notes', pendingEmbeddings: 3, failedEmbeddings: 1, completedEmbeddings: 120, pendingMetadata: 2,
  progress: 0.975, oldestPendingAt: '2026-10-29T09:12:03Z', provider: 'text-embedding-3-small', indexed: false,
  images: { documents: 4, embedded: 2, model: 'clip', note: 'no multimodal provider' },
  trainingCollections: ['docs', 'repo'],
};

beforeEach(() => {
  useStore.setState(initialStore, true);
  useStore.setState({ stats: { collections: [{ name: 'notes' }] } });
  vi.mocked(mddbClient.getIndexingStatus).mockReset();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('IndexingStatusCard', () => {
  test('shows queued work, images and training collections', async () => {
    vi.mocked(mddbClient.getIndexingStatus).mockResolvedValue(busy);
    render(<IndexingStatusCard />);
    expect(await screen.findByText(/Embeddings are still queued/)).toBeTruthy();
    expect(mddbClient.getIndexingStatus).toHaveBeenCalledWith(undefined);
    expect(screen.getByText(/Provider: text-embedding-3-small/)).toBeTruthy();
    expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('98');
    expect(screen.getByText('98% embedded')).toBeTruthy();
    expect(screen.getByText('Pending embeddings').nextElementSibling.textContent).toBe('3');
    expect(screen.getByText('Failed embeddings').nextElementSibling.textContent).toBe('1');
    expect(screen.getByText('Oldest pending').nextElementSibling.textContent).not.toBe('—');
    expect(screen.getByText(/Images: 2 of 4 embedded with clip/)).toBeTruthy();
    expect(screen.getByText('no multimodal provider')).toBeTruthy();
    expect(screen.getByText(/still training: docs, repo/)).toBeTruthy();
  });

  test('an indexed collection with missing figures falls back', async () => {
    vi.mocked(mddbClient.getIndexingStatus).mockResolvedValue({ indexed: true, images: {} });
    render(<IndexingStatusCard />);
    expect(await screen.findByText('Everything written is searchable.')).toBeTruthy();
    expect(screen.getByText('0% embedded')).toBeTruthy();
    expect(screen.getByText('Pending metadata').nextElementSibling.textContent).toBe('0');
    expect(screen.getByText('Completed embeddings').nextElementSibling.textContent).toBe('0');
    expect(screen.getByText('Oldest pending').nextElementSibling.textContent).toBe('—');
    expect(screen.getByText(/Images: 0 of 0 embedded/)).toBeTruthy();
    expect(screen.queryByText(/still training/)).toBeNull();
  });

  test('choosing a collection and Refresh ask again', async () => {
    vi.mocked(mddbClient.getIndexingStatus).mockResolvedValue({ indexed: true });
    render(<IndexingStatusCard />);
    await screen.findByText('Everything written is searchable.');
    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'notes' } });
    await act(async () => {});
    expect(mddbClient.getIndexingStatus).toHaveBeenLastCalledWith('notes');
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await act(async () => {});
    expect(mddbClient.getIndexingStatus).toHaveBeenCalledTimes(3);
  });

  test('refreshes while queued and stops once indexed', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.mocked(mddbClient.getIndexingStatus)
      .mockResolvedValueOnce(busy)
      .mockResolvedValueOnce({ ...busy, indexed: true, progress: 1 });
    render(<IndexingStatusCard />);
    await screen.findByText(/still queued/);
    await act(() => vi.advanceTimersByTimeAsync(REFRESH_MS));
    expect(await screen.findByText('Everything written is searchable.')).toBeTruthy();
    await act(() => vi.advanceTimersByTimeAsync(REFRESH_MS * 3));
    expect(mddbClient.getIndexingStatus).toHaveBeenCalledTimes(2);
  });

  test('unmounting stops the refresh', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.mocked(mddbClient.getIndexingStatus).mockResolvedValue(busy);
    const { unmount } = render(<IndexingStatusCard />);
    await screen.findByText(/still queued/);
    unmount();
    await act(() => vi.advanceTimersByTimeAsync(REFRESH_MS * 2));
    expect(mddbClient.getIndexingStatus).toHaveBeenCalledTimes(1);
  });

  test('shows an error, then clears it on success; no stats means no collections', async () => {
    useStore.setState({ stats: null });
    vi.mocked(mddbClient.getIndexingStatus)
      .mockRejectedValueOnce(new Error('API Error (403): forbidden'))
      .mockResolvedValueOnce({ indexed: true });
    render(<IndexingStatusCard />);
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (403): forbidden');
    expect(screen.getAllByRole('option')).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await screen.findByText('Everything written is searchable.');
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
