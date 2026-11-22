// @vitest-environment jsdom
// IssuesSyncFields (INT-023): "Issues and pull requests" as a synced source —
// its fields appear with the source, the URL is optional when a collection
// supplies the repository, the forge shows only for a self-hosted host, and
// the payload carries the kinds, state, date and comments.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { useState } from 'react';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { listChatMcpSecrets: vi.fn(), putChatMcpSecret: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: CronActionFields } = await import('./CronActionFields');
const { EMPTY_RULE, EMPTY_SYNC, buildPayload } = await import('../lib/automation-form');
const client = vi.mocked(mddbClient);

afterEach(cleanup);

/** Renders a cron rule's fields with a sync, over real form state. */
function renderSync(sync, seen = vi.fn()) {
  function Harness() {
    const [form, setForm] = useState({ ...EMPTY_RULE, type: 'cron', cronAction: 'sync', sync: { ...EMPTY_SYNC, ...sync } });
    const update = (next) => {
      seen(next);
      setForm(next);
    };
    return <CronActionFields form={form} setForm={update} webhooks={[]} collections={[{ name: 'docs' }, { name: 'docs-issues' }]} />;
  }
  render(<Harness />);
  return seen;
}

const last = (seen) => seen.mock.calls.at(-1)[0];
const change = (label, value) => fireEvent.change(screen.getByLabelText(label), { target: { value } });

describe('IssuesSyncFields', () => {
  test('choosing Issues shows its fields; a repository from a collection needs no URL', async () => {
    client.listChatMcpSecrets.mockResolvedValue({ secrets: [{ name: 'gh', set: true }] });
    const seen = renderSync({ collection: 'docs-issues' });
    expect(screen.queryByLabelText(/^State/)).toBeNull();
    change('Source', 'issues');
    expect(screen.getByLabelText(/^Repository URL/).placeholder).toContain("empty: the collection's repository");
    change(/^Repository from collection/, 'docs');
    change(/^State/, 'open');
    change(/^Updated since/, '2026-03-01');
    fireEvent.click(screen.getByLabelText('Pull requests'));
    fireEvent.click(screen.getByLabelText('With comments'));
    await screen.findAllByRole('option', { name: 'gh (set)' });
    change(/^Token secret/, 'gh');
    expect(screen.queryByLabelText(/^Forge/)).toBeNull();
    expect(buildPayload({ ...last(seen), name: 'docs issues', schedule: '*/15 * * * *' }).sync).toEqual({
      source: 'issues', collection: 'docs-issues', lang: 'en', repositoryFrom: 'docs', state: 'open', since: '2026-03-01',
      include: ['issues', 'pulls'], comments: false, tokenSecretRef: 'gh',
    });
  });

  test('a self-hosted URL asks for its forge; unticking issues leaves only pulls', async () => {
    client.listChatMcpSecrets.mockResolvedValue({});
    const seen = renderSync({ source: 'issues', url: 'https://git.example.com/acme/docs' });
    change(/^Forge/, 'gitea');
    fireEvent.click(screen.getByLabelText('Issues'));
    fireEvent.click(screen.getByLabelText('Pull requests'));
    expect(last(seen).sync).toMatchObject({ forge: 'gitea', includeIssues: false, includePulls: true });
    await screen.findAllByRole('option', { name: 'None' });
  });
});
