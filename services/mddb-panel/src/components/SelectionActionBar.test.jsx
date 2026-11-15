// @vitest-environment jsdom
// SelectionActionBar on its own: a long selection is named by its first
// keys, a failure without messages is still counted, the selection exports
// as ZIP or NDJSON (FE-014) and a read-only collection offers export only.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { deleteBatch: vi.fn(), export: vi.fn() } }));
vi.mock('../lib/download', () => ({ saveBlob: vi.fn() }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { saveBlob } = await import('../lib/download');
const { default: SelectionActionBar } = await import('./SelectionActionBar');

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const two = [{ key: 'a', lang: 'en', collection: 'notes', meta: {} }, { key: 'b', lang: 'pl', collection: 'notes' }];

describe('SelectionActionBar', () => {
  test('names the first five keys and how many more; reports failures without messages', async () => {
    mddbClient.deleteBatch.mockResolvedValueOnce({ failed: 2 });
    const onDeleted = vi.fn();
    const documents = [...'abcdefg'].map((key) => ({ key, lang: 'en' }));
    render(<SelectionActionBar collection="notes" documents={documents} onClear={() => {}} onDeleted={onDeleted} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete selected' }));
    expect(screen.getByRole('alertdialog').textContent).toContain('a (en), b (en), c (en), d (en), e (en) and 2 more');
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Delete' })));
    expect(onDeleted).toHaveBeenCalledWith('Deleted 0, 2 failed.');
  });

  test('nothing selected shows no bar', () => {
    const { container } = render(<SelectionActionBar collection="notes" documents={[]} onClear={() => {}} onDeleted={() => {}} />);
    expect(container.textContent).toBe('');
  });

  test.each([
    ['Export ZIP', 'zip'],
    ['Export NDJSON', 'ndjson'],
  ])('%s sends only key and lang of the selection and saves the file', async (label, format) => {
    const file = { blob: new Blob(['x']), filename: `notes.${format}` };
    mddbClient.export.mockResolvedValueOnce(file);
    render(<SelectionActionBar collection="notes" documents={two} onClear={() => {}} onDeleted={() => {}} />);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: label })));
    expect(mddbClient.export).toHaveBeenCalledWith({
      collection: 'notes', format, keys: [{ key: 'a', lang: 'en' }, { key: 'b', lang: 'pl' }],
    });
    expect(saveBlob).toHaveBeenCalledWith(file, `notes.${format}`);
  });

  test('a refused export says not permitted and saves nothing', async () => {
    mddbClient.export.mockRejectedValueOnce(Object.assign(new Error('API Error (403): forbidden'), { status: 403, body: { error: 'forbidden' } }));
    render(<SelectionActionBar collection="notes" documents={two} onClear={() => {}} onDeleted={() => {}} />);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Export ZIP' })));
    expect(screen.getByText('Not permitted')).toBeTruthy();
    expect(saveBlob).not.toHaveBeenCalled();
  });

  test('a read-only collection offers export and no delete', () => {
    render(<SelectionActionBar collection="notes" documents={two} readOnly onClear={() => {}} onDeleted={() => {}} />);
    expect(screen.getByRole('button', { name: 'Export ZIP' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Delete selected' })).toBeNull();
  });
});
