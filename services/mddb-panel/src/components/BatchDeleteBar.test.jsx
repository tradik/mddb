// @vitest-environment jsdom
// BatchDeleteBar on its own: a long selection is named by its first keys,
// and a failure without messages is still counted.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { deleteBatch: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: BatchDeleteBar } = await import('./BatchDeleteBar');

afterEach(cleanup);

describe('BatchDeleteBar', () => {
  test('names the first five keys and how many more; reports failures without messages', async () => {
    mddbClient.deleteBatch.mockResolvedValueOnce({ failed: 2 });
    const onDeleted = vi.fn();
    const documents = [...'abcdefg'].map((key) => ({ key, lang: 'en' }));
    render(<BatchDeleteBar collection="notes" documents={documents} onClear={() => {}} onDeleted={onDeleted} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete selected' }));
    expect(screen.getByRole('alertdialog').textContent).toContain('a (en), b (en), c (en), d (en), e (en) and 2 more');
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Delete' })));
    expect(onDeleted).toHaveBeenCalledWith('Deleted 0, 2 failed.');
  });

  test('nothing selected shows no bar', () => {
    const { container } = render(<BatchDeleteBar collection="notes" documents={[]} onClear={() => {}} onDeleted={() => {}} />);
    expect(container.textContent).toBe('');
  });
});
