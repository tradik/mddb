// @vitest-environment jsdom
// ChatMcpSecretsCard (MCP-005): write-only secrets — listed by name, set or
// replaced, deleted after a confirm; values never shown.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { putChatMcpSecret: vi.fn(), deleteChatMcpSecret: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: ChatMcpSecretsCard } = await import('./ChatMcpSecretsCard');

const secrets = [
  { name: 'wiki', header: '', set: true, usedBy: ['support'] },
  { name: 'api', header: 'X-Api-Key', set: true, usedBy: [] },
];

beforeEach(() => {
  vi.mocked(mddbClient.putChatMcpSecret).mockResolvedValue({ set: true });
  vi.mocked(mddbClient.deleteChatMcpSecret).mockResolvedValue({ deleted: true });
});
afterEach(cleanup);

describe('ChatMcpSecretsCard', () => {
  test('lists names and headers, never values', () => {
    render(<ChatMcpSecretsCard secrets={secrets} onChanged={vi.fn()} />);
    expect(screen.getByText('Authorization: Bearer · set')).toBeTruthy();
    expect(screen.getByText('X-Api-Key · set')).toBeTruthy();
    expect(screen.getByText('used by support')).toBeTruthy();
  });

  test('sets a secret, clears the value field and reloads', async () => {
    const onChanged = vi.fn().mockResolvedValue();
    render(<ChatMcpSecretsCard secrets={[]} onChanged={onChanged} />);
    expect(screen.getByText('No secrets yet.')).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Secret name'), { target: { value: ' wiki ' } });
    fireEvent.change(screen.getByLabelText('Header (optional)'), { target: { value: ' X-Key ' } });
    fireEvent.change(screen.getByLabelText('Secret value'), { target: { value: 's3cr3t' } });
    expect(screen.getByLabelText('Secret value').type).toBe('password');
    fireEvent.click(screen.getByRole('button', { name: 'Set or replace' }));
    expect((await screen.findByText('Saved the secret wiki.')).tagName).toBe('OUTPUT');
    expect(mddbClient.putChatMcpSecret).toHaveBeenCalledWith('wiki', { value: 's3cr3t', header: 'X-Key' });
    expect(screen.getByLabelText('Secret value').value).toBe('');
    expect(onChanged).toHaveBeenCalled();
  });

  test('deletes after a confirm, not without one; a refusal shows the message', async () => {
    const confirmSpy = vi.fn(() => false);
    vi.stubGlobal('confirm', confirmSpy);
    render(<ChatMcpSecretsCard secrets={secrets} onChanged={vi.fn().mockResolvedValue()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete secret api' }));
    expect(mddbClient.deleteChatMcpSecret).not.toHaveBeenCalled();
    confirmSpy.mockReturnValue(true);
    fireEvent.click(screen.getByRole('button', { name: 'Delete secret api' }));
    expect(await screen.findByText('Deleted api.')).toBeTruthy();
    vi.mocked(mddbClient.deleteChatMcpSecret).mockRejectedValueOnce(new Error('API Error (409): in use by: support'));
    fireEvent.click(screen.getByRole('button', { name: 'Delete secret wiki' }));
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (409): in use by: support');
  });

  test('without an encryption key the form is disabled and names the setting', () => {
    render(<ChatMcpSecretsCard secrets={secrets} encryptionReady={false} onChanged={vi.fn()} />);
    const note = screen.getByText(/this server has no encryption key/);
    expect(note.textContent).toContain('Set MDDB_ENCRYPTION_KEY and restart mddbd');
    expect(screen.getByRole('button', { name: 'Set or replace' }).closest('fieldset').disabled).toBe(true);
    expect(screen.getByRole('button', { name: 'Set or replace' }).closest('form').getAttribute('aria-describedby')).toBe(note.id);
    expect(screen.getByText('used by support')).toBeTruthy();
  });

  test("a refused put shows the server's message as it is", async () => {
    const msg = 'mcp secrets are never stored in plain text and no at-rest encryption key is configured: set MDDB_ENCRYPTION_KEY and restart mddbd';
    vi.mocked(mddbClient.putChatMcpSecret).mockRejectedValueOnce(Object.assign(new Error(`API Error (409): ${msg}`),
      { status: 409, body: { error: msg, requires: 'MDDB_ENCRYPTION_KEY' } }));
    render(<ChatMcpSecretsCard secrets={[]} onChanged={vi.fn()} />);
    expect(screen.queryByText(/no encryption key/)).toBeNull();
    expect(screen.getByRole('button', { name: 'Set or replace' }).closest('fieldset').disabled).toBe(false);
    fireEvent.change(screen.getByLabelText('Secret name'), { target: { value: 'wiki' } });
    fireEvent.change(screen.getByLabelText('Secret value'), { target: { value: 's3cr3t' } });
    fireEvent.click(screen.getByRole('button', { name: 'Set or replace' }));
    expect((await screen.findByRole('alert')).textContent).toBe(msg);
  });

  test('without admin it says so', () => {
    render(<ChatMcpSecretsCard secrets={null} onChanged={vi.fn()} />);
    expect(screen.getByText(/Secrets need admin/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Set or replace' })).toBeNull();
  });
});
