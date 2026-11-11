// @vitest-environment jsdom
// CollectionSources (2.19.0): the syncs and triggers acting on a collection,
// their last result, Run now, refresh, the empty state and a failed load.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { listAutomation: vi.fn(), runAutomation: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: CollectionSources } = await import('./CollectionSources');
const client = vi.mocked(mddbClient);

const sync = {
  id: 's1', name: 'Docs sync', type: 'cron', enabled: true, schedule: '0 * * * *',
  sync: { source: 'sitemap', url: 'https://x/sitemap.xml', collection: 'docs' },
  lastResult: { status: 'success', added: 3 },
};
const trigger = { id: 't1', type: 'trigger', enabled: false, events: ['add'], query: 'urgent' };
const plainTrigger = { id: 't2', name: 'Any', type: 'trigger', enabled: true };
const webhook = { id: 'w1', name: 'Hook', type: 'webhook', enabled: true };

beforeEach(() => {
  client.listAutomation.mockReset();
});
afterEach(cleanup);

describe('CollectionSources', () => {
  test('lists rules acting on the collection with last result and Run now', async () => {
    client.listAutomation.mockResolvedValue({ rules: [sync, trigger, plainTrigger, webhook] });
    render(<CollectionSources collection="docs" />);
    expect(await screen.findByText('Docs sync')).toBeTruthy();
    expect(client.listAutomation).toHaveBeenCalledWith(undefined, { collection: 'docs' });
    expect(screen.getByText('0 * * * *')).toBeTruthy();
    expect(screen.getByText(/Last run: success/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Run Docs sync now' })).toBeTruthy();
    expect(screen.getByText('t1')).toBeTruthy();
    expect(screen.getByText('on add matching "urgent"')).toBeTruthy();
    expect(screen.getByText('on every change')).toBeTruthy();
    expect(screen.getByText('disabled')).toBeTruthy();
    expect(screen.getByText('Hook')).toBeTruthy();
  });

  test('accepts a bare list and refreshes', async () => {
    client.listAutomation.mockResolvedValue([sync]);
    render(<CollectionSources collection="docs" />);
    expect(await screen.findByText('Docs sync')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh sources and triggers' }));
    await waitFor(() => expect(client.listAutomation).toHaveBeenCalledTimes(2));
  });

  test.each([[{}], [null]])('empty state (%o)', async (reply) => {
    client.listAutomation.mockResolvedValue(reply);
    render(<CollectionSources collection="docs" />);
    expect(await screen.findByText('No synced sources or triggers act on this collection.')).toBeTruthy();
  });

  test('shows a failed load', async () => {
    client.listAutomation.mockRejectedValue(new Error('Forbidden'));
    render(<CollectionSources collection="docs" />);
    expect((await screen.findByRole('alert')).textContent).toBe('Forbidden');
  });
});

describe('CollectionSources across collections', () => {
  test('ignores the answer for a collection it no longer shows', async () => {
    let late;
    client.listAutomation
      .mockReturnValueOnce(new Promise((r) => { late = r; }))
      .mockResolvedValueOnce({ rules: [] });
    const { rerender } = render(<CollectionSources collection="old" />);
    rerender(<CollectionSources collection="docs" />);
    expect(await screen.findByText('No synced sources or triggers act on this collection.')).toBeTruthy();
    late({ rules: [sync] });
    await Promise.resolve();
    expect(screen.queryByText('Docs sync')).toBeNull();
  });

  test('ignores a late failure after unmount', async () => {
    let fail;
    client.listAutomation.mockReturnValueOnce(new Promise((_, r) => { fail = r; }));
    const { unmount } = render(<CollectionSources collection="docs" />);
    unmount();
    fail(new Error('late'));
    await Promise.resolve();
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
