// @vitest-environment jsdom
// CollectionRepository (INT-023): a collection's repository in its settings —
// URL, forge only for a self-hosted host, token by secret name — and the
// "Create issues sync" button, which needs a saved repository and says what
// it created or why it could not.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { useState } from 'react';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { listChatMcpSecrets: vi.fn(), putChatMcpSecret: vi.fn(), createAutomation: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: CollectionRepository } = await import('./CollectionRepository');
const client = vi.mocked(mddbClient);

afterEach(cleanup);

/** Renders the section over real state; returns the change spy. */
function renderRepository({ value = { url: '', forge: '', tokenSecretRef: '' }, savedUrl } = {}) {
  const seen = vi.fn();
  function Harness() {
    const [repo, setRepo] = useState(value);
    const change = (next) => {
      seen(next);
      setRepo(next);
    };
    return <CollectionRepository collection="docs" value={repo} onChange={change} savedUrl={savedUrl} />;
  }
  render(<Harness />);
  return seen;
}

const change = (label, value) => fireEvent.change(screen.getByLabelText(label), { target: { value } });
const createButton = () => screen.getByRole('button', { name: 'Create issues sync' });

describe('CollectionRepository', () => {
  test('edits the URL, the forge of a self-hosted host and the token by name', async () => {
    client.listChatMcpSecrets.mockResolvedValue({ secrets: [{ name: 'gh', set: true }] });
    const seen = renderRepository();
    expect(screen.queryByLabelText(/^Forge/)).toBeNull();
    change(/^Repository URL/, 'https://git.example.com/acme/docs');
    change(/^Forge/, 'forgejo');
    await screen.findByRole('option', { name: 'gh (set)' });
    change(/^Repository token/, 'gh');
    expect(seen).toHaveBeenLastCalledWith({ url: 'https://git.example.com/acme/docs', forge: 'forgejo', tokenSecretRef: 'gh' });
  });

  test('a pasted token is stored, the list read again and its name picked', async () => {
    client.listChatMcpSecrets.mockResolvedValueOnce({}).mockResolvedValue({ secrets: [{ name: 'new', set: true }] });
    client.putChatMcpSecret.mockResolvedValue({});
    const seen = renderRepository();
    fireEvent.click(screen.getByRole('button', { name: 'Paste a new token for Repository token' }));
    change('New secret name', 'new');
    change('Token value', 'tok');
    fireEvent.click(screen.getByRole('button', { name: 'Store and use' }));
    await screen.findByRole('option', { name: 'new (set)' });
    expect(client.listChatMcpSecrets).toHaveBeenCalledTimes(2);
    expect(seen).toHaveBeenLastCalledWith({ url: '', forge: '', tokenSecretRef: 'new' });
  });

  test('the issues sync waits for a saved repository', async () => {
    client.listChatMcpSecrets.mockResolvedValue({});
    renderRepository();
    expect(createButton().disabled).toBe(true);
    expect(screen.getByText(/Save a repository first/)).toBeTruthy();
    await screen.findByRole('option', { name: 'None' });
  });

  test('creates the issues sync and says what it made', async () => {
    client.listChatMcpSecrets.mockResolvedValue({});
    client.createAutomation.mockResolvedValue({ id: 'r1' });
    renderRepository({ value: { url: 'https://github.com/acme/docs', forge: '', tokenSecretRef: '' }, savedUrl: 'https://github.com/acme/docs' });
    expect(screen.getByText(/into docs-issues every 15 minutes/)).toBeTruthy();
    fireEvent.click(createButton());
    expect((await screen.findByRole('status')).textContent).toContain('Created the rule "docs issues"');
    expect(client.createAutomation).toHaveBeenCalledWith(expect.objectContaining({
      type: 'cron', name: 'docs issues', schedule: '*/15 * * * *', enabled: true,
      sync: expect.objectContaining({ source: 'issues', collection: 'docs-issues', repositoryFrom: 'docs' }),
    }));
  });

  test('a refused rule and an unreadable secret list are said plainly', async () => {
    client.listChatMcpSecrets.mockRejectedValue(new Error('feature disabled'));
    client.createAutomation.mockRejectedValue(new Error('API Error (400): sync: unknown collection'));
    renderRepository({ savedUrl: 'https://github.com/acme/docs' });
    expect((await screen.findByRole('alert')).textContent).toContain('Stored secrets could not be listed: feature disabled');
    fireEvent.click(createButton());
    expect(await screen.findByText('API Error (400): sync: unknown collection')).toBeTruthy();
    expect(screen.queryByRole('status')).toBeNull();
  });
});
