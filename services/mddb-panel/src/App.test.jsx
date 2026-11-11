// @vitest-environment jsdom
// App: the page switch — auth detection from the first stats call, the login
// form, the sidebar's width, collapse and resize handle, every view mode, the
// search-mode split of the documents view, and SSE-driven list refreshes.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('./lib/mddb-client', () => ({ default: { getStats: vi.fn(), getConfig: vi.fn() } }));
vi.mock('./lib/auth', () => ({ authManager: { isAuthenticated: vi.fn(() => false) } }));
vi.mock('./lib/useSSE', () => ({ useSSE: vi.fn(() => ({ connected: true, lastEvent: null })) }));
// Every page has its own suite; here each one is a label.
vi.mock('./components/Header', () => ({ default: ({ onRefresh }) => <button onClick={onRefresh}>refresh</button> }));
vi.mock('./components/Sidebar', () => ({
  default: ({ statsError, onCollapse }) => <div><button onClick={onCollapse}>collapse</button>{statsError && <p>sidebar error: {statsError}</p>}</div>,
}));
vi.mock('./components/LoginForm', () => ({ default: ({ onSuccess }) => <button onClick={onSuccess}>log in</button> }));
vi.mock('./components/DocumentList', () => ({ default: ({ sseRefreshKey }) => <div>document list {sseRefreshKey}</div> }));
vi.mock('./components/DocumentViewer', () => ({ default: () => <div>document viewer</div> }));
vi.mock('./components/SSEToast', () => ({ default: ({ connected }) => <div>toast {connected ? 'on' : 'off'}</div> }));
vi.mock('./components/ComplianceBanner', () => ({ default: () => <div>banner</div> }));
vi.mock('./components/VectorSearchPanel', () => ({ default: () => 'vector search panel' }));
vi.mock('./components/HybridSearchPanel', () => ({ default: () => 'hybrid search panel' }));
vi.mock('./components/FTSSearchPanel', () => ({ default: () => 'fts search panel' }));
vi.mock('./components/SystemInfo', () => ({ default: () => 'system info' }));
vi.mock('./components/ConfigPanel', () => ({ default: () => 'config panel' }));
vi.mock('./components/MCPConfigPanel', () => ({ default: () => 'mcp config panel' }));
vi.mock('./components/EndpointsPanel', () => ({ default: () => 'endpoints panel' }));
vi.mock('./components/UsersPanel', () => ({ default: () => 'users panel' }));
vi.mock('./components/GroupsPanel', () => ({ default: () => 'groups panel' }));
vi.mock('./components/VectorPanel', () => ({ default: () => 'vector panel' }));
vi.mock('./components/VectorSpacePanel', () => ({ default: () => 'vector space panel' }));
vi.mock('./components/SearchAdvisorPanel', () => ({ default: () => 'search advisor panel' }));
vi.mock('./components/EmbeddingModelsPanel', () => ({ default: () => 'embedding models panel' }));
vi.mock('./components/SettingsPanel', () => ({ default: () => 'settings panel' }));
vi.mock('./components/ClusterPanel', () => ({ default: () => 'cluster panel' }));
vi.mock('./components/SynonymsPanel', () => ({ default: () => 'synonyms panel' }));
vi.mock('./components/CurationPanel', () => ({ default: () => 'curation panel' }));
vi.mock('./components/StopWordsPanel', () => ({ default: () => 'stop words panel' }));
vi.mock('./components/AutomationPanel', () => ({ default: () => 'automation panel' }));
vi.mock('./components/CrossSearchPanel', () => ({ default: () => 'cross search panel' }));
vi.mock('./components/TemporalPanel', () => ({ default: () => 'temporal panel' }));
vi.mock('./components/SpellCheckPanel', () => ({ default: () => 'spell check panel' }));
vi.mock('./components/GeoPanel', () => ({ default: () => 'geo panel' }));
vi.mock('./components/AuditLogPanel', () => ({ default: () => 'audit log panel' }));
vi.mock('./components/WebhooksPanel', () => ({ default: () => 'webhooks panel' }));
vi.mock('./components/SecurityDashboard', () => ({ default: () => 'security dashboard' }));
vi.mock('./components/EncryptionPanel', () => ({ default: () => 'encryption panel' }));
vi.mock('./components/AliasesPanel', () => ({ default: () => 'aliases panel' }));
vi.mock('./components/ChatScenariosPanel', () => ({ default: () => 'chat scenarios panel' }));
vi.mock('./components/EvalPanel', () => ({ default: () => 'eval panel' }));
vi.mock('./components/CodeSymbolPanel', () => ({ default: () => 'code symbol panel' }));

const { default: mddbClient } = await import('./lib/mddb-client');
const { useSSE } = await import('./lib/useSSE');
const { useStore } = await import('./lib/store');
const { default: App } = await import('./App');

const initialStore = useStore.getState();
const stats = { totalDocuments: 3, collections: [] };

/** The option bag App handed to the latest useSSE call. */
const sseOptions = () => vi.mocked(useSSE).mock.lastCall[0];

/** The sidebar's width box. */
const sidebarBox = (container) => container.querySelector('.relative.flex-shrink-0');

/** Renders App with `state` in the store and waits for the first stats load. */
async function renderWith(state = {}) {
  useStore.setState(state);
  const view = render(<App />);
  await act(async () => {});
  return view;
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.getStats).mockReset().mockResolvedValue(stats);
  vi.mocked(mddbClient.getConfig).mockReset().mockResolvedValue({ version: '2.19.0' });
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(cleanup);

describe('App', () => {
  test('loads the stats and config, then shows the System page with the toast', async () => {
    await renderWith();
    expect(screen.getByText('system info')).toBeTruthy();
    expect(screen.getByText('banner')).toBeTruthy();
    expect(screen.getByText('toast on')).toBeTruthy();
    expect(useStore.getState().stats).toEqual(stats);
    expect(useStore.getState().config).toEqual({ version: '2.19.0' });
    expect(useStore.getState().statsLoading).toBe(false);
    expect(sseOptions().enabled).toBe(true);
  });

  test.each(['API Error (401): no token', 'Unauthorized'])('"%s" shows the login form; logging in opens System and loads the stats', async (message) => {
    vi.mocked(mddbClient.getStats).mockRejectedValueOnce(new Error(message));
    vi.mocked(mddbClient.getConfig).mockRejectedValue(new Error('config refused'));
    await renderWith({ viewMode: 'config' });
    expect(screen.getByRole('button', { name: 'log in' })).toBeTruthy();
    expect(screen.queryByText('config panel')).toBeNull();
    expect(sseOptions().enabled).toBe(false);

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'log in' })));
    expect(screen.getByText('system info')).toBeTruthy();
    expect(useStore.getState().stats).toEqual(stats);
    expect(mddbClient.getStats).toHaveBeenCalledTimes(2);
  });

  test('another failure assumes auth is off and shows the error; Refresh loads again', async () => {
    vi.mocked(mddbClient.getStats).mockRejectedValueOnce(new Error('ECONNREFUSED'));
    await renderWith();
    expect(screen.getByText('sidebar error: ECONNREFUSED')).toBeTruthy();
    expect(screen.getByText('system info')).toBeTruthy();

    vi.mocked(mddbClient.getStats).mockRejectedValueOnce(new Error('still down'));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'refresh' })));
    expect(screen.getByText('sidebar error: still down')).toBeTruthy();

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'refresh' })));
    expect(screen.queryByText(/sidebar error/)).toBeNull();
    expect(useStore.getState().stats).toEqual(stats);
  });

  test('the sidebar collapses to nothing and comes back at its width', async () => {
    const { container } = await renderWith({ sidebarWidth: 300 });
    expect(sidebarBox(container).style.width).toBe('300px');
    fireEvent.click(screen.getByRole('button', { name: 'collapse' }));
    expect(useStore.getState().sidebarCollapsed).toBe(true);
    expect(sidebarBox(container).style.width).toBe('0px');
    expect(screen.queryByRole('button', { name: 'collapse' })).toBeNull();
    expect(container.querySelector('.cursor-col-resize')).toBeNull();

    fireEvent.click(screen.getByTitle('Show sidebar'));
    expect(useStore.getState().sidebarCollapsed).toBe(false);
    expect(sidebarBox(container).style.width).toBe('300px');
  });

  test('dragging the handle resizes the sidebar between 180 and 480 px until the mouse is released', async () => {
    const { container } = await renderWith();
    fireEvent.mouseDown(container.querySelector('.cursor-col-resize'));
    expect(document.body.style.cursor).toBe('col-resize');
    expect(document.body.style.userSelect).toBe('none');

    fireEvent.mouseMove(document, { clientX: 320 });
    expect(useStore.getState().sidebarWidth).toBe(320);
    fireEvent.mouseMove(document, { clientX: 20 });
    expect(useStore.getState().sidebarWidth).toBe(180);
    fireEvent.mouseMove(document, { clientX: 900 });
    expect(useStore.getState().sidebarWidth).toBe(480);

    fireEvent.mouseUp(document);
    expect(document.body.style.cursor).toBe('');
    expect(document.body.style.userSelect).toBe('');
    fireEvent.mouseMove(document, { clientX: 300 });
    expect(useStore.getState().sidebarWidth).toBe(480);
  });

  test('an SSE event refreshes the document list', async () => {
    await renderWith({ viewMode: 'documents' });
    expect(screen.getByText('document list 0')).toBeTruthy();
    act(() => sseOptions().onEvent({ type: 'document.added' }));
    expect(screen.getByText('document list 1')).toBeTruthy();
  });

  test.each([
    ['metadata', 'document list 0'],
    ['vector', 'vector search panel'],
    ['hybrid', 'hybrid search panel'],
    ['fulltext', 'fts search panel'],
  ])('documents view in %s mode shows %s, and the viewer once a document is open', async (searchMode, label) => {
    await renderWith({ viewMode: 'documents', searchMode });
    expect(screen.getByText(label)).toBeTruthy();
    expect(screen.queryByText('document viewer')).toBeNull();
    act(() => useStore.setState({ currentDocument: { key: 'a' } }));
    expect(screen.getByText('document viewer')).toBeTruthy();
  });

  test.each([
    ['crossSearch', 'cross search panel'],
    ['geo', 'geo panel'],
  ])('%s shows the viewer beside it once a document is open', async (viewMode, label) => {
    await renderWith({ viewMode });
    expect(screen.getByText(label)).toBeTruthy();
    expect(screen.queryByText('document viewer')).toBeNull();
    act(() => useStore.setState({ currentDocument: { key: 'a' } }));
    expect(screen.getByText('document viewer')).toBeTruthy();
  });

  test.each([
    ['cluster', 'cluster panel'], ['system', 'system info'], ['config', 'config panel'],
    ['mcp', 'mcp config panel'], ['endpoints', 'endpoints panel'], ['users', 'users panel'],
    ['groups', 'groups panel'], ['vectors', 'vector panel'], ['vectorSpace', 'vector space panel'],
    ['searchAdvisor', 'search advisor panel'], ['embeddings', 'embedding models panel'],
    ['synonyms', 'synonyms panel'], ['stopwords', 'stop words panel'], ['curation', 'curation panel'],
    ['aliases', 'aliases panel'], ['chat', 'chat scenarios panel'], ['eval', 'eval panel'], ['codeSymbols', 'code symbol panel'],
    ['automation', 'automation panel'], ['settings', 'settings panel'], ['temporal', 'temporal panel'],
    ['spellcheck', 'spell check panel'], ['auditLog', 'audit log panel'], ['webhooks', 'webhooks panel'],
    ['security', 'security dashboard'], ['encryption', 'encryption panel'],
  ])('view mode %s shows only %s', async (viewMode, label) => {
    const { container } = await renderWith({ viewMode, currentDocument: { key: 'a' } });
    expect(container.querySelector('.flex-1.flex.overflow-hidden').textContent).toBe(label);
  });
});
