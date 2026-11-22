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
    listBackups: vi.fn(), downloadBackup: vi.fn(),
  },
}));
vi.mock('../lib/download', () => ({ saveBlob: vi.fn() }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { saveBlob } = await import('../lib/download');
const { BACKUP_FILES_PAGE } = await import('../lib/use-backup-files');
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
  // A server before 2.19.11: no file list. The listing tests below answer it.
  mddbClient.listBackups.mockRejectedValue(httpError(404));
  saveBlob.mockReset();
});
afterEach(cleanup);

/** A client error with the HTTP status, as apiError builds it. */
function httpError(status, message = `API Error (${status}): x`) {
  return Object.assign(new Error(message), { status });
}

const files = [
  { name: 'mddb-1.db', path: '/data/backups/nightly/mddb-1.db', bytes: 3 * 1024 * 1024, modifiedAt: '1970-01-01T00:01:40Z', state: 'checksummed', collectionFiles: 0 },
  { name: 'snap.db', path: '/data/backups/snap.db', bytes: 512, modifiedAt: '1970-01-01T00:03:20Z', state: 'unknown', collectionFiles: 4 },
];

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
    expect(mddbClient.listBackups).not.toHaveBeenCalled();
    expect(screen.queryByRole('region', { name: 'Scheduled backups' })).toBeNull();
  });

  test('every backup file is listed with its size, a run that wrote one shares its row', async () => {
    mddbClient.listBackups.mockResolvedValue({ backups: files, total: 2 });
    await renderPage();
    expect(mddbClient.listBackups).toHaveBeenCalledWith({ limit: BACKUP_FILES_PAGE });
    const table = screen.getByRole('table', { name: 'Backups' });
    const rows = within(table).getAllByRole('row');
    expect(within(table).getByRole('columnheader', { name: 'Size' })).toBeTruthy();
    // snap.db is the newest; mddb-1.db is one row with its scheduled run.
    expect(rows[1].textContent).toContain('snap.db');
    expect(rows[1].textContent).toContain('512 B');
    expect(rows[1].textContent).toContain('plus 4 collection files, kept on the server');
    expect(within(rows[1]).getByText('On the server').className).toContain('bg-surface-subtle');
    expect(within(table).getAllByText('mddb-1.db')).toHaveLength(1);
    expect(within(table).getAllByText('3.0 MB').length).toBeGreaterThan(0);
    expect(within(table).getAllByText('Scheduled: nightly')).toHaveLength(3);
    expect(within(table).getAllByRole('button', { name: /^Download/ })).toHaveLength(2);
    expect(within(table).getAllByRole('button', { name: /for restore/ })).toHaveLength(2);
    expect(screen.queryByRole('button', { name: 'Show older backups' })).toBeNull();

    mddbClient.downloadBackup.mockResolvedValueOnce({ blob: 'b', filename: null });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Download snap.db' })));
    expect(mddbClient.downloadBackup).toHaveBeenCalledWith('snap.db');
    expect(saveBlob).toHaveBeenCalledWith({ blob: 'b', filename: null }, 'snap.db');

    mddbClient.downloadBackup.mockRejectedValueOnce(httpError(404, 'API Error (404): backup not found'));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Download mddb-1.db' })));
    expect(screen.getByRole('alert').textContent).toBe('API Error (404): backup not found');

    fireEvent.click(screen.getByRole('button', { name: 'Use snap.db for restore' }));
    expect(screen.getByLabelText('Restore from file').value).toBe('/data/backups/snap.db');
  });

  test('older backups load page by page; a backup taken now reloads the list', async () => {
    mddbClient.listBackups.mockResolvedValueOnce({ backups: [files[1]], total: 2 }).mockResolvedValueOnce({ backups: [files[0]], total: 2 });
    await renderPage();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Show older backups' })));
    expect(mddbClient.listBackups).toHaveBeenLastCalledWith({ limit: BACKUP_FILES_PAGE, offset: 1 });
    expect(screen.getAllByRole('button', { name: /^Download/ })).toHaveLength(2);
    expect(screen.queryByRole('button', { name: 'Show older backups' })).toBeNull();

    mddbClient.listBackups.mockRejectedValueOnce(httpError(500, 'API Error (500): disk'));
    mddbClient.backup.mockResolvedValueOnce({ backup: '/data/backups/now.db' });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Back up now' })));
    expect(mddbClient.listBackups).toHaveBeenCalledTimes(3);
    expect(screen.getByRole('alert').textContent).toBe('API Error (500): disk');
    // The list is gone with the failed read; the page shows what it knows.
    expect(screen.getByRole('table', { name: 'Backups' }).textContent).toContain('now.db');
  });

  test('a failed next page is shown and keeps the rows', async () => {
    mddbClient.listBackups.mockResolvedValueOnce({ backups: [files[1]], total: 2 }).mockRejectedValueOnce(httpError(500, 'API Error (500): gone'));
    await renderPage();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Show older backups' })));
    expect(screen.getByRole('alert').textContent).toBe('API Error (500): gone');
    expect(screen.getByRole('button', { name: 'Download snap.db' })).toBeTruthy();
  });

  test('a server without the file list leaves the page as before', async () => {
    await renderPage();
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.queryByRole('columnheader', { name: 'Size' })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Download/ })).toBeNull();
    cleanup();
    // An answer that is not the API's JSON (a page server's HTML) counts the same.
    mddbClient.listBackups.mockRejectedValue(new SyntaxError('Unexpected token <'));
    await renderPage();
    expect(screen.queryByRole('alert')).toBeNull();
    cleanup();
    mddbClient.listBackups.mockRejectedValue(httpError(405));
    await renderPage();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  test('a refused file list is shown', async () => {
    mddbClient.listBackups.mockRejectedValue(httpError(403, 'API Error (403): admin access required'));
    await renderPage();
    expect(screen.getByRole('alert').textContent).toContain('API Error (403): admin access required');
  });

  test('without authentication the page works as for an admin', async () => {
    mddbClient.getMe.mockRejectedValue(new Error('API Error (404): not found'));
    await renderPage();
    expect(screen.queryByRole('note')).toBeNull();
    expect(screen.getByRole('button', { name: 'Back up now' }).disabled).toBe(false);
  });
});
