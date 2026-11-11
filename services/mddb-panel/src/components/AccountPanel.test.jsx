// @vitest-environment jsdom
// My Account page and header badge: the signed-in identity (/v1/auth/me) and
// the user's own API keys — create with an optional expiry, the secret shown
// once, the list, revoke behind a confirmation — plus the MCP keys panel that
// shares the one-time secret.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    getMe: vi.fn(), listApiKeys: vi.fn(), createApiKey: vi.fn(), deleteApiKey: vi.fn(),
    listMCPAPIKeys: vi.fn(), createMCPAPIKey: vi.fn(), deleteMCPAPIKey: vi.fn(), disableMCPAPIKey: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: AccountPanel } = await import('./AccountPanel');
const { default: IdentityBadge } = await import('./IdentityBadge');
const { default: MCPAPIKeysPanel } = await import('./MCPAPIKeysPanel');

const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: { error: text } });
const key = { keyHash: '292c986b954dd625957e2f83986a9805', description: 'ci', createdAt: 100, expiresAt: 0 };

async function renderPage() {
  render(<AccountPanel />);
  await act(async () => {});
}

beforeEach(() => {
  for (const fn of Object.values(mddbClient)) fn.mockReset();
  mddbClient.getMe.mockResolvedValue({ username: 'admin', admin: true, createdAt: 100 });
  mddbClient.listApiKeys.mockResolvedValue({ keys: [key, { keyHash: 'abcdef1234567890', createdAt: 100, expiresAt: 200 }] });
  Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn().mockResolvedValue() }, configurable: true });
});
afterEach(cleanup);

describe('IdentityBadge', () => {
  test('names the account and marks an admin; nothing until it is known', async () => {
    const { container } = render(<IdentityBadge />);
    expect(container.textContent).toBe('');
    await act(async () => {});
    expect(container.textContent).toBe('Signed in asadminadmin');
    cleanup();
    mddbClient.getMe.mockResolvedValueOnce({ username: 'jo', admin: false });
    const view = render(<IdentityBadge />);
    await act(async () => {});
    expect(view.container.textContent).toBe('Signed in asjo');
    cleanup();
    mddbClient.getMe.mockRejectedValueOnce(apiError(401, 'no token'));
    const failed = render(<IdentityBadge />);
    await act(async () => {});
    expect(failed.container.textContent).toBe('');
  });
});

describe('AccountPanel', () => {
  test('shows who is signed in and lists the keys', async () => {
    await renderPage();
    expect(screen.getByText('Global admin (admin on "*")')).toBeTruthy();
    const table = screen.getByRole('table', { name: 'My API keys' });
    expect(within(table).getAllByText('ci')).toHaveLength(2);
    expect(within(table).getByText('292c986b954d…')).toBeTruthy();
    expect(within(table).getByText('Never')).toBeTruthy();
    expect(within(table).getByText('—')).toBeTruthy();
  });

  test('a non-admin, no keys, and failures as the server words them', async () => {
    mddbClient.getMe.mockResolvedValueOnce({ username: 'jo', admin: false, createdAt: 0 });
    mddbClient.listApiKeys.mockResolvedValueOnce({});
    await renderPage();
    expect(screen.getByText('Per-collection permissions')).toBeTruthy();
    expect(screen.getByText('You have no API keys.')).toBeTruthy();
    cleanup();
    mddbClient.getMe.mockRejectedValueOnce(apiError(401, 'missing token'));
    mddbClient.listApiKeys.mockRejectedValueOnce(apiError(500, 'failed to retrieve API keys'));
    await renderPage();
    expect(screen.getByText('API Error (401): missing token')).toBeTruthy();
    expect(screen.getByText('API Error (500): failed to retrieve API keys')).toBeTruthy();
  });

  test('creates a key with an expiry, shows the secret once and reloads', async () => {
    mddbClient.createApiKey.mockResolvedValueOnce({ key: 'mddb_live_secret', description: 'deploy' });
    await renderPage();
    const create = screen.getByRole('button', { name: 'Create key' });
    expect(create.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Description'), { target: { value: ' deploy ' } });
    fireEvent.change(screen.getByLabelText('Expires on (optional)'), { target: { value: '2027-01-01' } });
    await act(async () => fireEvent.click(create));
    expect(mddbClient.createApiKey).toHaveBeenCalledWith({ description: 'deploy', expiresAt: Math.floor(new Date('2027-01-01T00:00:00').getTime() / 1000) });
    expect(screen.getByText('mddb_live_secret')).toBeTruthy();
    expect(mddbClient.listApiKeys).toHaveBeenCalledTimes(2);
    expect(screen.getByLabelText('Description').value).toBe('');
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByText('mddb_live_secret')).toBeNull();

    mddbClient.createApiKey.mockRejectedValueOnce(apiError(400, 'invalid request'));
    fireEvent.change(screen.getByLabelText('Description'), { target: { value: 'x' } });
    await act(async () => fireEvent.click(create));
    expect(mddbClient.createApiKey).toHaveBeenLastCalledWith({ description: 'x', expiresAt: 0 });
    expect(screen.getByText('API Error (400): invalid request')).toBeTruthy();
  });

  test('revokes a key after a confirmation naming it', async () => {
    mddbClient.deleteApiKey.mockResolvedValueOnce({ status: 'deleted' });
    await renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Revoke abcdef123456' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getByRole('button', { name: 'Revoke ci' }));
    const dialog = screen.getByRole('alertdialog', { name: 'Revoke API key' });
    expect(within(dialog).getByText('ci')).toBeTruthy();
    await act(async () => fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke' })));
    expect(mddbClient.deleteApiKey).toHaveBeenCalledWith(key.keyHash);
    expect(screen.queryByRole('alertdialog')).toBeNull();
    expect(mddbClient.listApiKeys).toHaveBeenCalledTimes(2);

    fireEvent.click(screen.getByRole('button', { name: 'Revoke abcdef123456' }));
    expect(within(screen.getByRole('alertdialog')).getByText('abcdef123456')).toBeTruthy();
  });
});

describe('MCPAPIKeysPanel', () => {
  const mcpKey = { name: 'claude-prod', keyPrefix: 'mk_abc...', expiresAt: 0 };

  beforeEach(() => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    mddbClient.listMCPAPIKeys.mockResolvedValue({ keys: [mcpKey, { name: 'old', keyPrefix: 'mk_old...', disabled: true, expiresAt: 1800000000 }] });
  });

  test('lists keys, creates one with the one-time secret, disables and deletes', async () => {
    mddbClient.createMCPAPIKey.mockResolvedValueOnce({ key: 'mk_full_secret' });
    render(<MCPAPIKeysPanel />);
    await act(async () => {});
    expect(screen.getByText('claude-prod')).toBeTruthy();
    expect(screen.getByText('DISABLED')).toBeTruthy();
    expect(screen.getByText(/^expires /)).toBeTruthy();

    const name = screen.getByPlaceholderText('Key name (e.g. claude-prod)');
    await act(async () => fireEvent.keyDown(name, { key: 'Enter' }));
    expect(mddbClient.createMCPAPIKey).not.toHaveBeenCalled();
    fireEvent.change(name, { target: { value: 'bot' } });
    await act(async () => fireEvent.keyDown(name, { key: 'a' }));
    await act(async () => fireEvent.keyDown(name, { key: 'Enter' }));
    expect(mddbClient.createMCPAPIKey).toHaveBeenCalledWith('bot');
    expect(screen.getByText('mk_full_secret')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));

    await act(async () => fireEvent.click(screen.getByTitle('Disable key')));
    expect(mddbClient.disableMCPAPIKey).toHaveBeenCalledWith('mk_abc');
    await act(async () => fireEvent.click(screen.getAllByTitle('Delete key')[0]));
    expect(mddbClient.deleteMCPAPIKey).toHaveBeenCalledWith('mk_abc');
    window.confirm.mockReturnValueOnce(false);
    await act(async () => fireEvent.click(screen.getAllByTitle('Delete key')[1]));
    expect(mddbClient.deleteMCPAPIKey).toHaveBeenCalledTimes(1);
  });

  test('failures are shown and can be dismissed; an empty list says so', async () => {
    mddbClient.listMCPAPIKeys.mockRejectedValueOnce(new Error('')).mockResolvedValue({});
    mddbClient.createMCPAPIKey.mockRejectedValueOnce(new Error('MCP API keys are disabled'));
    mddbClient.disableMCPAPIKey.mockRejectedValueOnce(new Error('cannot disable'));
    mddbClient.deleteMCPAPIKey.mockRejectedValueOnce(new Error('cannot delete'));
    render(<MCPAPIKeysPanel />);
    expect(screen.getByText('Loading keys...')).toBeTruthy();
    await act(async () => {});
    expect(screen.getByText('Failed to load API keys')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '×' }));
    expect(screen.getByText('No API keys configured. Create one above.')).toBeTruthy();

    fireEvent.change(screen.getByPlaceholderText('Key name (e.g. claude-prod)'), { target: { value: 'bot' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /Create Key/ })));
    expect(screen.getByText('MCP API keys are disabled')).toBeTruthy();
  });

  test('an answer without keys lists none', async () => {
    mddbClient.listMCPAPIKeys.mockResolvedValueOnce({});
    render(<MCPAPIKeysPanel />);
    await act(async () => {});
    expect(screen.getByText('No API keys configured. Create one above.')).toBeTruthy();
  });

  test('disable and delete failures are shown', async () => {
    mddbClient.disableMCPAPIKey.mockRejectedValueOnce(new Error('cannot disable'));
    mddbClient.deleteMCPAPIKey.mockRejectedValueOnce(new Error('cannot delete'));
    render(<MCPAPIKeysPanel />);
    await act(async () => {});
    await act(async () => fireEvent.click(screen.getByTitle('Disable key')));
    expect(screen.getByText('cannot disable')).toBeTruthy();
    await act(async () => fireEvent.click(screen.getAllByTitle('Delete key')[0]));
    expect(screen.getByText('cannot delete')).toBeTruthy();
  });
});
