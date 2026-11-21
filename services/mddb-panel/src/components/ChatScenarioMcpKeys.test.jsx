// @vitest-environment jsdom
// ChatScenarioMcpKeys (#439): the MCP keys bound to one scenario — listed,
// created bound to it with the secret shown once, disabled and deleted.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { listMCPAPIKeys: vi.fn(), createMCPAPIKey: vi.fn(), deleteMCPAPIKey: vi.fn(), disableMCPAPIKey: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: ChatScenarioMcpKeys } = await import('./ChatScenarioMcpKeys');

const bound = { name: 'widget', keyPrefix: 'mcp_', fingerprint: 'aa11', scenario: 'support' };
const other = { name: 'elsewhere', keyPrefix: 'mcp_', fingerprint: 'bb22', scenario: 'sales' };

beforeEach(() => {
  for (const fn of Object.values(mddbClient)) fn.mockReset();
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  mddbClient.listMCPAPIKeys.mockResolvedValue({ keys: [bound, other, { name: 'plain', fingerprint: 'cc33' }, { name: 'legacy', keyPrefix: 'mcp_', scenario: 'support' }] });
  Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn().mockResolvedValue() }, configurable: true });
});
afterEach(cleanup);

async function renderFor(scenario) {
  render(<ChatScenarioMcpKeys scenario={scenario} />);
  await act(async () => {});
}

describe('ChatScenarioMcpKeys', () => {
  test('lists only the keys bound to the scenario, creates, disables and deletes', async () => {
    mddbClient.createMCPAPIKey.mockResolvedValueOnce({ key: 'mcp_bound_secret' });
    await renderFor('support');
    expect(screen.getByText('widget')).toBeTruthy();
    expect(screen.getByText('legacy')).toBeTruthy(); // a server without fingerprints
    expect(screen.queryByText('elsewhere')).toBeNull();
    expect(screen.queryByText('plain')).toBeNull();
    expect(screen.getAllByText('scenario support · read-only')).toHaveLength(2);

    const create = screen.getByRole('button', { name: /Create key/ });
    expect(create.disabled).toBe(true);
    await act(async () => fireEvent.submit(create.closest('form')));
    expect(mddbClient.createMCPAPIKey).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText('New key name'), { target: { value: ' site ' } });
    await act(async () => fireEvent.click(create));
    expect(mddbClient.createMCPAPIKey).toHaveBeenCalledWith('site', 0, { scenario: 'support' });
    expect(screen.getByText('mcp_bound_secret')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByText('mcp_bound_secret')).toBeNull();

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Disable key widget' })));
    expect(mddbClient.disableMCPAPIKey).toHaveBeenCalledWith({ fingerprint: 'aa11' });
    window.confirm.mockReturnValueOnce(false);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Delete key widget' })));
    expect(mddbClient.deleteMCPAPIKey).not.toHaveBeenCalled();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Delete key widget' })));
    expect(mddbClient.deleteMCPAPIKey).toHaveBeenCalledWith({ fingerprint: 'aa11' });
  });

  test('a scenario without keys says so; a refused list and a failed create are shown', async () => {
    mddbClient.listMCPAPIKeys.mockReset();
    mddbClient.listMCPAPIKeys.mockRejectedValueOnce(new Error('')).mockResolvedValue({});
    mddbClient.createMCPAPIKey.mockResolvedValueOnce({ key: 'mcp_first' });
    mddbClient.createMCPAPIKey.mockRejectedValueOnce(new Error('chat scenario not found'));
    await renderFor('drafts');
    expect(screen.getByRole('alert').textContent).toBe('MCP keys could not be loaded');
    expect(screen.getByText('No key is bound to this scenario.')).toBeTruthy();
    fireEvent.change(screen.getByLabelText('New key name'), { target: { value: 'x' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /Create key/ })));
    expect(screen.queryByRole('alert')).toBeNull(); // the reload after a create cleared it
    expect(screen.getByText('mcp_first')).toBeTruthy();
    fireEvent.change(screen.getByLabelText('New key name'), { target: { value: 'y' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /Create key/ })));
    expect(screen.getByRole('alert').textContent).toBe('chat scenario not found');
  });
});
