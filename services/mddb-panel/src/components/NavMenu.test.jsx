// @vitest-environment jsdom
// NavMenu, NavGroup and NavButton (2.19.0): the grouped sidebar menu —
// disclosure headers, folding remembered across mounts, the active page's
// group kept open, the "Search the menu" box and the module switches.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { listFeatures: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { resetFeatures } = await import('../lib/features');
const { default: NavMenu } = await import('./NavMenu');

const initialStore = useStore.getState();
const header = (label) => screen.getByRole('button', { name: `${label} group` });
const box = () => screen.getByRole('searchbox', { name: 'Search the menu' });

async function renderMenu(props = {}) {
  const view = render(<NavMenu {...props} />);
  await act(async () => {});
  return view;
}

beforeEach(() => {
  localStorage.clear();
  useStore.setState({ ...initialStore, viewMode: 'documents' }, true);
  resetFeatures();
  vi.mocked(mddbClient.listFeatures).mockReset().mockResolvedValue({ features: [], version: 0 });
});
afterEach(cleanup);

describe('groups', () => {
  test('six disclosure headers, all open, each controlling its list', async () => {
    await renderMenu({ authEnabled: true, clusterEnabled: true });
    for (const label of ['Search', 'Data', 'Chat', 'Integrations', 'Security', 'System']) {
      const h = header(label);
      expect(h.getAttribute('aria-expanded')).toBe('true');
      expect(document.getElementById(h.getAttribute('aria-controls')).hidden).toBe(false);
    }
    expect(within(document.getElementById('nav-group-security')).getByRole('button', { name: 'My Account' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Cluster' })).toBeTruthy();
  });

  test('a header folds its group, and the fold survives a new mount', async () => {
    const { unmount } = await renderMenu();
    fireEvent.click(header('Search'));
    expect(header('Search').getAttribute('aria-expanded')).toBe('false');
    expect(screen.queryByRole('button', { name: 'Stop Words' })).toBeNull();
    unmount();
    await renderMenu();
    expect(header('Search').getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(header('Search'));
    expect(screen.getByRole('button', { name: 'Stop Words' })).toBeTruthy();
  });

  test("the open page's group stays open and its header ignores clicks", async () => {
    localStorage.setItem('mddb.nav.collapsed', '["system"]');
    useStore.setState({ viewMode: 'config' });
    await renderMenu();
    const h = header('System');
    expect(h.getAttribute('aria-expanded')).toBe('true');
    expect(h.getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(h);
    expect(header('System').getAttribute('aria-expanded')).toBe('true');
    expect(screen.getByRole('button', { name: 'Configuration' }).getAttribute('aria-current')).toBe('page');
    expect(header('Data').getAttribute('aria-disabled')).toBeNull();
  });

  test('an entry switches the page and is highlighted', async () => {
    await renderMenu();
    const geo = screen.getByRole('button', { name: 'Geo Search' });
    expect(geo.className).toContain('text-gray-700');
    fireEvent.click(geo);
    expect(useStore.getState().viewMode).toBe('geo');
    expect(screen.getByRole('button', { name: 'Geo Search' }).className).toContain('bg-blue-100');
  });

  test('entries follow auth, replication, automations and the module switches; an empty group goes', async () => {
    vi.mocked(mddbClient.listFeatures).mockResolvedValue({
      features: ['webhooks', 'automation'].map((name) => ({ name, enabled: false })), version: 1,
    });
    await renderMenu({ automationsEnabled: false });
    for (const name of ['Users', 'Groups', 'My Account', 'Cluster', 'Automation', 'Webhooks']) {
      expect(screen.queryByRole('button', { name })).toBeNull();
    }
    expect(screen.getByRole('button', { name: 'API Endpoints' })).toBeTruthy();
    cleanup();
    vi.mocked(mddbClient.listFeatures).mockResolvedValue({ features: [], version: 2 });
    resetFeatures();
    await renderMenu();
    expect(screen.getByRole('button', { name: 'Automation' })).toBeTruthy();
  });
});

describe('search', () => {
  test('filters by label without regard to case or accents, opening folded groups', async () => {
    localStorage.setItem('mddb.nav.collapsed', '["search","security"]');
    await renderMenu();
    fireEvent.change(box(), { target: { value: 'STÓP' } });
    expect(screen.getByRole('button', { name: 'Stop Words' })).toBeTruthy();
    expect(header('Search').getAttribute('aria-expanded')).toBe('true');
    expect(screen.queryByRole('button', { name: 'Data group' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'System Info' })).toBeNull();
  });

  test('no match says so; Escape clears the box and restores the menu', async () => {
    await renderMenu();
    fireEvent.change(box(), { target: { value: 'zzz' } });
    expect(screen.getByRole('status').textContent).toBe('No menu entries match “zzz”.');
    fireEvent.keyDown(box(), { key: 'a' });
    expect(box().value).toBe('zzz');
    fireEvent.keyDown(box(), { key: 'Escape' });
    expect(box().value).toBe('');
    expect(screen.queryByRole('status')).toBeNull();
    expect(screen.getByRole('button', { name: 'System Info' })).toBeTruthy();
  });

  test('a blank query is no search: folded groups stay folded', async () => {
    localStorage.setItem('mddb.nav.collapsed', '["data"]');
    await renderMenu();
    fireEvent.change(box(), { target: { value: '   ' } });
    expect(header('Data').getAttribute('aria-expanded')).toBe('false');
  });
});
