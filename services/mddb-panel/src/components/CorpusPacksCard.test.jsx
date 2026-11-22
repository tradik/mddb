// @vitest-environment jsdom
// CorpusPacksCard (AGT-005, FE-022): the installed packs, one row per pack —
// its facts, the collections of a pack of several, the vector warning —
// downloading one again, and removing one after the confirm dialog.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { removeCorpus: vi.fn(), buildCorpus: vi.fn() } }));
vi.mock('../lib/download', () => ({ saveBlob: vi.fn() }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { saveBlob } = await import('../lib/download');
const { useStore } = await import('../lib/store');
const { groupPacks } = await import('../lib/corpus-packs');
const { default: CorpusPacksCard } = await import('./CorpusPacksCard');

const packs = groupPacks([
  { collection: 'docs', readOnly: true, corpus: { name: 'kb', version: '2', license: 'CC-BY-4.0', packLicense: 'CC-BY-4.0', mddbVersion: '2.19.11', docCount: 3, bytes: 2048, installedAt: '2026-11-01T10:00:00Z', description: 'Two collections', collections: ['docs', 'notes'] } },
  { collection: 'notes', readOnly: true, corpus: { name: 'kb', version: '2', license: 'MIT', packLicense: 'CC-BY-4.0', mddbVersion: '2.19.11', docCount: 2, bytes: 1024, vectorsDisabled: true, sources: [{ repo: 'https://example.org/notes', commit: 'v1', license: 'MIT' }] } },
  { collection: 'rfc', readOnly: true, corpus: { name: 'rfc', license: 'MIT', mddbVersion: '2.18.10', docCount: 1, sources: [{ repo: 'https://rfc.example', license: 'MIT' }] } },
]);

const onChanged = vi.fn();
const renderCard = (props = {}) => render(<CorpusPacksCard packs={packs} canManage onChanged={onChanged} {...props} />);

beforeEach(() => {
  for (const fn of Object.values(mddbClient)) vi.mocked(fn).mockReset();
  vi.mocked(saveBlob).mockReset();
  onChanged.mockReset().mockResolvedValue();
});
afterEach(cleanup);

describe('CorpusPacksCard', () => {
  test('one row per pack, with its facts and collections', () => {
    renderCard({ highlight: 'kb' });
    expect(screen.getByText('kb')).toBeTruthy();
    expect(screen.getByText('v2')).toBeTruthy();
    expect(screen.getByText('Two collections')).toBeTruthy();
    expect(screen.getByText('5 documents · 3 KB · licence CC-BY-4.0')).toBeTruthy();
    expect(screen.getByText(/from https:\/\/example.org\/notes @ v1/)).toBeTruthy();
    expect(screen.getByText(/another embedding model/)).toBeTruthy();
    expect(screen.getByText('1 documents · licence MIT')).toBeTruthy();
    expect(screen.getByText(/from https:\/\/rfc.example$/)).toBeTruthy();
    expect(screen.getAllByText('read-only')).toHaveLength(2);
    expect(document.getElementById('pack-kb').className).toContain('bg-info-muted');
  });

  test('a collection of a pack opens its documents', () => {
    renderCard();
    fireEvent.click(screen.getByRole('button', { name: 'notes' }));
    expect(useStore.getState().currentCollection).toBe('notes');
    expect(useStore.getState().viewMode).toBe('documents');
  });

  test('nothing installed, a listing error, and no controls for a reader', () => {
    renderCard({ packs: [], loadError: new Error('API Error (500): down') });
    expect(screen.getByText('No packs installed on this server.')).toBeTruthy();
    expect(screen.getByRole('alert').textContent).toBe('API Error (500): down');
    cleanup();
    renderCard({ canManage: false });
    expect(screen.queryByRole('button', { name: 'Remove pack kb' })).toBeNull();
  });

  test('downloads a pack built again from its collections', async () => {
    const file = { blob: new Blob(['p']), filename: null };
    vi.mocked(mddbClient.buildCorpus).mockResolvedValue(file);
    renderCard();
    fireEvent.click(screen.getByRole('button', { name: 'Download pack kb' }));
    expect(screen.getByRole('status').textContent).toMatch(/Preparing the download/);
    await waitFor(() => expect(saveBlob).toHaveBeenCalledWith(file, 'kb-2.mddbpack'));
    expect(mddbClient.buildCorpus).toHaveBeenCalledWith({
      name: 'kb', license: 'CC-BY-4.0', version: '2', description: 'Two collections',
      collections: [{ name: 'docs', license: 'CC-BY-4.0' }, { name: 'notes', license: 'MIT', sources: [{ repo: 'https://example.org/notes', commit: 'v1', license: 'MIT' }] }],
    });
  });

  test('removes a pack after the confirm dialog', async () => {
    vi.mocked(mddbClient.removeCorpus).mockResolvedValue({ deleted: 5 });
    renderCard();
    fireEvent.click(screen.getByRole('button', { name: 'Remove pack kb' }));
    expect(screen.getByRole('alertdialog').textContent).toContain('(docs, notes) and their 5 documents');
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    expect((await screen.findByRole('status')).textContent).toBe('Removed kb: 5 documents deleted.');
    expect(mddbClient.removeCorpus).toHaveBeenCalledWith('kb');
    expect(onChanged).toHaveBeenCalled();
    expect(screen.queryByRole('alertdialog')).toBeNull();
  });

  test('a failed removal is shown; cancel removes nothing', async () => {
    vi.mocked(mddbClient.removeCorpus).mockRejectedValue(Object.assign(new Error('API Error (403): admin'), { status: 403, body: { error: 'admin access required' } }));
    renderCard();
    fireEvent.click(screen.getByRole('button', { name: 'Remove pack rfc' }));
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    expect((await screen.findByRole('alert')).textContent).toContain('Not permitted');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('alertdialog')).toBeNull();
    expect(onChanged).not.toHaveBeenCalled();
  });
});
