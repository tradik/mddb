// @vitest-environment jsdom
// Sidebar: server stats, the embeddings box, the collection list (icons,
// open, settings, delete), the upload and create modals, and the
// administration menu — including the Chat entry (#403).
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    listCollectionConfigs: vi.fn(),
    vectorStats: vi.fn(),
    deleteCollection: vi.fn(),
    getStats: vi.fn(),
  },
}));
vi.mock('./UploadModal', () => ({
  default: ({ collection, onClose, onSuccess }) => (
    <div data-testid="upload-modal" data-collection={collection || ''}>
      <button onClick={onClose}>close upload</button>
      <button onClick={() => onSuccess('fresh')}>finish upload</button>
      <button onClick={() => onSuccess('')}>finish unnamed upload</button>
    </div>
  ),
}));
vi.mock('./CreateCollectionModal', () => ({
  default: ({ onClose, onCreate }) => (
    <div data-testid="create-modal">
      <button onClick={onClose}>close create</button>
      <button onClick={() => onCreate('fresh')}>create fresh</button>
    </div>
  ),
}));
vi.mock('./CollectionConfigModal', () => ({
  default: ({ collection, onClose, onSave }) => (
    <div data-testid="config-modal" data-collection={collection}>
      <button onClick={onClose}>close config</button>
      <button onClick={onSave}>save config</button>
    </div>
  ),
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: Sidebar } = await import('./Sidebar');

const initialStore = useStore.getState();

const stats = {
  totalDocuments: 1234,
  totalRevisions: 56,
  databaseSize: 1536,
  collections: ['site', 'photos', 'talks', 'papers', 'notes', 'starred'].map((name, i) => ({ name, documentCount: i + 1 })),
};
const configs = [
  { collection: 'site', config: { type: 'website' } },
  { collection: 'photos', config: { type: 'images' } },
  { collection: 'talks', config: { type: 'audio' } },
  { collection: 'papers', config: { type: 'documents' } },
  { collection: 'starred', config: { icon: '⭐' } },
];

/** Renders the sidebar and lets its first loads settle. */
async function renderSidebar(props = {}) {
  const view = render(<Sidebar stats={stats} onCollapse={() => {}} {...props} />);
  await act(async () => {});
  return view;
}

const row = (name) => screen.getByRole('button', { name }).closest('.group');

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.listCollectionConfigs).mockResolvedValue({ configs });
  vi.mocked(mddbClient.vectorStats).mockResolvedValue({ enabled: true, provider: 'openai', model: 'text-embedding-3-small', dimensions: 1536 });
  vi.mocked(mddbClient.getStats).mockResolvedValue({ totalDocuments: 1235, collections: [] });
  vi.stubGlobal('alert', vi.fn());
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('stats and embeddings', () => {
  test('shows the totals, the database size and the embedding model', async () => {
    await renderSidebar();
    expect(screen.getByText('1,234')).toBeTruthy();
    expect(screen.getByText('56')).toBeTruthy();
    expect(screen.getByText('1.5 KB')).toBeTruthy();
    expect(screen.getByText('openai')).toBeTruthy();
    expect(screen.getByText('1536')).toBeTruthy();
    expect(screen.getByTitle('Connected')).toBeTruthy();
  });

  test('without stats everything reads zero; disabled embeddings say so', async () => {
    vi.mocked(mddbClient.vectorStats).mockResolvedValue({ enabled: false });
    vi.mocked(mddbClient.listCollectionConfigs).mockResolvedValue({});
    await renderSidebar({ stats: null });
    expect(screen.getByText('0 B')).toBeTruthy();
    expect(screen.getByText('Collections (0)')).toBeTruthy();
    expect(screen.getByText('No collections found')).toBeTruthy();
    expect(screen.getByText('Disabled')).toBeTruthy();
    expect(screen.getByTitle('Disconnected')).toBeTruthy();
  });

  test('a provider the server does not name reads Unknown; failed loads are not fatal', async () => {
    vi.mocked(mddbClient.vectorStats).mockResolvedValueOnce({ enabled: true, model: 'm', dimensions: 3 }).mockRejectedValue(new Error('down'));
    vi.mocked(mddbClient.listCollectionConfigs).mockRejectedValue(new Error('down'));
    vi.useFakeTimers();
    const view = render(<Sidebar stats={stats} onCollapse={() => {}} />);
    await act(async () => {});
    expect(screen.getByText('Unknown')).toBeTruthy();
    await act(async () => { vi.advanceTimersByTime(5000); });
    expect(mddbClient.vectorStats).toHaveBeenCalledTimes(2);
    view.unmount();
    vi.advanceTimersByTime(20000);
    expect(mddbClient.vectorStats).toHaveBeenCalledTimes(2);
  });

  test('a connection error replaces the list', async () => {
    await renderSidebar({ statsError: 'HTTP 502' });
    expect(screen.getByText('Connection Error')).toBeTruthy();
    expect(screen.getByText('HTTP 502')).toBeTruthy();
  });
});

describe('collections', () => {
  test('each collection shows its icon by type, or its own', async () => {
    await renderSidebar();
    expect(screen.getByText('⭐')).toBeTruthy();
    expect(row('site').querySelector('svg.lucide-globe')).toBeTruthy();
    expect(row('photos').querySelector('svg.lucide-image')).toBeTruthy();
    expect(row('talks').querySelector('svg.lucide-music')).toBeTruthy();
    expect(row('papers').querySelector('svg.lucide-file-text')).toBeTruthy();
    expect(row('notes').querySelector('svg.lucide-folder')).toBeTruthy();
  });

  test('opening one selects it, shows its documents and offers Upload', async () => {
    await renderSidebar();
    fireEvent.click(screen.getByRole('button', { name: 'site' }));
    expect(useStore.getState().currentCollection).toBe('site');
    expect(useStore.getState().viewMode).toBe('documents');
    expect(row('site').className).toContain('bg-blue-100');
    fireEvent.click(screen.getByTitle('Upload documents to collection'));
    expect(screen.getByTestId('upload-modal').dataset.collection).toBe('site');
    fireEvent.click(screen.getByText('close upload'));
    expect(screen.queryByTestId('upload-modal')).toBeNull();
  });

  test('the settings button opens the config modal, and a save reloads the configs', async () => {
    await renderSidebar();
    fireEvent.click(within(row('site')).getByTitle('Collection settings'));
    expect(screen.getByTestId('config-modal').dataset.collection).toBe('site');
    fireEvent.click(screen.getByText('save config'));
    expect(mddbClient.listCollectionConfigs).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByText('close config'));
    expect(screen.queryByTestId('config-modal')).toBeNull();
  });

  test('delete asks first, then reports how many documents went', async () => {
    const confirmSpy = vi.fn(() => false);
    vi.stubGlobal('confirm', confirmSpy);
    let finish;
    vi.mocked(mddbClient.deleteCollection).mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    await renderSidebar();
    fireEvent.click(within(row('site')).getByTitle('Delete collection'));
    expect(confirmSpy.mock.calls[0][0]).toContain('PERMANENTLY delete ALL documents in "site"');
    expect(mddbClient.deleteCollection).not.toHaveBeenCalled();

    confirmSpy.mockReturnValue(true);
    fireEvent.click(within(row('site')).getByTitle('Delete collection'));
    expect(within(row('site')).getByTitle('Delete collection').querySelector('.animate-spin')).toBeTruthy();
    await act(async () => finish({ deletedCount: 7 }));
    expect(vi.mocked(alert).mock.calls[0][0]).toContain('Deleted 7 documents.');
    expect(useStore.getState().currentCollection).toBeNull(); // the click did not open the collection
  });

  test('a failed delete says why', async () => {
    vi.stubGlobal('confirm', () => true);
    vi.mocked(mddbClient.deleteCollection).mockRejectedValue(new Error('HTTP 403: forbidden'));
    await renderSidebar();
    await act(async () => fireEvent.click(within(row('site')).getByTitle('Delete collection')));
    expect(alert).toHaveBeenCalledWith('Failed to delete collection: HTTP 403: forbidden');
    expect(within(row('site')).getByTitle('Delete collection').querySelector('.animate-spin')).toBeNull();
  });
});

describe('creating and uploading', () => {
  test('a created collection is selected and goes straight to upload; the upload refreshes the stats', async () => {
    const onStatsRefresh = vi.fn();
    await renderSidebar({ onStatsRefresh });
    fireEvent.click(screen.getByTitle('Create new collection'));
    fireEvent.click(screen.getByText('close create'));
    expect(screen.queryByTestId('create-modal')).toBeNull();
    fireEvent.click(screen.getByTitle('Create new collection'));
    fireEvent.click(screen.getByText('create fresh'));
    expect(useStore.getState().currentCollection).toBe('fresh');
    await act(async () => fireEvent.click(screen.getByText('finish upload')));
    expect(useStore.getState().stats.totalDocuments).toBe(1235);
    expect(onStatsRefresh).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId('upload-modal')).toBeNull();
  });

  test('an upload with no collection open selects the one it wrote to', async () => {
    await renderSidebar();
    fireEvent.click(screen.getByTitle('Create new collection'));
    fireEvent.click(screen.getByText('create fresh'));
    act(() => useStore.getState().setCurrentCollection(null));
    await act(async () => fireEvent.click(screen.getByText('finish upload')));
    expect(useStore.getState().currentCollection).toBe('fresh');
  });

  test('an upload that names no collection selects none; a failed refresh is logged', async () => {
    await renderSidebar();
    fireEvent.click(screen.getByTitle('Create new collection'));
    fireEvent.click(screen.getByText('create fresh'));
    act(() => useStore.getState().setCurrentCollection(null));
    await act(async () => fireEvent.click(screen.getByText('finish unnamed upload')));
    expect(useStore.getState().currentCollection).toBeNull();

    fireEvent.click(screen.getByTitle('Create new collection'));
    fireEvent.click(screen.getByText('create fresh'));
    vi.mocked(mddbClient.getStats).mockRejectedValue(new Error('down'));
    await act(async () => fireEvent.click(screen.getByText('finish upload')));
    expect(console.error).toHaveBeenCalledWith('Failed to refresh stats:', expect.any(Error));
  });
});

describe('administration menu', () => {
  test('Chat opens the chat scenarios page and is highlighted', async () => {
    await renderSidebar();
    const chat = screen.getByRole('button', { name: 'Chat' });
    expect(chat.className).toContain('text-gray-700');
    fireEvent.click(chat);
    expect(useStore.getState().viewMode).toBe('chat');
    expect(screen.getByRole('button', { name: 'Chat' }).className).toContain('bg-blue-100');
  });

  test('Users, Groups and Cluster appear only when the server has them; Automation unless disabled', async () => {
    await renderSidebar();
    for (const name of ['Users', 'Groups', 'Cluster']) expect(screen.queryByRole('button', { name })).toBeNull();
    expect(screen.getByRole('button', { name: 'Automation' })).toBeTruthy();
    expect(screen.getByText(/Server v\.\.\./)).toBeTruthy();

    act(() => useStore.setState({ config: { authEnabled: true, replicationRole: 'leader', automationsEnabled: false, version: '2.19.0' } }));
    for (const name of ['Users', 'Groups', 'Cluster']) expect(screen.getByRole('button', { name })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Automation' })).toBeNull();
    expect(screen.getByText(/Server v2\.19\.0/)).toBeTruthy();
  });

  test('the collapse button calls back', async () => {
    const onCollapse = vi.fn();
    await renderSidebar({ onCollapse });
    fireEvent.click(screen.getByTitle('Hide sidebar'));
    expect(onCollapse).toHaveBeenCalledTimes(1);
  });
});
