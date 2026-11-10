// @vitest-environment jsdom
// Sidebar, acting side: deleting a collection (confirm, spinner, alert and
// page reload, failure), the upload and create-collection modals and what a
// finished upload refreshes, and the per-collection settings modal.
// Sidebar.nav.test.jsx covers stats, embeddings, the list and navigation.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { vectorStats: vi.fn(), listCollectionConfigs: vi.fn(), deleteCollection: vi.fn(), getStats: vi.fn() },
}));
vi.mock('./UploadModal', () => ({
  default: ({ collection, onClose, onSuccess }) => (
    <div>upload modal {collection ?? 'new'}
      <button onClick={onClose}>close upload</button>
      <button onClick={() => onSuccess('fresh')}>uploaded to fresh</button>
      <button onClick={() => onSuccess()}>uploaded nowhere</button>
    </div>
  ),
}));
vi.mock('./CreateCollectionModal', () => ({
  default: ({ onClose, onCreate }) => <div>create modal<button onClick={onClose}>close create</button><button onClick={() => onCreate('made')}>created</button></div>,
}));
vi.mock('./CollectionConfigModal', () => ({
  default: ({ collection, onClose, onSave }) => <div>config modal {collection}<button onClick={onClose}>close config</button><button onClick={onSave}>save config</button></div>,
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: Sidebar } = await import('./Sidebar');

const initialStore = useStore.getState();
const stats = { totalDocuments: 3, collections: [{ name: 'a', documentCount: 2 }, { name: 'b', documentCount: 1 }] };

/** The list row of `collection`. */
const row = (collection) => screen.getByText(collection).closest('.group');
const deleteButton = (collection) => row(collection).querySelector('button[title="Delete collection"]');

/** Renders the sidebar and waits for its first loads. */
async function renderWith(props = {}) {
  const view = render(<Sidebar stats={stats} {...props} />);
  await act(async () => {});
  return view;
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.vectorStats).mockReset().mockResolvedValue(null);
  vi.mocked(mddbClient.listCollectionConfigs).mockReset().mockResolvedValue({ configs: [] });
  vi.mocked(mddbClient.deleteCollection).mockReset();
  vi.mocked(mddbClient.getStats).mockReset();
  vi.spyOn(window, 'alert').mockImplementation(() => {});
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(cleanup);

describe('Sidebar delete', () => {
  test('asks for confirmation; declining deletes nothing', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    await renderWith();
    fireEvent.click(deleteButton('a'));
    expect(confirm.mock.calls[0][0]).toContain('PERMANENTLY delete ALL documents in "a"');
    expect(mddbClient.deleteCollection).not.toHaveBeenCalled();
  });

  test('deletes, spins meanwhile, reports the count and reloads the page', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const reload = vi.fn();
    vi.stubGlobal('location', { ...window.location, reload });
    let finish;
    vi.mocked(mddbClient.deleteCollection).mockReturnValue(new Promise((resolve) => (finish = resolve)));
    await renderWith();
    fireEvent.click(deleteButton('a'));
    expect(mddbClient.deleteCollection).toHaveBeenCalledWith({ collection: 'a' });
    expect(deleteButton('a').disabled).toBe(true);
    expect(deleteButton('a').querySelector('.animate-spin')).not.toBeNull();
    expect(deleteButton('b').disabled).toBe(false);

    await act(async () => finish({ deletedCount: 2 }));
    expect(window.alert).toHaveBeenCalledWith(expect.stringContaining('Deleted 2 documents'));
    expect(reload).toHaveBeenCalledTimes(1);
    expect(deleteButton('a').disabled).toBe(false);
  });

  test('a refused delete alerts with the reason', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    vi.mocked(mddbClient.deleteCollection).mockRejectedValue(new Error('API Error (403): forbidden'));
    await renderWith();
    await act(async () => fireEvent.click(deleteButton('b')));
    expect(window.alert).toHaveBeenCalledWith('Failed to delete collection: API Error (403): forbidden');
    expect(console.error).toHaveBeenCalledWith('Delete collection error:', expect.any(Error));
  });
});

describe('Sidebar modals', () => {
  test('with a collection selected, Upload opens the modal; a finished upload refreshes the stats and tells the parent', async () => {
    useStore.setState({ currentCollection: 'a' });
    vi.mocked(mddbClient.getStats).mockResolvedValue({ totalDocuments: 9, collections: stats.collections });
    const onStatsRefresh = vi.fn();
    await renderWith({ onStatsRefresh });
    expect(screen.queryByTitle('Create new collection')).toBeNull();
    fireEvent.click(screen.getByTitle('Upload documents to collection'));
    expect(screen.getByText('upload modal a')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'close upload' }));
    expect(screen.queryByText('upload modal a')).toBeNull();

    fireEvent.click(screen.getByTitle('Upload documents to collection'));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'uploaded to fresh' })));
    expect(screen.queryByText('upload modal a')).toBeNull();
    expect(useStore.getState().stats.totalDocuments).toBe(9);
    expect(useStore.getState().currentCollection).toBe('a');
    expect(onStatsRefresh).toHaveBeenCalledTimes(1);
  });

  test('without a collection, Create opens the create modal; creating one selects it and opens the upload', async () => {
    vi.mocked(mddbClient.getStats).mockResolvedValue({ totalDocuments: 1 });
    await renderWith();
    expect(screen.queryByTitle('Upload documents to collection')).toBeNull();
    fireEvent.click(screen.getByTitle('Create new collection'));
    expect(screen.getByText('create modal')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'close create' }));
    expect(screen.queryByText('create modal')).toBeNull();

    fireEvent.click(screen.getByTitle('Create new collection'));
    fireEvent.click(screen.getByRole('button', { name: 'created' }));
    expect(useStore.getState().currentCollection).toBe('made');
    expect(screen.getByText('upload modal made')).toBeTruthy();

    // The selection was dropped meanwhile: the upload's collection is adopted, with no parent to tell.
    act(() => useStore.setState({ currentCollection: null }));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'uploaded to fresh' })));
    expect(useStore.getState().currentCollection).toBe('fresh');
    expect(useStore.getState().stats).toEqual({ totalDocuments: 1 });
  });

  test('an upload that names no collection selects none; a failed stats reload is logged', async () => {
    vi.mocked(mddbClient.getStats).mockResolvedValueOnce({ totalDocuments: 2 }).mockRejectedValueOnce(new Error('API Error (500): boom'));
    await renderWith();
    fireEvent.click(screen.getByTitle('Create new collection'));
    fireEvent.click(screen.getByRole('button', { name: 'created' }));
    act(() => useStore.setState({ currentCollection: null }));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'uploaded nowhere' })));
    expect(useStore.getState().currentCollection).toBeNull();
    expect(useStore.getState().stats).toEqual({ totalDocuments: 2 });

    fireEvent.click(screen.getByTitle('Create new collection'));
    fireEvent.click(screen.getByRole('button', { name: 'created' }));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'uploaded to fresh' })));
    expect(console.error).toHaveBeenCalledWith('Failed to refresh stats:', expect.any(Error));
  });

  test('the settings button opens the config modal; saving reloads the configs, closing hides it', async () => {
    await renderWith();
    expect(mddbClient.listCollectionConfigs).toHaveBeenCalledTimes(1);
    fireEvent.click(row('b').querySelector('button[title="Collection settings"]'));
    expect(screen.getByText('config modal b')).toBeTruthy();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'save config' })));
    expect(mddbClient.listCollectionConfigs).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByRole('button', { name: 'close config' }));
    expect(screen.queryByText('config modal b')).toBeNull();
  });
});
