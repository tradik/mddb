// @vitest-environment jsdom
// DocumentCompare (FE-013): pick another document, read it, and show the
// metadata and content that differ; a failed read shows the error.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getDocument: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: DocumentCompare } = await import('./DocumentCompare');

const doc = { collection: 'docs', key: 'guide', lang: 'en', meta: { tags: ['a'] }, contentMd: '# Guide\nHello' };

beforeEach(() => {
  mddbClient.getDocument.mockReset();
});
afterEach(cleanup);

function setup(document = doc) {
  const onClose = vi.fn();
  render(<DocumentCompare document={document} onClose={onClose} />);
  return {
    onClose,
    collection: screen.getByLabelText('Collection'),
    key: screen.getByLabelText('Key'),
    lang: screen.getByLabelText('Language'),
    compare: screen.getByRole('button', { name: 'Compare' }),
  };
}

describe('DocumentCompare', () => {
  test('starts from the same collection and key; Compare waits for a language', () => {
    const { collection, key, lang, compare } = setup();
    expect(screen.getByText('docs / guide (en)')).toBeTruthy();
    expect(collection.value).toBe('docs');
    expect(key.value).toBe('guide');
    expect(lang.value).toBe('');
    expect(compare.disabled).toBe(true);
    fireEvent.change(lang, { target: { value: 'pl' } });
    expect(compare.disabled).toBe(false);
  });

  test('a document without a collection starts with an empty one', () => {
    const { collection } = setup({ key: 'k', lang: 'en' });
    expect(collection.value).toBe('');
  });

  test('reads the other document and shows how it differs', async () => {
    let resolve;
    mddbClient.getDocument.mockReturnValue(new Promise((r) => { resolve = r; }));
    const { collection, key, lang, compare } = setup();
    fireEvent.change(collection, { target: { value: 'docs-pl' } });
    fireEvent.change(key, { target: { value: ' guide ' } });
    fireEvent.change(lang, { target: { value: ' pl ' } });
    fireEvent.click(compare);
    expect(compare.disabled).toBe(true);
    await act(async () => resolve({ key: 'guide', lang: 'pl', meta: { tags: ['b'] }, contentMd: '# Guide\nCześć' }));
    expect(mddbClient.getDocument).toHaveBeenCalledWith({ collection: 'docs-pl', key: 'guide', lang: 'pl' });
    expect(screen.getByText('docs-pl / guide (pl)')).toBeTruthy();
    expect(screen.getByText('Metadata: 1 key differ')).toBeTruthy();
    expect(screen.getByText('Content: 1 line added, 1 removed')).toBeTruthy();
    expect(screen.getByText('Cześć')).toBeTruthy();
  });

  test('documents without content or metadata compare as empty', async () => {
    mddbClient.getDocument.mockResolvedValue({ key: 'k', lang: 'pl' });
    const { lang, compare } = setup({ collection: 'c', key: 'k', lang: 'en' });
    fireEvent.change(lang, { target: { value: 'pl' } });
    await act(async () => fireEvent.click(compare));
    expect(screen.getByText('No metadata change.')).toBeTruthy();
    expect(screen.getByText('No change.')).toBeTruthy();
  });

  test('a failed read shows the error; close leaves', async () => {
    mddbClient.getDocument.mockRejectedValue(new Error('API Error (404): document not found'));
    const { onClose, lang, compare } = setup();
    fireEvent.change(lang, { target: { value: 'de' } });
    await act(async () => fireEvent.click(compare));
    expect(screen.getByRole('alert').textContent).toBe('API Error (404): document not found');
    fireEvent.click(screen.getByRole('button', { name: 'Close comparison' }));
    expect(onClose).toHaveBeenCalled();
  });
});
