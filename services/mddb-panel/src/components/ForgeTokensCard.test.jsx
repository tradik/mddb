// @vitest-environment jsdom
// ForgeTokensCard (INT-023): the server's default forge tokens — host and
// stored secret per row, rows added and removed, Save replacing the whole
// map, and a refused load or save said plainly.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { getForgeTokens: vi.fn(), setForgeTokens: vi.fn(), listChatMcpSecrets: vi.fn(), putChatMcpSecret: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: ForgeTokensCard } = await import('./ForgeTokensCard');
const client = vi.mocked(mddbClient);

afterEach(cleanup);

const secrets = { secrets: [{ name: 'gh', set: true }, { name: 'lab', set: true }] };
const save = () => fireEvent.click(screen.getByRole('button', { name: 'Save default tokens' }));

describe('ForgeTokensCard', () => {
  test('lists the stored defaults, edits, adds and removes rows, and saves the whole map', async () => {
    client.listChatMcpSecrets.mockResolvedValue(secrets);
    client.getForgeTokens.mockResolvedValue({ defaults: { 'github.com': 'gh', 'old.example.com': 'gh' } });
    client.setForgeTokens.mockResolvedValue({});
    render(<ForgeTokensCard />);
    expect((await screen.findAllByLabelText(/^Host/))[0].value).toBe('github.com');
    await screen.findAllByRole('option', { name: 'lab (set)' });
    fireEvent.click(screen.getByRole('button', { name: 'Remove the token of old.example.com' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add host' }));
    expect(screen.getByLabelText(/^Token for this host/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Add host' }));
    const hosts = screen.getAllByLabelText(/^Host/);
    fireEvent.change(hosts[1], { target: { value: ' GitLab.com ' } });
    fireEvent.change(screen.getByLabelText(/^Token for GitLab\.com/), { target: { value: 'lab' } });
    save();
    expect((await screen.findByRole('status')).textContent).toBe('Saved 2 default tokens.');
    expect(client.setForgeTokens).toHaveBeenCalledWith({ 'github.com': 'gh', 'gitlab.com': 'lab' });
  });

  test('no defaults says so; saving one says it in the singular', async () => {
    client.listChatMcpSecrets.mockResolvedValue(secrets);
    client.getForgeTokens.mockResolvedValue({});
    client.setForgeTokens.mockResolvedValue({});
    render(<ForgeTokensCard />);
    expect(await screen.findByText('No default tokens.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Add host' }));
    fireEvent.change(screen.getByLabelText(/^Host/), { target: { value: 'github.com' } });
    save();
    expect((await screen.findByRole('status')).textContent).toBe('Saved 1 default token.');
    expect(client.setForgeTokens).toHaveBeenCalledWith({ 'github.com': '' });
  });

  test('a refused save is shown as the server worded it', async () => {
    client.listChatMcpSecrets.mockResolvedValue(secrets);
    client.getForgeTokens.mockResolvedValue({ defaults: { 'github.com': 'gone' } });
    client.setForgeTokens.mockRejectedValue(new Error('API Error (400): unknown secret "gone"'));
    render(<ForgeTokensCard />);
    await screen.findAllByRole('option', { name: 'gh (set)' });
    save();
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (400): unknown secret "gone"');
    expect(screen.queryByRole('status')).toBeNull();
  });

  test('a refused load disables Save; an unreadable secret list is said', async () => {
    client.listChatMcpSecrets.mockRejectedValue(new Error('feature disabled'));
    client.getForgeTokens.mockRejectedValue(Object.assign(new Error('API Error (403): admin required'), { status: 403, body: { error: 'admin required' } }));
    render(<ForgeTokensCard />);
    expect(await screen.findByText('Not permitted')).toBeTruthy();
    await screen.findByText(/Stored secrets could not be listed: feature disabled/);
    expect(screen.getByRole('button', { name: 'Save default tokens' }).disabled).toBe(true);
    expect(screen.queryByText('No default tokens.')).toBeNull();
  });

  test('answers after unmount are dropped', async () => {
    let answer;
    let fail;
    client.listChatMcpSecrets.mockReturnValue(new Promise(() => {}));
    client.getForgeTokens.mockReturnValueOnce(new Promise((resolve) => { answer = resolve; }));
    render(<ForgeTokensCard />).unmount();
    answer({ defaults: { 'github.com': 'gh' } });
    client.getForgeTokens.mockReturnValueOnce(new Promise((_, reject) => { fail = reject; }));
    render(<ForgeTokensCard />).unmount();
    fail(new Error('late'));
    await waitFor(() => expect(client.getForgeTokens).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
