// @vitest-environment jsdom
// SecretPicker (INT-023): a stored secret picked by name, or a pasted token
// stored under a new name and then picked — the value goes only to the
// secret store, is cleared once saved, and a refusal is said plainly.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { useState } from 'react';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { putChatMcpSecret: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: SecretPicker } = await import('./SecretPicker');
const client = vi.mocked(mddbClient);

afterEach(cleanup);

const secrets = [{ name: 'gh', set: true }, { name: 'stale', set: false }];

/** Renders a picker over real state; returns the store's reload spy and the change spy. */
function renderPicker({ value = '', encryptionReady = true } = {}) {
  const reload = vi.fn();
  const seen = vi.fn();
  function Harness() {
    const [picked, setPicked] = useState(value);
    const change = (v) => {
      seen(v);
      setPicked(v);
    };
    return <SecretPicker name="Token" value={picked} onChange={change} store={{ secrets, encryptionReady, reload }} hint="A hint." />;
  }
  render(<Harness />);
  return { reload, seen };
}

const openPaste = () => fireEvent.click(screen.getByRole('button', { name: 'Paste a new token for Token' }));
const type = (label, value) => fireEvent.change(screen.getByLabelText(label), { target: { value } });

describe('SecretPicker', () => {
  test('lists stored secrets by name with whether a value is set, and a missing name', () => {
    const { seen } = renderPicker({ value: 'gone' });
    expect(screen.getByRole('option', { name: 'gh (set)' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'stale (not sealed: set it again)' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'gone (not found)' })).toBeTruthy();
    expect(screen.getByText('A hint.')).toBeTruthy();
    type(/^Token/, 'gh');
    expect(seen).toHaveBeenLastCalledWith('gh');
    expect(screen.queryByRole('option', { name: 'gone (not found)' })).toBeNull();
  });

  test('a pasted token is stored under its name, cleared, and the name picked', async () => {
    client.putChatMcpSecret.mockResolvedValue({ name: 'new-token' });
    const { reload, seen } = renderPicker();
    openPaste();
    const store = screen.getByRole('button', { name: 'Store and use' });
    expect(store.disabled).toBe(true);
    type('New secret name', ' new-token ');
    type('Token value', 's3cret');
    expect(screen.getByLabelText('Token value').type).toBe('password');
    fireEvent.click(store);
    await waitFor(() => expect(seen).toHaveBeenLastCalledWith('new-token'));
    expect(client.putChatMcpSecret).toHaveBeenCalledWith('new-token', { value: 's3cret' });
    expect(reload).toHaveBeenCalled();
    expect(screen.queryByLabelText('Token value')).toBeNull();
    expect(screen.getByRole('button', { name: 'Paste a new token for Token' }).getAttribute('aria-expanded')).toBe('false');
  });

  test('a refused store is said plainly and keeps the form open', async () => {
    client.putChatMcpSecret.mockRejectedValue(Object.assign(new Error('API Error (409)'), { body: { error: 'requires MDDB_ENCRYPTION_KEY' } }));
    const { seen } = renderPicker();
    openPaste();
    type('New secret name', 'k');
    type('Token value', 'v');
    fireEvent.click(screen.getByRole('button', { name: 'Store and use' }));
    expect((await screen.findByRole('alert')).textContent).toBe('requires MDDB_ENCRYPTION_KEY');
    expect(seen).not.toHaveBeenCalled();
    expect(screen.getByLabelText('Token value').value).toBe('v');
  });

  test('without an encryption key the paste form says why it cannot store', () => {
    renderPicker({ encryptionReady: false });
    openPaste();
    type('New secret name', 'k');
    type('Token value', 'v');
    expect(screen.getByRole('button', { name: 'Store and use' }).disabled).toBe(true);
    expect(screen.getByText(/no encryption key/).textContent).toContain('MDDB_ENCRYPTION_KEY');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel pasting a token' }));
    expect(screen.queryByText(/no encryption key/)).toBeNull();
  });
});
