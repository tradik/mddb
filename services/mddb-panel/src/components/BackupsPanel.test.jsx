// @vitest-environment jsdom
// BackupsPanel (2.19.11): the Backup & Restore page — backup now, the list
// of backups (scheduled runs and the ones taken here), picking one for the
// restore, the scheduled backups, and what an account without admin on "*"
// or a server without automations sees.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    getMe: vi.fn(), listFeatures: vi.fn(), listAutomation: vi.fn(), listAutomationLogs: vi.fn(), backup: vi.fn(), restore: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { resetFeatures } = await import('../lib/features');
const { RUNS_PER_RULE } = await import('../lib/use-backup-history');
const { default: BackupsPanel } = await import('./BackupsPanel');

const initialStore = useStore.getState();
const nightly = { id: 'r1', type: 'cron', name: 'nightly', enabled: true, schedule: '0 3 * * *', backup: { path: 'nightly', keep: 7 } };
const offsite = { id: 'r2', type: 'cron', name: 'offsite', enabled: false, schedule: '0 4 * * 0', backup: { target: 's3', bucket: 'b' } };
const logs = {
  r1: {
    logs: [
      { id: 'l1', action: 'backup', ruleId: 'r1', ruleName: 'nightly', status: 'success', timestamp: 100, target: '/data/backups/nightly/mddb-1.db' },
      { id: 'l3', action: 'backup', ruleId: 'r1', ruleName: 'nightly', status: 'error', timestamp: 80, error: 'backup failed the integrity check' },
      { id: 'l4', action: 'backup', ruleId: 'r1', ruleName: 'nightly', status: 'skipped', timestamp: 70, target: '/data/backups/nightly/mddb-0.db' },
    ],
  },
  r2: { logs: [{ id: 'l2', action: 'backup', ruleId: 'r2', ruleName: 'offsite', status: 'success', timestamp: 90, target: 's3://b/mddb-2.db' }] },
};

async function renderPage() {
  render(<BackupsPanel />);
  await act(async () => {});
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  resetFeatures();
  for (const fn of Object.values(mddbClient)) fn.mockReset();
  mddbClient.getMe.mockResolvedValue({ username: 'root', admin: true });
  mddbClient.listFeatures.mockResolvedValue({ features: [{ name: 'automation', enabled: true }] });
  mddbClient.listAutomation.mockResolvedValue({ rules: [nightly, offsite, { id: 'w', type: 'cron', webhookId: 'x' }] });
  mddbClient.listAutomationLogs.mockImplementation(({ ruleId }) => Promise.resolve(logs[ruleId]));
});
afterEach(cleanup);

describe('BackupsPanel', () => {
  test('lists the scheduled runs and the backup rules, one log read per rule', async () => {
    await renderPage();
    expect(mddbClient.listAutomation).toHaveBeenCalledWith('cron');
    expect(mddbClient.listAutomationLogs).toHaveBeenCalledWith({ ruleId: 'r1', limit: RUNS_PER_RULE });
    expect(mddbClient.listAutomationLogs).toHaveBeenCalledTimes(2);
    const table = screen.getByRole('table', { name: 'Backups' });
    expect(within(table).getByText('mddb-1.db')).toBeTruthy();
    expect(within(table).getAllByText('Scheduled: nightly')).toHaveLength(3);
    expect(within(table).getAllByText('Stored and checked')).toHaveLength(2);
    // A failed run has no file: it shows its error, and no restore button.
    expect(within(table).getByText('Failed').className).toContain('bg-danger-soft');
    expect(within(table).getByText('backup failed the integrity check')).toBeTruthy();
    expect(within(table).getByText('—')).toBeTruthy();
    expect(within(table).getByText('Skipped').className).toContain('bg-surface-subtle');
    expect(within(table).getAllByRole('button', { name: /for restore/ })).toHaveLength(1);
    // A backup in object storage is fetched back by hand: no restore button.
    expect(within(table).queryByRole('button', { name: 'Use mddb-2.db for restore' })).toBeNull();

    const scheduled = screen.getByRole('region', { name: 'Scheduled backups' });
    expect(scheduled.textContent).toContain('0 3 * * *');
    expect(scheduled.textContent).toContain('Server disk, folder nightly · keeps the newest 7');
    expect(scheduled.textContent).toContain('(paused)');
    fireEvent.click(within(scheduled).getByRole('button', { name: 'Open nightly in Automation' }));
    expect(useStore.getState().viewMode).toBe('automation');
  });

  test('a listed backup fills the restore field; a backup taken now joins the list', async () => {
    mddbClient.backup.mockResolvedValueOnce({ backup: '/data/backups/now.db' });
    await renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Use mddb-1.db for restore' }));
    const field = screen.getByLabelText('Restore from file');
    expect(field.value).toBe('/data/backups/nightly/mddb-1.db');
    expect(document.activeElement).toBe(field);

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Back up now' })));
    const table = screen.getByRole('table', { name: 'Backups' });
    expect(within(table).getAllByRole('row')[1].textContent).toContain('now.db');
    expect(field.value).toBe('/data/backups/now.db');
  });

  test('without scheduled backups the list and the schedule say so', async () => {
    mddbClient.listAutomation.mockResolvedValue([]);
    await renderPage();
    expect(screen.getByText('No backups yet. Take one above, or schedule them below.')).toBeTruthy();
    expect(screen.getByText(/Nothing is scheduled/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Open Automation' }));
    expect(useStore.getState().viewMode).toBe('automation');
  });

  test('a log without entries lists nothing; a failed load is shown', async () => {
    mddbClient.listAutomationLogs.mockResolvedValue(undefined);
    await renderPage();
    expect(screen.queryByRole('table', { name: 'Backups' })).toBeNull();
    cleanup();
    mddbClient.listAutomation.mockRejectedValue(new Error('API Error (500): down'));
    await renderPage();
    expect(screen.getByRole('alert').textContent).toBe('API Error (500): down');
  });

  test('automations switched off: no schedule, nothing loaded', async () => {
    useStore.setState({ config: { automationsEnabled: false } });
    await renderPage();
    expect(mddbClient.listAutomation).not.toHaveBeenCalled();
    expect(screen.queryByRole('region', { name: 'Scheduled backups' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Back up now' }).disabled).toBe(false);
  });

  test('an account without admin on "*" reads why, and nothing can be started', async () => {
    mddbClient.getMe.mockResolvedValue({ username: 'ann', admin: false });
    await renderPage();
    expect(screen.getByRole('note').textContent).toContain('only an administrator of the whole server');
    expect(screen.getByRole('button', { name: 'Back up now' }).disabled).toBe(true);
    expect(mddbClient.listAutomation).not.toHaveBeenCalled();
    expect(screen.queryByRole('region', { name: 'Scheduled backups' })).toBeNull();
  });

  test('without authentication the page works as for an admin', async () => {
    mddbClient.getMe.mockRejectedValue(new Error('API Error (404): not found'));
    await renderPage();
    expect(screen.queryByRole('note')).toBeNull();
    expect(screen.getByRole('button', { name: 'Back up now' }).disabled).toBe(false);
  });
});
