// @vitest-environment jsdom
// CorpusBuildForm (FE-022): one collection or several, the pack name several
// need, the licence it always needs, a licence per collection, sources, and
// the download.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { buildCorpus: vi.fn() } }));
vi.mock('../lib/download', () => ({ saveBlob: vi.fn() }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { saveBlob } = await import('../lib/download');
const { default: CorpusBuildForm } = await import('./CorpusBuildForm');

const type = (label, value) => fireEvent.change(screen.getByLabelText(label), { target: { value } });
const submit = () => screen.getByRole('button', { name: 'Create and download' });
const file = { blob: new Blob(['p']), filename: 'srv.mddbpack' };

beforeEach(() => {
  vi.mocked(mddbClient.buildCorpus).mockReset().mockResolvedValue(file);
  vi.mocked(saveBlob).mockReset();
});
afterEach(cleanup);

describe('CorpusBuildForm', () => {
  test('a pack of one collection needs only a licence', async () => {
    render(<CorpusBuildForm collections={['docs', 'notes']} preset={['docs']} />);
    expect(submit().disabled).toBe(true);
    expect(screen.queryByLabelText(/Pack name/)).toBeNull();
    type(/^Licence \(required\)/, 'MIT');
    type(/^Version/, '1');
    type(/^Description/, 'Docs');
    fireEvent.click(submit());
    expect((await screen.findByRole('status')).textContent).toMatch(/Taking a snapshot/);
    await waitFor(() => expect(saveBlob).toHaveBeenCalledWith(file, 'docs-1.mddbpack'));
    expect(mddbClient.buildCorpus).toHaveBeenCalledWith({ collection: 'docs', license: 'MIT', version: '1', description: 'Docs' });
  });

  test('several collections need a pack name; each may have its own licence and the sources travel', async () => {
    render(<CorpusBuildForm collections={['docs', 'notes']} />);
    fireEvent.click(screen.getByLabelText('docs'));
    fireEvent.click(screen.getByLabelText('notes'));
    type(/^Licence \(required\)/, 'MIT');
    expect(submit().disabled).toBe(true);
    type(/^Pack name/, 'kb');
    type('Licence of notes', 'CC-BY-4.0');
    type('Licence of docs', 'MIT');
    fireEvent.click(screen.getByRole('button', { name: 'Add a source' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add a source' }));
    type('Source 1 address', 'https://example.org/r');
    type('Source 1 version or commit', 'v2');
    type('Source 1 licence', 'Apache-2.0');
    fireEvent.click(screen.getByRole('button', { name: 'Remove source 2' }));
    fireEvent.click(submit());
    await waitFor(() => expect(saveBlob).toHaveBeenCalledWith(file, 'kb.mddbpack'));
    expect(mddbClient.buildCorpus).toHaveBeenCalledWith({
      name: 'kb', collections: ['docs', { name: 'notes', license: 'CC-BY-4.0' }], license: 'MIT',
      sources: [{ repo: 'https://example.org/r', commit: 'v2', license: 'Apache-2.0' }],
    });
  });

  test('unticking, no collections, a new preset, and a refusal', async () => {
    vi.mocked(mddbClient.buildCorpus).mockRejectedValue(new Error('API Error (400): encrypted'));
    const { rerender } = render(<CorpusBuildForm collections={['docs']} />);
    fireEvent.click(screen.getByLabelText('docs'));
    fireEvent.click(screen.getByLabelText('docs'));
    expect(screen.getByLabelText('docs').checked).toBe(false);
    rerender(<CorpusBuildForm key="docs" collections={['docs']} preset={['docs']} />);
    expect(screen.getByLabelText('docs').checked).toBe(true);
    type(/^Licence \(required\)/, 'MIT');
    fireEvent.click(submit());
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (400): encrypted');
    cleanup();
    render(<CorpusBuildForm collections={[]} />);
    expect(screen.getByText('There are no collections yet.')).toBeTruthy();
  });
});
