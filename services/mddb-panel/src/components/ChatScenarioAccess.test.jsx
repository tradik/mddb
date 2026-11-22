// @vitest-environment jsdom
// ChatScenarioAccess (#441): links to the screens that decide access, and who
// can read the scenario.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getChatScenarioAccess: vi.fn(), listFeatures: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { resetFeatures } = await import('../lib/features');
const { default: ChatScenarioAccess } = await import('./ChatScenarioAccess');

const initialStore = useStore.getState();
const access = {
  scenario: 'phil', authEnabled: true, collections: ['docs', 'private'],
  users: [
    { username: 'bob', admin: false, full: false, grants: [{ collection: 'docs', via: 'group:readers' }] },
    { username: 'root', admin: true, full: true, grants: [{ collection: 'docs', via: 'admin' }, { collection: 'private', via: 'admin' }] },
    { username: 'erin', admin: false, disabled: true, full: false, grants: [] },
  ],
};

async function show(props = {}) {
  const view = render(<ChatScenarioAccess name="phil" version={1} {...props} />);
  await act(async () => {});
  return view;
}

beforeEach(() => {
  resetFeatures();
  useStore.setState({ ...initialStore, config: { authEnabled: true } }, true);
  vi.mocked(mddbClient.listFeatures).mockResolvedValue({ features: [], version: 0 });
  vi.mocked(mddbClient.getChatScenarioAccess).mockReset().mockResolvedValue(access);
});
afterEach(cleanup);

describe('ChatScenarioAccess', () => {
  test('lists every user, full first, with the rule written out', async () => {
    await show();
    const rows = screen.getAllByRole('row').slice(1).map((r) => within(r).getAllByRole('cell').map((c) => c.textContent));
    expect(rows).toEqual([
      ['root', 'All collections', 'admin'],
      ['bob', '1 of 2 collections', 'group readers'],
      ['erin (disabled)', 'None', ''],
    ]);
    expect(screen.getByText('Who can read this scenario').tagName).toBe('CAPTION');
    expect(screen.getByText(/auth_username/)).toBeTruthy();
    expect(screen.queryByText(/Saved state/)).toBeNull();
  });

  test('the links open their view, MCP API keys on its tab', async () => {
    await show();
    fireEvent.click(screen.getByRole('button', { name: 'MCP API keys' }));
    expect(useStore.getState()).toMatchObject({ viewMode: 'mcp', viewTarget: 'api-keys' });
    fireEvent.click(screen.getByRole('button', { name: 'Users and permissions' }));
    expect(useStore.getState()).toMatchObject({ viewMode: 'users', viewTarget: null });
  });

  test('auth off: no auth links, and the server says so', async () => {
    useStore.setState({ config: { authEnabled: false } });
    vi.mocked(mddbClient.getChatScenarioAccess).mockResolvedValue({ ...access, authEnabled: false, users: [] });
    await show();
    expect(screen.queryByRole('button', { name: 'Users and permissions' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Connect an MCP client' })).toBeTruthy();
    expect(screen.getByText(/Authentication is off/)).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();
  });

  test('a 403 keeps the links and says the list needs admin; another error is shown', async () => {
    vi.mocked(mddbClient.getChatScenarioAccess).mockRejectedValue(Object.assign(new Error('forbidden'), { status: 403 }));
    await show();
    expect(screen.getByText('The list of users needs admin on "*".')).toBeTruthy();
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByRole('button', { name: 'Groups' })).toBeTruthy();
    cleanup();
    vi.mocked(mddbClient.getChatScenarioAccess).mockRejectedValue(new Error('boom'));
    await show();
    expect(screen.getByRole('alert').textContent).toBe('boom');
  });

  test('no collections, a stale form, and a reload on a new version', async () => {
    vi.mocked(mddbClient.getChatScenarioAccess).mockResolvedValue({ ...access, collections: [], users: [] });
    const view = await show({ stale: true });
    expect(screen.getByText(/allows no collections/)).toBeTruthy();
    expect(screen.getByText('Saved state. Save the scenario to refresh.')).toBeTruthy();
    view.rerender(<ChatScenarioAccess name="phil" version={2} />);
    await act(async () => {});
    expect(mddbClient.getChatScenarioAccess).toHaveBeenCalledTimes(2);
  });

  test('a reply after unmount is dropped, and nothing renders before it', async () => {
    let resolve;
    vi.mocked(mddbClient.getChatScenarioAccess).mockReturnValue(new Promise((r) => { resolve = r; }));
    const view = render(<ChatScenarioAccess name="phil" version={1} />);
    expect(screen.queryByRole('table')).toBeNull();
    view.unmount();
    await act(async () => resolve(access));
    let reject;
    vi.mocked(mddbClient.getChatScenarioAccess).mockReturnValue(new Promise((_, r) => { reject = r; }));
    const again = render(<ChatScenarioAccess name="phil" version={1} />);
    again.unmount();
    await act(async () => reject(new Error('late')));
  });
});
