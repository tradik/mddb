// @vitest-environment jsdom
// System page maintenance: a collection's checksum, export and revision
// truncation (behind a confirmation naming the collection), and backup now /
// restore (behind a confirmation naming the file).
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { checksum: vi.fn(), export: vi.fn(), truncate: vi.fn(), backup: vi.fn(), restore: vi.fn() } }));
vi.mock('../lib/download', () => ({ saveBlob: vi.fn() }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { saveBlob } = await import('../lib/download');
const { useStore } = await import('../lib/store');
const { default: CollectionMaintenanceCard } = await import('./CollectionMaintenanceCard');
const { default: BackupRestoreCard } = await import('./BackupRestoreCard');

const initialStore = useStore.getState();
const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: { error: text } });
const click = (name) => act(async () => fireEvent.click(screen.getByRole('button', { name })));

beforeEach(() => {
  useStore.setState(initialStore, true);
  useStore.setState({ stats: { collections: [{ name: 'docs' }, { name: 'kb' }] } });
  for (const fn of Object.values(mddbClient)) fn.mockReset();
  vi.mocked(saveBlob).mockReset();
});
afterEach(cleanup);

describe('CollectionMaintenanceCard', () => {
  test('checksum of the chosen collection', async () => {
    mddbClient.checksum.mockResolvedValueOnce({ collection: 'kb', checksum: '66e59f57', documentCount: 5 });
    render(<CollectionMaintenanceCard />);
    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'kb' } });
    await click('Checksum');
    expect(mddbClient.checksum).toHaveBeenCalledWith('kb');
    expect(screen.getByRole('status').textContent).toBe('Checksum of kb: 66e59f57 over 5 documents.');
  });

  test('exports as NDJSON or ZIP and saves the file', async () => {
    const file = { blob: new Blob(['x']), filename: null };
    mddbClient.export.mockResolvedValue(file);
    render(<CollectionMaintenanceCard />);
    await click('Export');
    expect(mddbClient.export).toHaveBeenCalledWith({ collection: 'docs', format: 'ndjson' });
    expect(saveBlob).toHaveBeenCalledWith(file, 'docs.ndjson');
    expect(screen.getByRole('status').textContent).toBe('Exported docs as NDJSON.');
    fireEvent.change(screen.getByLabelText('Export format'), { target: { value: 'zip' } });
    await click('Export');
    expect(saveBlob).toHaveBeenLastCalledWith(file, 'docs.zip');
    expect(screen.getByRole('status').textContent).toBe('Exported docs as a ZIP of Markdown files.');
  });

  test('truncates after a confirmation naming the collection; failures as worded', async () => {
    mddbClient.truncate.mockResolvedValueOnce({ status: 'truncated' }).mockRejectedValueOnce(apiError(403, 'forbidden'));
    render(<CollectionMaintenanceCard />);
    fireEvent.change(screen.getByLabelText('Revisions to keep'), { target: { value: '' } });
    await click('Truncate revisions');
    const dialog = screen.getByRole('alertdialog', { name: 'Truncate revisions' });
    expect(within(dialog).getByText('docs')).toBeTruthy();
    expect(dialog.textContent).toContain('last 0 revisions');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(mddbClient.truncate).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText('Revisions to keep'), { target: { value: '2' } });
    await click('Truncate revisions');
    await click('Truncate');
    expect(mddbClient.truncate).toHaveBeenCalledWith({ collection: 'docs', keepRevs: 2 });
    expect(screen.getByRole('status').textContent).toBe('Revision history of docs truncated to the last 2 per document.');
    await click('Truncate revisions');
    await click('Truncate');
    expect(screen.getByText('Not permitted')).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Revisions to keep'), { target: { value: 'x' } });
    await click('Truncate revisions');
    await click('Truncate');
    expect(mddbClient.truncate).toHaveBeenLastCalledWith({ collection: 'docs', keepRevs: 0 });
  });

  test('without collections nothing can run', () => {
    useStore.setState({ stats: null });
    render(<CollectionMaintenanceCard />);
    expect(screen.getByRole('button', { name: 'Checksum' }).disabled).toBe(true);
  });
});

describe('BackupRestoreCard', () => {
  test('backs up now, offers that file for restore, restores after a confirmation', async () => {
    mddbClient.backup.mockResolvedValueOnce({ backup: 'backup-1.db' }).mockResolvedValueOnce({ backup: 'nightly.db' });
    mddbClient.restore.mockResolvedValueOnce({ restored: 'nightly.db' });
    render(<BackupRestoreCard />);
    expect(screen.getByRole('button', { name: 'Restore' }).disabled).toBe(true);
    await click('Back up now');
    expect(mddbClient.backup).toHaveBeenCalledWith(undefined);
    expect(screen.getByRole('status').textContent).toBe('Backup written: backup-1.db. Restore takes this file name.');
    expect(screen.getByLabelText('Restore from file').value).toBe('backup-1.db');
    fireEvent.change(screen.getByLabelText('Backup file name (optional)'), { target: { value: ' nightly.db ' } });
    await click('Back up now');
    expect(mddbClient.backup).toHaveBeenLastCalledWith('nightly.db');
    fireEvent.change(screen.getByLabelText('Restore from file'), { target: { value: ' nightly.db ' } });

    await click('Restore');
    const dialog = screen.getByRole('alertdialog', { name: 'Restore the database' });
    expect(within(dialog).getByText('nightly.db')).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(mddbClient.restore).not.toHaveBeenCalled();
    await click('Restore');
    await act(async () => fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Restore' })));
    expect(mddbClient.restore).toHaveBeenCalledWith('nightly.db');
    expect(screen.getByRole('status').textContent).toBe('Database restored from nightly.db.');
  });

  test('a refused backup says not permitted', async () => {
    mddbClient.backup.mockRejectedValueOnce(apiError(403, 'admin access required'));
    render(<BackupRestoreCard />);
    await click('Back up now');
    expect(screen.getByText('Not permitted')).toBeTruthy();
    expect(screen.getByText('admin access required')).toBeTruthy();
  });
});
