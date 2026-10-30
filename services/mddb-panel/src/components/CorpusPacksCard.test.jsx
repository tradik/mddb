// @vitest-environment jsdom
// CorpusPacksCard (AGT-005): the installed packs, installing a .mddbpack,
// removing one after a confirm, and building one for download — each
// refreshing the pack list and the collection list, and showing refusals.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    listCorpus: vi.fn(), installCorpus: vi.fn(), removeCorpus: vi.fn(), buildCorpus: vi.fn(), getStats: vi.fn(),
  },
}));
vi.mock('../lib/download', () => ({ saveBlob: vi.fn() }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { saveBlob } = await import('../lib/download');
const { useStore } = await import('../lib/store');
const { default: CorpusPacksCard } = await import('./CorpusPacksCard');

const initialStore = useStore.getState();

const pack = {
  collection: 'kb',
  readOnly: true,
  corpus: {
    name: 'kb', version: '1.2', license: 'CC-BY-4.0', mddbVersion: '2.18.10', docCount: 42,
    installedAt: '2026-10-30T10:00:00Z', vectorsDisabled: true,
  },
};
const bare = { collection: 'plain', readOnly: false, corpus: { name: 'plain', license: 'MIT', mddbVersion: '2.18.10', docCount: 1 } };

beforeEach(() => {
  useStore.setState(initialStore, true);
  useStore.setState({ stats: { collections: [{ name: 'docs' }, { name: 'kb' }] } });
  for (const fn of Object.values(mddbClient)) vi.mocked(fn).mockReset();
  vi.mocked(mddbClient.listCorpus).mockResolvedValue({ packs: [pack, bare], total: 2 });
  vi.mocked(mddbClient.getStats).mockResolvedValue({ collections: [{ name: 'fresh' }] });
  vi.mocked(saveBlob).mockReset();
});
afterEach(cleanup);

describe('CorpusPacksCard', () => {
  test('lists packs with version, licence, read-only and vector warnings', async () => {
    render(<CorpusPacksCard />);
    expect(await screen.findByText('kb')).toBeTruthy();
    expect(screen.getByText('v1.2')).toBeTruthy();
    expect(screen.getAllByText('read-only')).toHaveLength(1);
    expect(screen.getByText(/42 documents · licence CC-BY-4.0 · built by MDDB 2.18.10 · installed/)).toBeTruthy();
    expect(screen.getByText(/Embedded by another model/)).toBeTruthy();
    expect(screen.getByText('1 documents · licence MIT · built by MDDB 2.18.10')).toBeTruthy();
  });

  test.each([
    [{ packs: [] }],
    [{}],
  ])('no packs (%o)', async (reply) => {
    vi.mocked(mddbClient.listCorpus).mockResolvedValue(reply);
    render(<CorpusPacksCard />);
    expect(await screen.findByText('No packs installed.')).toBeTruthy();
  });

  test('a failed listing is shown', async () => {
    vi.mocked(mddbClient.listCorpus).mockRejectedValue(new Error('API Error (403): forbidden'));
    render(<CorpusPacksCard />);
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (403): forbidden');
  });

  test('installs the chosen file, reports warnings and refreshes', async () => {
    vi.mocked(mddbClient.installCorpus).mockResolvedValue({ name: 'kb', docCount: 42, warnings: ['vectors disabled.'] });
    render(<CorpusPacksCard />);
    await screen.findByText('kb');
    const install = screen.getByRole('button', { name: 'Install' });
    expect(install.disabled).toBe(true);
    const file = new File(['x'], 'kb.mddbpack');
    fireEvent.change(screen.getByLabelText('Pack file'), { target: { files: [file] } });
    fireEvent.click(install);
    expect((await screen.findByRole('status')).textContent).toBe('Installed kb (42 documents), read-only. vectors disabled.');
    expect(mddbClient.installCorpus).toHaveBeenCalledWith(file);
    expect(mddbClient.listCorpus).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(useStore.getState().stats).toEqual({ collections: [{ name: 'fresh' }] }));
    expect(screen.getByRole('button', { name: 'Install' }).disabled).toBe(true);
  });

  test('an install without warnings, and a cleared file choice', async () => {
    vi.mocked(mddbClient.installCorpus).mockResolvedValue({ name: 'x', docCount: 1, warnings: [] });
    render(<CorpusPacksCard />);
    await screen.findByText('kb');
    const input = screen.getByLabelText('Pack file');
    fireEvent.change(input, { target: { files: [] } });
    expect(screen.getByRole('button', { name: 'Install' }).disabled).toBe(true);
    fireEvent.change(input, { target: { files: [new File(['x'], 'x.mddbpack')] } });
    fireEvent.click(screen.getByRole('button', { name: 'Install' }));
    expect((await screen.findByRole('status')).textContent).toBe('Installed x (1 documents), read-only.');
  });

  test('an install conflict is shown', async () => {
    vi.mocked(mddbClient.installCorpus).mockRejectedValue(new Error('API Error (409): collection "kb" holds data'));
    render(<CorpusPacksCard />);
    await screen.findByText('kb');
    fireEvent.change(screen.getByLabelText('Pack file'), { target: { files: [new File(['x'], 'kb.mddbpack')] } });
    fireEvent.click(screen.getByRole('button', { name: 'Install' }));
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (409): collection "kb" holds data');
  });

  test('removes a pack after confirmation', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    vi.mocked(mddbClient.removeCorpus).mockResolvedValue({ status: 'removed', name: 'kb', deleted: 42 });
    render(<CorpusPacksCard />);
    fireEvent.click(await screen.findByRole('button', { name: 'Remove pack kb' }));
    expect((await screen.findByRole('status')).textContent).toBe('Removed kb (42 documents deleted).');
    expect(mddbClient.removeCorpus).toHaveBeenCalledWith('kb');
  });

  test('a failed removal is shown and the button is usable again', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    vi.mocked(mddbClient.removeCorpus).mockRejectedValue(new Error('API Error (403): admin on "*" required'));
    render(<CorpusPacksCard />);
    fireEvent.click(await screen.findByRole('button', { name: 'Remove pack kb' }));
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (403): admin on "*" required');
    expect(screen.queryByRole('status')).toBeNull();
    expect(screen.getByRole('button', { name: 'Remove pack kb' }).disabled).toBe(false);
  });

  test('a declined confirm removes nothing', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    render(<CorpusPacksCard />);
    fireEvent.click(await screen.findByRole('button', { name: 'Remove pack kb' }));
    expect(mddbClient.removeCorpus).not.toHaveBeenCalled();
  });

  test('builds a pack and saves it', async () => {
    const file = { blob: new Blob(['p']), filename: 'docs-2.mddbpack' };
    vi.mocked(mddbClient.buildCorpus).mockResolvedValue(file);
    render(<CorpusPacksCard />);
    await screen.findByText('kb');
    const submit = screen.getByRole('button', { name: 'Build and download' });
    expect(submit.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'docs' } });
    fireEvent.change(screen.getByLabelText('Licence'), { target: { value: 'MIT' } });
    fireEvent.change(screen.getByLabelText('Version'), { target: { value: '2' } });
    fireEvent.click(submit);
    await waitFor(() => expect(saveBlob).toHaveBeenCalledWith(file, 'docs.mddbpack'));
    expect(mddbClient.buildCorpus).toHaveBeenCalledWith({ collection: 'docs', license: 'MIT', version: '2' });
  });

  test('a refused build is shown; no stats means no collections to pick', async () => {
    vi.mocked(mddbClient.buildCorpus).mockRejectedValue(new Error('API Error (400): encrypted collection'));
    render(<CorpusPacksCard />);
    await screen.findByText('kb');
    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'kb' } });
    fireEvent.change(screen.getByLabelText('Licence'), { target: { value: 'MIT' } });
    fireEvent.click(screen.getByRole('button', { name: 'Build and download' }));
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (400): encrypted collection');
    cleanup();
    useStore.setState({ stats: null });
    render(<CorpusPacksCard />);
    await screen.findByText('kb');
    expect(screen.getByLabelText('Collection').options).toHaveLength(1);
  });
});
