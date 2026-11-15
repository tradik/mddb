// @vitest-environment jsdom
// FilterPanel (2.19.6, wired into the document list): metadata filters added,
// removed, applied and cleared; the sort field and order; the page size
// clamped to what the server's search takes. Everything lands in the store,
// which DocumentList's search reads.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, within } from '@testing-library/react';

const { useStore } = await import('../lib/store');
const { default: FilterPanel } = await import('./FilterPanel');

const initialStore = useStore.getState();

function open(state = {}) {
  useStore.setState(state);
  const onClose = vi.fn();
  render(<FilterPanel onClose={onClose} />);
  return onClose;
}

const keyField = () => screen.getByRole('textbox', { name: 'Filter key' });
const valueField = () => screen.getByRole('textbox', { name: 'Filter value' });

beforeEach(() => useStore.setState(initialStore, true));
afterEach(cleanup);

describe('FilterPanel metadata filters', () => {
  test('lists the store filters; Add filter and Enter add one, blanks are ignored', () => {
    const onClose = open({ filters: { author: ['jo'], tag: 'solo' } });
    const region = screen.getByRole('region', { name: 'Sort and filter documents' });
    expect(within(region).getByText('jo')).toBeTruthy();
    expect(within(region).getByText('solo')).toBeTruthy();

    fireEvent.change(keyField(), { target: { value: '  ' } });
    fireEvent.change(valueField(), { target: { value: 'x' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add filter' }));
    expect(screen.getAllByRole('listitem')).toHaveLength(2);

    fireEvent.change(keyField(), { target: { value: 'category' } });
    fireEvent.change(valueField(), { target: { value: ' museum ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add filter' }));
    expect(screen.getByText('museum')).toBeTruthy();
    expect(keyField().value).toBe('');
    expect(valueField().value).toBe('');

    fireEvent.change(keyField(), { target: { value: 'lang' } });
    fireEvent.change(valueField(), { target: { value: 'en' } });
    fireEvent.keyDown(valueField(), { key: 'a' });
    expect(screen.queryByText('en')).toBeNull();
    fireEvent.keyDown(valueField(), { key: 'Enter' });
    expect(screen.getByText('en')).toBeTruthy();

    // Nothing reaches the store until Apply.
    expect(useStore.getState().filters).toEqual({ author: ['jo'], tag: 'solo' });
    fireEvent.click(screen.getByRole('button', { name: 'Remove filter tag' }));
    fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }));
    expect(useStore.getState().filters).toEqual({ author: ['jo'], category: ['museum'], lang: ['en'] });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  test('Clear filters empties the list and the store at once', () => {
    const onClose = open({ filters: { author: ['jo'] } });
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(screen.queryAllByRole('listitem')).toHaveLength(0);
    expect(useStore.getState().filters).toEqual({});
    expect(onClose).not.toHaveBeenCalled();
  });

  test('the close button calls onClose', () => {
    const onClose = open();
    fireEvent.click(screen.getByRole('button', { name: 'Close sort and filter' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe('FilterPanel sort and page size', () => {
  test('the sort field and order go to the store; the pressed order is announced', () => {
    open();
    const sort = screen.getByLabelText('Sort by');
    expect(sort.value).toBe('addedAt');
    expect([...sort.options].map((o) => o.value)).toEqual(['addedAt', 'updatedAt', 'key']);
    fireEvent.change(sort, { target: { value: 'key' } });
    expect(useStore.getState().sortBy).toBe('key');

    const desc = screen.getByRole('button', { name: 'Descending' });
    const asc = screen.getByRole('button', { name: 'Ascending' });
    expect(desc.getAttribute('aria-pressed')).toBe('true');
    fireEvent.click(asc);
    expect(useStore.getState().sortAsc).toBe(true);
    expect(asc.getAttribute('aria-pressed')).toBe('true');
    expect(desc.getAttribute('aria-pressed')).toBe('false');
    fireEvent.click(desc);
    expect(useStore.getState().sortAsc).toBe(false);
  });

  test.each([
    ['50', 50],
    ['5000', 1000],
    ['0', 1],
    ['', 100],
  ])('a page size of %j is stored as %i', (typed, stored) => {
    open({ limit: 20 });
    const limit = screen.getByLabelText('Per page');
    expect(limit.value).toBe('20');
    fireEvent.change(limit, { target: { value: typed } });
    expect(useStore.getState().limit).toBe(stored);
  });
});
