// @vitest-environment jsdom
// SaveTemplateDialog (FE-018): the name (the key by default), keeping the
// metadata or not, the busy state, success, and a refused write.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { addDocument: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: SaveTemplateDialog } = await import('./SaveTemplateDialog');

const doc = { collection: 'docs', key: 'guide', lang: 'en', meta: { tags: ['a'] }, contentMd: '# G' };

beforeEach(() => {
  mddbClient.addDocument.mockReset();
});
afterEach(cleanup);

function setup() {
  const onClose = vi.fn();
  render(<SaveTemplateDialog document={doc} onClose={onClose} />);
  return { onClose, name: screen.getByLabelText('Template name'), save: screen.getByRole('button', { name: 'Save template' }) };
}

describe('SaveTemplateDialog', () => {
  test('defaults to the key; an empty name cannot be saved; Cancel closes', () => {
    const { onClose, name, save } = setup();
    expect(screen.getByRole('dialog', { name: 'Save as template' })).toBeTruthy();
    expect(name.value).toBe('guide');
    fireEvent.change(name, { target: { value: '   ' } });
    expect(save.disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onClose).toHaveBeenCalled();
  });

  test('saves under the name with the metadata, then says where', async () => {
    let resolve;
    mddbClient.addDocument.mockReturnValue(new Promise((r) => { resolve = r; }));
    const { onClose, name, save } = setup();
    fireEvent.change(name, { target: { value: ' meeting ' } });
    fireEvent.click(save);
    expect(screen.getByRole('button', { name: 'Saving...' }).disabled).toBe(true);
    await act(async () => resolve({}));
    expect(mddbClient.addDocument).toHaveBeenCalledWith({ collection: '_templates', key: 'meeting', lang: 'en', meta: { tags: ['a'] }, contentMd: '# G' });
    expect(screen.getByRole('status').textContent).toContain('Saved "meeting" to _templates.');
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalled();
  });

  test('without the metadata when the box is cleared', async () => {
    mddbClient.addDocument.mockResolvedValue({});
    const { save } = setup();
    fireEvent.click(screen.getByLabelText("Keep this document's metadata as the template's defaults"));
    await act(async () => fireEvent.click(save));
    expect(mddbClient.addDocument).toHaveBeenCalledWith(expect.objectContaining({ key: 'guide', meta: {} }));
  });

  test('a refused write reads "Not permitted" and keeps the form', async () => {
    mddbClient.addDocument.mockRejectedValue(Object.assign(new Error('API Error (403): forbidden'), { status: 403, body: { error: 'forbidden' } }));
    const { save } = setup();
    await act(async () => fireEvent.click(save));
    expect(screen.getByText('Not permitted')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Save template' }).disabled).toBe(false);
  });
});
