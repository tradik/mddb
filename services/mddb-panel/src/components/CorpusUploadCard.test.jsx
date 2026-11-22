// @vitest-environment jsdom
// CorpusUploadCard (FE-022): choosing a file shows what the pack holds before
// it is installed; a name clash, a bad file and a missing licence are worded
// for people; installing reports what was installed.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { inspectCorpus: vi.fn(), installCorpus: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: CorpusUploadCard } = await import('./CorpusUploadCard');

const manifest = { format: 2, name: 'kb', version: '2', license: 'MIT', docCount: 5, description: 'Handbook', collections: [{ name: 'docs' }, { name: 'notes' }] };
const file = new File(['x'], 'kb.mddbpack');
const onInstalled = vi.fn();

const choose = (f = file) => fireEvent.change(screen.getByLabelText('Pack file'), { target: { files: f ? [f] : [] } });
const failure = (status, error) => Object.assign(new Error(`API Error (${status}): ${error}`), { status, body: { error } });

beforeEach(() => {
  for (const fn of Object.values(mddbClient)) vi.mocked(fn).mockReset();
  onInstalled.mockReset().mockResolvedValue();
});
afterEach(cleanup);

describe('CorpusUploadCard', () => {
  test('shows the pack, then installs it', async () => {
    vi.mocked(mddbClient.inspectCorpus).mockResolvedValue({ manifest, conflicts: [] });
    vi.mocked(mddbClient.installCorpus).mockResolvedValue({ name: 'kb', docCount: 5, collections: ['docs', 'notes'], warnings: ['notes: keyword search only.'] });
    render(<CorpusUploadCard onInstalled={onInstalled} />);
    const install = screen.getByRole('button', { name: 'Install' });
    expect(install.disabled).toBe(true);
    choose();
    expect(await screen.findByText('Installs as: docs, notes (read-only)')).toBeTruthy();
    expect(screen.getByText('Handbook')).toBeTruthy();
    expect(mddbClient.inspectCorpus).toHaveBeenCalledWith(file);
    fireEvent.click(screen.getByRole('button', { name: 'Install' }));
    expect((await screen.findByRole('status')).textContent).toMatch(/Reading and checking/);
    await waitFor(() => expect(onInstalled).toHaveBeenCalled());
    expect(screen.getByText('Installed kb: 5 documents in docs, notes, read-only. Note: notes: keyword search only.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Install' }).disabled).toBe(true);
  });

  test('a pack of one collection without warnings', async () => {
    vi.mocked(mddbClient.inspectCorpus).mockResolvedValue({ manifest: { format: 1, name: 'rfc', license: 'MIT', docCount: 1 }, conflicts: [] });
    vi.mocked(mddbClient.installCorpus).mockResolvedValue({ name: 'rfc', docCount: 1, warnings: [] });
    render(<CorpusUploadCard onInstalled={onInstalled} />);
    choose();
    expect(await screen.findByText('Installs as: rfc (read-only)')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Install' }));
    expect(await screen.findByText('Installed rfc: 1 documents in rfc, read-only.')).toBeTruthy();
  });

  test('a name clash keeps Install off and says why', async () => {
    vi.mocked(mddbClient.inspectCorpus).mockResolvedValue({ manifest, conflicts: ['collection "docs" already holds data'] });
    render(<CorpusUploadCard onInstalled={onInstalled} />);
    choose();
    expect(await screen.findByText(/already on this server/)).toBeTruthy();
    expect(screen.getByText('collection "docs" already holds data')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Install' }).disabled).toBe(true);
  });

  test.each([
    [failure(400, 'invalid corpus pack: reading the archive (is it a .mddbpack?): EOF'), 'not a pack this server can read'],
    [failure(400, 'invalid corpus pack: a corpus pack needs a licence'), 'does not say under which licence'],
    [failure(409, 'collection "docs" already holds data'), 'already on this server'],
  ])('a refusal is worded for people (%#)', async (err, text) => {
    vi.mocked(mddbClient.inspectCorpus).mockRejectedValue(err);
    render(<CorpusUploadCard onInstalled={onInstalled} />);
    choose();
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain(text);
    expect(alert.textContent).toContain(`Server: ${err.body.error}`);
  });

  test('a refusal without a body shows the message as the server words', async () => {
    vi.mocked(mddbClient.inspectCorpus).mockRejectedValue(new Error('unsupported pack format 3'));
    render(<CorpusUploadCard onInstalled={onInstalled} />);
    choose();
    expect((await screen.findByRole('alert')).textContent).toContain('Server: unsupported pack format 3');
  });

  test('any other failure is the server words; clearing the choice checks nothing', async () => {
    vi.mocked(mddbClient.inspectCorpus).mockRejectedValue(new Error('network down'));
    render(<CorpusUploadCard onInstalled={onInstalled} />);
    choose();
    expect((await screen.findByRole('alert')).textContent).toBe('network down');
    choose(null);
    expect(screen.queryByRole('alert')).toBeNull();
    expect(mddbClient.inspectCorpus).toHaveBeenCalledTimes(1);
  });
});
