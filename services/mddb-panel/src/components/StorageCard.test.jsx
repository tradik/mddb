// @vitest-environment jsdom
// StorageCard (GET /v1/storage): measuring on demand for all collections or
// one, cancelling a slow measurement, errors, the per-collection category bars
// with their text alternative, and the bucket table of a full breakdown.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getStorage: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: StorageCard } = await import('./StorageCard');

const initialStore = useStore.getState();

const full = {
  fileBytes: 2048, pageSize: 4096, freePages: 1200, freeBytes: 1024,
  buckets: { ftsp: { inUseBytes: 900, keys: 1500 }, docs: { inUseBytes: 100 }, odd: null },
  collections: {
    src: { totalBytes: 1000, fileBytes: 4096, byCategory: { content: 250, fullText: 750 } },
    tiny: { totalBytes: 10 },
  },
  elapsedMs: 41210,
};

beforeEach(() => {
  useStore.setState(initialStore, true);
  useStore.setState({ stats: { collections: [{ name: 'src' }, { name: 'tiny' }] } });
  vi.mocked(mddbClient.getStorage).mockReset();
});
afterEach(cleanup);

describe('StorageCard', () => {
  test('measures every collection and shows totals, bars and buckets', async () => {
    vi.mocked(mddbClient.getStorage).mockResolvedValue(full);
    render(<StorageCard />);
    expect(screen.getByText(/can take minutes/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Measure' }));
    expect(await screen.findByText('Reclaimable by compaction')).toBeTruthy();
    expect(mddbClient.getStorage).toHaveBeenCalledWith({ collection: undefined, signal: expect.any(AbortSignal) });
    expect(screen.getByText('2.0 KB')).toBeTruthy();
    expect(screen.getByText('41.2 s')).toBeTruthy();
    expect(screen.getByText('1,200')).toBeTruthy();
    expect(screen.getByText(/own file 4.0 KB/)).toBeTruthy();
    expect(screen.getByRole('img', { name: 'src: Content 250 B (25%), Full text 750 B (75%)' })).toBeTruthy();
    expect(screen.getByRole('img', { name: 'tiny: No bytes recorded' })).toBeTruthy();
    const rows = screen.getAllByRole('row');
    expect(rows.map((r) => r.textContent)).toEqual(['BucketIn useKeys', 'ftsp900 B1,500', 'docs100 B0', 'odd0 B0']);
  });

  test('measures one collection; no buckets and missing figures fall back', async () => {
    vi.mocked(mddbClient.getStorage).mockResolvedValue({ collections: { src: null } });
    render(<StorageCard />);
    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'src' } });
    fireEvent.click(screen.getByRole('button', { name: 'Measure' }));
    expect(await screen.findByRole('img', { name: 'src: No bytes recorded' })).toBeTruthy();
    expect(mddbClient.getStorage).toHaveBeenCalledWith({ collection: 'src', signal: expect.any(AbortSignal) });
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.getByText('0 ms')).toBeTruthy();
  });

  test('says so when no collection is reported, with no stats in the store', async () => {
    useStore.setState({ stats: null });
    vi.mocked(mddbClient.getStorage).mockResolvedValue({});
    render(<StorageCard />);
    expect(screen.getAllByRole('option')).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Measure' }));
    expect(await screen.findByText('No collections reported.')).toBeTruthy();
  });

  test('shows a refusal', async () => {
    vi.mocked(mddbClient.getStorage).mockRejectedValue(new Error('API Error (403): forbidden'));
    render(<StorageCard />);
    fireEvent.click(screen.getByRole('button', { name: 'Measure' }));
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (403): forbidden');
  });

  test('Cancel aborts a running measurement', async () => {
    vi.mocked(mddbClient.getStorage).mockImplementation(({ signal }) => new Promise((_, reject) => {
      signal.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), { name: 'AbortError' })));
    }));
    render(<StorageCard />);
    fireEvent.click(screen.getByRole('button', { name: 'Measure' }));
    expect(screen.getByRole('button', { name: 'Measuring…' }).disabled).toBe(true);
    expect(screen.getByLabelText('Collection').disabled).toBe(true);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Cancel' })));
    expect((await screen.findByRole('alert')).textContent).toBe('Measurement cancelled.');
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull();
  });

  test('unmounting aborts a measurement still running', async () => {
    let seen;
    vi.mocked(mddbClient.getStorage).mockImplementation(({ signal }) => {
      seen = signal;
      return new Promise(() => {});
    });
    const { unmount } = render(<StorageCard />);
    fireEvent.click(screen.getByRole('button', { name: 'Measure' }));
    unmount();
    expect(seen.aborted).toBe(true);
  });

  test('a second measurement keeps its own controller', async () => {
    let resolveFirst;
    vi.mocked(mddbClient.getStorage)
      .mockImplementationOnce(() => new Promise((r) => (resolveFirst = r)));
    render(<StorageCard />);
    fireEvent.click(screen.getByRole('button', { name: 'Measure' }));
    await act(async () => resolveFirst(full));
    expect(screen.getByRole('button', { name: 'Measure' }).disabled).toBe(false);
  });
});
