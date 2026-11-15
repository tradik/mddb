// @vitest-environment jsdom
// CollectionSpelling (FE-020): off by default and silent; switched on it
// shows the count (or "Checking…"), a failure, and each suggestion with a
// Replace button that rewrites every occurrence of the word.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';

vi.mock('../lib/use-collection-spelling', () => ({ useCollectionSpelling: vi.fn() }));

const { useCollectionSpelling } = await import('../lib/use-collection-spelling');
const { default: CollectionSpelling } = await import('./CollectionSpelling');

const state = (over = {}) => ({ suggestions: [], checking: false, error: null, ...over });

beforeEach(() => vi.mocked(useCollectionSpelling).mockReset().mockReturnValue(state()));
afterEach(cleanup);

const toggle = () => screen.getByRole('checkbox', { name: 'Check spelling against the collection dictionary' });

describe('CollectionSpelling', () => {
  test('off by default: the hook is told so and nothing else shows', () => {
    render(<CollectionSpelling collection="blog" lang="en" text="Teh" />);
    expect(toggle().checked).toBe(false);
    expect(useCollectionSpelling).toHaveBeenLastCalledWith({ enabled: false, collection: 'blog', lang: 'en', text: 'Teh' });
    expect(screen.queryByRole('status')).toBeNull();
  });

  test('on: the count, singular and plural', () => {
    render(<CollectionSpelling collection="blog" lang="en" text="x" />);
    fireEvent.click(toggle());
    expect(useCollectionSpelling).toHaveBeenLastCalledWith(expect.objectContaining({ enabled: true }));
    expect(screen.getByRole('status').textContent).toBe('0 suggestions');
    vi.mocked(useCollectionSpelling).mockReturnValue(state({ suggestions: [{ word: 'teh', corrected: 'the' }] }));
    fireEvent.click(toggle());
    fireEvent.click(toggle());
    expect(screen.getByRole('status').textContent).toBe('1 suggestion');
  });

  test('while checking and after a failure', () => {
    vi.mocked(useCollectionSpelling).mockReturnValue(state({ checking: true, error: 'spell index loading' }));
    render(<CollectionSpelling collection="blog" lang="en" text="x" />);
    expect(screen.queryByRole('alert')).toBeNull();
    fireEvent.click(toggle());
    expect(screen.getByRole('status').textContent).toBe('Checking…');
    expect(screen.getByRole('alert').textContent).toBe('Spelling check failed: spell index loading');
  });

  test('Replace rewrites every occurrence, keeping capitals; without onChange it does nothing', () => {
    vi.mocked(useCollectionSpelling).mockReturnValue(state({ suggestions: [{ word: 'teh', corrected: 'the' }, { word: 'recieve', corrected: 'receive' }] }));
    const onChange = vi.fn();
    const { unmount } = render(<CollectionSpelling collection="blog" lang="en" text="Teh cat and teh dog" onChange={onChange} />);
    fireEvent.click(toggle());
    expect(screen.getByRole('list', { name: 'Spelling suggestions' }).children).toHaveLength(2);
    fireEvent.click(screen.getByRole('button', { name: 'Replace teh with the' }));
    expect(onChange).toHaveBeenCalledWith('The cat and the dog');
    unmount();
    render(<CollectionSpelling collection="blog" lang="en" text="teh" />);
    fireEvent.click(toggle());
    fireEvent.click(screen.getByRole('button', { name: 'Replace teh with the' }));
  });
});
