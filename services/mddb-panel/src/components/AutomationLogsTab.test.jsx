// @vitest-environment jsdom
// AutomationLogsTab: the automation run log — loading and empty states, one
// row per run (webhook, backup and sync actions, rule type badge, HTTP code),
// the status filter, Refresh, the 10-second auto-refresh, the load-more cursor
// and a failed request.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { listAutomationLogs: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: AutomationLogsTab } = await import('./AutomationLogsTab');

const initialStore = useStore.getState();
const list = () => vi.mocked(mddbClient.listAutomationLogs);

// 2026-10-30 12:00:00 UTC, in seconds.
const now = 1793361600;

/** The rendered table rows, header excluded. */
function bodyRows(container) {
  return [...container.querySelectorAll('tbody tr')];
}

/** The text of each cell in a row. */
function cells(row) {
  return [...row.querySelectorAll('td')].map((td) => td.textContent);
}

/** Query sent by the n-th listAutomationLogs call (0-based). */
const query = (n) => list().mock.calls[n][0];

beforeEach(() => {
  useStore.setState(initialStore, true);
  list().mockReset();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('AutomationLogsTab', () => {
  test('shows the spinner while the first page loads', () => {
    list().mockReturnValue(new Promise(() => {}));
    render(<AutomationLogsTab />);
    expect(screen.getByText('Loading automation logs...')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Refresh' }).disabled).toBe(true);
    expect(query(0)).toEqual({ limit: 50, cursor: '', status: '' });
  });

  test('says so when there are no logs (or the reply has no list)', async () => {
    list().mockResolvedValue({ logs: 'not-a-list' });
    render(<AutomationLogsTab />);
    expect(await screen.findByText('No automation logs found.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  });

  test('renders a row per run: webhook, backup and sync actions', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(now * 1000);
    list().mockResolvedValue({
      logs: [
        { id: '1', timestamp: now - 5, ruleName: 'Hook', ruleType: 'webhook', webhookUrl: 'https://h', status: 'success', httpStatus: 200, durationMs: 12, attempt: 1 },
        { id: '2', timestamp: now - 120, ruleName: 'Nightly', ruleType: 'cron', action: 'backup', target: '/data/b.tar', removed: ['a', 'b'], status: 'error', error: 'disk full', durationMs: 0, attempt: 2 },
        { id: '3', timestamp: now - 7200, ruleName: 'Mirror', ruleType: 'cron', action: 'sync', sync: { added: 3, updated: 1, unchanged: 5, deleted: 2, failed: 1 }, status: 'skipped' },
        { id: '4', timestamp: now - 2 * 86400, ruleName: 'Watch', ruleType: 'trigger', action: 'backup', removed: [], status: 'success' },
        { action: 'sync' },
        { id: '6', ruleType: 'trigger', sync: { failed: 0 }, action: 'sync', status: 'error' },
      ],
    });
    const { container } = render(<AutomationLogsTab />);
    await screen.findByText('Hook');

    const rows = bodyRows(container);
    expect(rows.map(cells)).toEqual([
      ['5s ago', 'Hook', 'webhook', 'https://h', 'success', '200', '12ms', '1', ''],
      ['2m ago', 'Nightly', 'Cron', 'backup /data/b.tar, 2 old removed', 'error', '-', '0ms', '2', 'disk full'],
      ['2h ago', 'Mirror', 'Cron', 'sync +3 added, 1 updated, 5 unchanged, 2 deleted, 1 failed', 'skipped', '-', '-', '-', ''],
      ['2d ago', 'Watch', 'Trigger', 'backup -', 'success', '-', '-', '-', ''],
      ['-', '-', '', 'sync +0 added, 0 updated, 0 unchanged, 0 deleted', '', '-', '-', '-', ''],
      ['-', '-', 'Trigger', 'sync +0 added, 0 updated, 0 unchanged, 0 deleted', 'error', '-', '-', '-', ''],
    ]);
    expect(rows[0].querySelectorAll('td')[3].title).toBe('https://h');
    expect(rows[1].querySelectorAll('td')[3].title).toBe('/data/b.tar');
    expect(rows[1].querySelectorAll('td')[8].title).toBe('disk full');
    expect(rows[1].querySelector('td:nth-child(3) span').className).toContain('bg-purple-100');
    expect(rows[3].querySelector('td:nth-child(3) span').className).toContain('bg-amber-100');
    expect(rows[0].querySelector('td:nth-child(5) span').className).toContain('bg-green-100');
    expect(rows[1].querySelector('td:nth-child(5) span').className).toContain('bg-red-100');
    expect(rows[4].querySelector('td:nth-child(5) span').className).toContain('bg-gray-100');
    expect(rows[2].querySelector('.text-red-700').textContent).toBe(', 1 failed');
  });

  test('the status filter reloads with that status', async () => {
    list().mockResolvedValue({ logs: [] });
    render(<AutomationLogsTab />);
    await screen.findByText('No automation logs found.');
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'error' } });
    await waitFor(() => expect(list()).toHaveBeenCalledTimes(2));
    expect(query(1)).toEqual({ limit: 50, cursor: '', status: 'error' });
  });

  test('Refresh reloads the first page', async () => {
    list().mockResolvedValueOnce({ logs: [{ id: '1', ruleName: 'Old' }] })
      .mockResolvedValueOnce({ logs: [{ id: '2', ruleName: 'New' }] });
    render(<AutomationLogsTab />);
    await screen.findByText('Old');
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(await screen.findByText('New')).toBeTruthy();
    expect(screen.queryByText('Old')).toBeNull();
    expect(query(1)).toEqual({ limit: 50, cursor: '', status: '' });
  });

  test('Load more appends the next page with the cursor until there is none', async () => {
    let releaseSecond;
    list()
      .mockResolvedValueOnce({ logs: [{ id: '1', ruleName: 'First' }], nextCursor: 'c1' })
      .mockReturnValueOnce(new Promise((resolve) => { releaseSecond = resolve; }));
    render(<AutomationLogsTab />);
    await screen.findByText('First');

    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Load more' }).disabled).toBe(true));
    expect(query(1)).toEqual({ limit: 50, cursor: 'c1', status: '' });

    await act(async () => releaseSecond({ logs: [{ id: '2', ruleName: 'Second' }] }));
    expect(screen.getByText('First')).toBeTruthy();
    expect(screen.getByText('Second')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  });

  test('shows the error message of a failed request', async () => {
    list().mockRejectedValue(new Error('logs disabled'));
    render(<AutomationLogsTab />);
    expect(await screen.findByText('logs disabled')).toBeTruthy();
    expect(screen.getByText('No automation logs found.')).toBeTruthy();
  });

  test('auto-refresh reloads every 10 seconds until switched off', async () => {
    list().mockResolvedValue({ logs: [] });
    render(<AutomationLogsTab />);
    await screen.findByText('No automation logs found.');
    vi.useFakeTimers();

    const box = screen.getByRole('checkbox', { name: 'Auto-refresh' });
    fireEvent.click(box);
    expect(box.checked).toBe(true);
    await act(async () => vi.advanceTimersByTime(9999));
    expect(list()).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTime(1));
    expect(list()).toHaveBeenCalledTimes(2);
    await act(async () => vi.advanceTimersByTime(10000));
    expect(list()).toHaveBeenCalledTimes(3);
    expect(query(2)).toEqual({ limit: 50, cursor: '', status: '' });

    fireEvent.click(box);
    await act(async () => vi.advanceTimersByTime(30000));
    expect(list()).toHaveBeenCalledTimes(3);
  });
});
