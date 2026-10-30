// @vitest-environment jsdom
// Header: the search-mode switch, Refresh, Logout, and the result-cap notice
// (#368) it mounts for every view.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/auth', () => ({ authManager: { isAuthenticated: vi.fn(), logout: vi.fn() } }));

const { authManager } = await import('../lib/auth');
const { useStore } = await import('../lib/store');
const { reportLimit } = await import('../lib/limit-notice');
const { default: Header } = await import('./Header');

const initialStore = useStore.getState();

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(authManager.isAuthenticated).mockReturnValue(false);
  reportLimit(null);
});
afterEach(cleanup);

describe('Header', () => {
  test.each([
    ['Metadata', 'metadata'],
    ['Full-Text', 'fulltext'],
    ['Vector', 'vector'],
    ['Hybrid', 'hybrid'],
  ])('%s switches the search mode to %s and highlights it', (label, mode) => {
    render(<Header onRefresh={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: label }));
    expect(useStore.getState().searchMode).toBe(mode);
    expect(screen.getByRole('button', { name: label }).className).toContain('bg-white');
  });

  test('Refresh calls the handler; Logout shows only when signed in', () => {
    const onRefresh = vi.fn();
    render(<Header onRefresh={onRefresh} />);
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(screen.queryByTitle('Logout')).toBeNull();
  });

  test('Logout signs out', () => {
    vi.mocked(authManager.isAuthenticated).mockReturnValue(true);
    render(<Header onRefresh={() => {}} />);
    fireEvent.click(screen.getByTitle('Logout'));
    expect(authManager.logout).toHaveBeenCalled();
  });

  test('a capped search shows the notice, with its limit, until dismissed', () => {
    render(<Header onRefresh={() => {}} />);
    expect(screen.queryByRole('status')).toBeNull();
    act(() => reportLimit({ endpoint: '/fts', limit: 1000 }));
    const notice = screen.getByRole('status');
    expect(notice.tagName).toBe('OUTPUT');
    expect(notice.textContent).toContain('The last search (/fts) was capped at 1000 results');
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByRole('status')).toBeNull();
  });

  test('a cap without a number names the server limit', () => {
    render(<Header onRefresh={() => {}} />);
    act(() => reportLimit({ endpoint: '/search', limit: null }));
    expect(screen.getByRole('status').textContent).toContain('capped at the server limit results');
  });
});
