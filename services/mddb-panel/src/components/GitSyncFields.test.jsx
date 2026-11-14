// @vitest-environment jsdom
// GitSyncFields (2.19.5, #421): "Git repository" as a synced source — its
// fields appear with the source, the forge only for a self-hosted host, the
// stored secrets listed by name with whether a value is set (never the
// value), and a list that cannot be read said plainly.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { useState } from 'react';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { listChatMcpSecrets: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: CronActionFields } = await import('./CronActionFields');
const { default: GitSyncFields } = await import('./GitSyncFields');
const { EMPTY_RULE, EMPTY_SYNC } = await import('../lib/automation-form');
const client = vi.mocked(mddbClient);

afterEach(cleanup);

const secrets = { secrets: [{ name: 'forge-token', set: true }, { name: 'old', set: false }], encryptionReady: true };

/** Renders a cron rule's fields with a sync, over real form state. */
function renderSync(sync, seen = vi.fn()) {
  function Harness() {
    const [form, setForm] = useState({ ...EMPTY_RULE, type: 'cron', cronAction: 'sync', sync: { ...EMPTY_SYNC, ...sync } });
    const update = (next) => {
      seen(next);
      setForm(next);
    };
    return <CronActionFields form={form} setForm={update} webhooks={[]} collections={[{ name: 'docs' }]} />;
  }
  render(<Harness />);
  return seen;
}

const last = (seen) => seen.mock.calls.at(-1)[0].sync;
const change = (label, value) => fireEvent.change(screen.getByLabelText(label), { target: { value } });

describe('GitSyncFields', () => {
  test('choosing Git repository shows its fields; typed values reach the form', async () => {
    client.listChatMcpSecrets.mockResolvedValue(secrets);
    const seen = renderSync({});
    expect(screen.queryByLabelText(/^Repository URL/)).toBeNull();
    change('Source', 'git');
    expect(screen.getByLabelText(/^Repository URL/)).toBeTruthy();
    expect(screen.getByText(/latest-release: the forge's newest release/)).toBeTruthy();
    change(/^Repository URL/, 'https://github.com/acme/docs.git');
    change(/^Ref/, 'v1.2.0');
    change(/^Include/, '*.md');
    change(/^Exclude/, 'testdata/**');
    fireEvent.click(screen.getByLabelText(/^Adopt/));
    expect(last(seen)).toMatchObject({
      source: 'git', url: 'https://github.com/acme/docs.git', ref: 'v1.2.0', include: '*.md', exclude: 'testdata/**', adopt: true,
    });
    expect(screen.queryByLabelText(/^Forge/)).toBeNull();
    await screen.findAllByRole('option', { name: 'forge-token (set)' });
  });

  test('a self-hosted host asks for its forge', async () => {
    client.listChatMcpSecrets.mockResolvedValue(secrets);
    const seen = renderSync({ source: 'git', url: 'https://git.example.com/acme/docs' });
    change(/^Forge/, 'forgejo');
    expect(last(seen).forge).toBe('forgejo');
    await screen.findAllByRole('option', { name: 'forge-token (set)' });
  });

  test('secrets are picked by name, with whether a value is stored — never the value', async () => {
    client.listChatMcpSecrets.mockResolvedValue(secrets);
    const seen = renderSync({ source: 'git', url: 'https://github.com/a/b', hookSecretRef: 'gone' });
    await screen.findAllByRole('option', { name: 'forge-token (set)' });
    expect(screen.getAllByRole('option', { name: 'old (not sealed: set it again)' })).toHaveLength(2);
    expect(screen.getByRole('option', { name: 'gone (not found)' })).toBeTruthy();
    change(/^Token secret/, 'forge-token');
    change(/^Webhook secret/, '');
    expect(last(seen)).toMatchObject({ tokenSecretRef: 'forge-token', hookSecretRef: '' });
    expect(screen.getByText(/Secret values are write-only/)).toBeTruthy();
    expect(screen.queryByLabelText(/^Token secret/).type).not.toBe('password');
  });

  test('a secret list that cannot be read is said plainly', async () => {
    client.listChatMcpSecrets.mockRejectedValue(new Error('feature disabled'));
    renderSync({ source: 'git', url: 'https://github.com/a/b' });
    expect((await screen.findByRole('alert')).textContent).toContain('Stored secrets could not be listed: feature disabled');
  });

  test('an answer without secrets lists none', async () => {
    client.listChatMcpSecrets.mockResolvedValue({});
    renderSync({ source: 'git', url: 'https://github.com/a/b' });
    await waitFor(() => expect(client.listChatMcpSecrets).toHaveBeenCalled());
    expect(screen.getAllByRole('option', { name: 'None' })).toHaveLength(2);
  });

  test('an answer after unmount is dropped', async () => {
    let answer;
    let fail;
    client.listChatMcpSecrets.mockReturnValueOnce(new Promise((resolve) => { answer = resolve; }));
    const first = render(<GitSyncFields value={{ ...EMPTY_SYNC, source: 'git' }} onChange={vi.fn()} />);
    first.unmount();
    answer(secrets);
    client.listChatMcpSecrets.mockReturnValueOnce(new Promise((_, reject) => { fail = reject; }));
    const second = render(<GitSyncFields value={{ ...EMPTY_SYNC, source: 'git' }} onChange={vi.fn()} />);
    second.unmount();
    fail(new Error('late'));
    await waitFor(() => expect(client.listChatMcpSecrets).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
