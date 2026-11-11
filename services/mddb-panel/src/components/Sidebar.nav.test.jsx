// @vitest-environment jsdom
// Sidebar, reading side: the stats summary, the embeddings block fed by a
// five-second poll, the collection list (icons per type, selection, error and
// empty states) and which administration pages the config makes visible.
// Sidebar.actions.test.jsx covers deleting, uploading and the modals.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { vectorStats: vi.fn(), listCollectionConfigs: vi.fn(), deleteCollection: vi.fn(), getStats: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: Sidebar } = await import('./Sidebar');

const initialStore = useStore.getState();
const stats = {
  totalDocuments: 1234, totalRevisions: 5, databaseSize: 1536,
  collections: [{ name: 'a', documentCount: 10 }, { name: 'b', documentCount: 2 }, { name: 'c', documentCount: 0 }],
};

/** The list row of `collection`. */
const row = (collection) => screen.getByText(collection).closest('.group');

/** Renders the sidebar and waits for its first loads. */
async function renderWith(props = { stats }) {
  const view = render(<Sidebar {...props} />);
  await act(async () => {});
  return view;
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.vectorStats).mockReset().mockResolvedValue(null);
  vi.mocked(mddbClient.listCollectionConfigs).mockReset().mockResolvedValue({ configs: [] });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('Sidebar stats and embeddings', () => {
  test('shows the totals, the size and the server version', async () => {
    useStore.setState({ config: { version: '2.19.0' } });
    await renderWith();
    expect(screen.getByText('1,234')).toBeTruthy();
    expect(screen.getByText('5')).toBeTruthy();
    expect(screen.getByText('1.5 KB')).toBeTruthy();
    expect(screen.getByText('Server v2.19.0 · Panel v2.11.4')).toBeTruthy();
    fireEvent.click(screen.getByTitle('Hide sidebar'));
  });

  test('without stats or config everything reads zero', async () => {
    const onCollapse = vi.fn();
    await renderWith({ stats: null, onCollapse });
    expect(screen.getAllByText('0')).toHaveLength(2);
    expect(screen.getByText('0 B')).toBeTruthy();
    expect(screen.getByText('Collections (0)')).toBeTruthy();
    expect(screen.getByText('No collections found')).toBeTruthy();
    expect(screen.getByText('Server v... · Panel v2.11.4')).toBeTruthy();
    fireEvent.click(screen.getByTitle('Hide sidebar'));
    expect(onCollapse).toHaveBeenCalledTimes(1);
  });

  test('the embeddings block appears with the stats: provider, model and dimensions, or Disabled', async () => {
    vi.mocked(mddbClient.vectorStats).mockResolvedValue({ enabled: true, provider: 'openai', model: 'text-embedding-3-small', dimensions: 1536 });
    await renderWith();
    expect(screen.getByText('openai')).toBeTruthy();
    expect(screen.getByText('text-embedding-3-small')).toBeTruthy();
    expect(screen.getByText('1536')).toBeTruthy();
    expect(screen.getByTitle('Connected')).toBeTruthy();

    act(() => useStore.setState({ vectorStats: { enabled: true } }));
    expect(screen.getByText('Unknown')).toBeTruthy();
    act(() => useStore.setState({ vectorStats: { enabled: false } }));
    expect(screen.getByText('Disabled')).toBeTruthy();
    expect(screen.getByTitle('Disconnected')).toBeTruthy();
  });

  test('polls the vector stats every five seconds, shrugs off a failure and stops on unmount', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.mocked(mddbClient.vectorStats).mockRejectedValue(new Error('API Error (503): no provider'));
    const { unmount } = await renderWith();
    expect(screen.queryByText('Embeddings')).toBeNull();
    expect(mddbClient.vectorStats).toHaveBeenCalledTimes(1);
    await act(() => vi.advanceTimersByTimeAsync(5000));
    expect(mddbClient.vectorStats).toHaveBeenCalledTimes(2);
    unmount();
    await act(() => vi.advanceTimersByTimeAsync(5000));
    expect(mddbClient.vectorStats).toHaveBeenCalledTimes(2);
  });
});

describe('Sidebar collections', () => {
  test('shows an icon per configured type or the emoji, highlights the current one and selects on click', async () => {
    useStore.setState({ currentCollection: 'a' });
    const configs = [
      { collection: 'a', config: { icon: '📚' } }, { collection: 'b', config: { type: 'website' } },
      { collection: 'c', config: { type: 'images' } }, { collection: 'd', config: { type: 'audio' } },
      { collection: 'e', config: { type: 'documents' } }, { collection: 'f', config: { type: 'odd' } },
    ];
    vi.mocked(mddbClient.listCollectionConfigs).mockResolvedValue({ configs });
    const collections = [...'abcdefg'].map((name) => ({ name, documentCount: 1 }));
    await renderWith({ stats: { collections } });
    expect(screen.getByText('Collections (7)')).toBeTruthy();
    expect(screen.getByText('📚')).toBeTruthy();
    for (const [name, icon] of [['b', 'globe'], ['c', 'image'], ['d', 'music'], ['e', 'file-text'], ['f', 'folder'], ['g', 'folder']]) {
      expect(row(name).querySelector(`svg[class*="lucide-${icon}"]`), `${name} shows ${icon}`).not.toBeNull();
    }
    expect(row('a').className).toContain('bg-blue-100');
    expect(row('b').className).not.toContain('bg-blue-100');

    fireEvent.click(screen.getByText('b'));
    expect(useStore.getState().currentCollection).toBe('b');
    expect(useStore.getState().viewMode).toBe('documents');
    expect(row('b').className).toContain('bg-blue-100');
  });

  test('an answer without configs, or a refused one, leaves the folder icon', async () => {
    vi.mocked(mddbClient.listCollectionConfigs).mockResolvedValueOnce({}).mockRejectedValueOnce(new Error('API Error (403): forbidden'));
    await renderWith();
    expect(row('a').querySelector('svg[class*="lucide-folder"]')).not.toBeNull();
    cleanup();
    await renderWith();
    expect(row('a').querySelector('svg[class*="lucide-folder"]')).not.toBeNull();
    expect(useStore.getState().collectionConfigs).toEqual({});
  });

  test('a stats error replaces the list with the connection error', async () => {
    await renderWith({ stats, statsError: 'fetch failed' });
    expect(screen.getByText('Connection Error')).toBeTruthy();
    expect(screen.getByText('fetch failed')).toBeTruthy();
    expect(screen.queryByText('a')).toBeNull();
  });
});

describe('Sidebar administration', () => {
  test('switches the view; Users, Groups and Cluster appear with auth and replication, Automation unless disabled', async () => {
    await renderWith();
    expect(screen.queryByRole('button', { name: 'Users' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Cluster' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Automation' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'System Info' }).className).toContain('bg-blue-100');

    fireEvent.click(screen.getByRole('button', { name: 'Configuration' }));
    expect(useStore.getState().viewMode).toBe('config');
    expect(screen.getByRole('button', { name: 'Configuration' }).className).toContain('bg-blue-100');
    expect(screen.getByRole('button', { name: 'System Info' }).className).not.toContain('bg-blue-100');

    act(() => useStore.setState({ config: { authEnabled: true, replicationRole: 'primary', automationsEnabled: false } }));
    expect(screen.getByRole('button', { name: 'Users' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Groups' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Cluster' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Automation' })).toBeNull();
  });

  test('the 2.19.0 pages are listed after the others and switch the view', async () => {
    await renderWith();
    fireEvent.click(screen.getByRole('button', { name: 'Agent Memory' }));
    expect(useStore.getState().viewMode).toBe('memory');
  });
});
