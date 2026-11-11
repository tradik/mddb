// @vitest-environment jsdom
// nav-collapse (2.19.0): the folded groups survive a reload through
// localStorage, and a missing, broken or refusing storage changes nothing
// but the memory.
import { describe, test, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { NAV_COLLAPSE_KEY, readCollapsed, writeCollapsed, useCollapsedGroups } from './nav-collapse';

const throwing = { getItem: () => { throw new Error('blocked'); }, setItem: () => { throw new Error('quota'); } };

beforeEach(() => localStorage.clear());

describe('storage', () => {
  test('reads what was written', () => {
    writeCollapsed(['search', 'data']);
    expect(localStorage.getItem(NAV_COLLAPSE_KEY)).toBe('["search","data"]');
    expect(readCollapsed()).toEqual(['search', 'data']);
  });

  test('nothing, garbage or a non-list reads as nothing folded; non-strings are dropped', () => {
    expect(readCollapsed()).toEqual([]);
    localStorage.setItem(NAV_COLLAPSE_KEY, '{nope');
    expect(readCollapsed()).toEqual([]);
    localStorage.setItem(NAV_COLLAPSE_KEY, '{"a":1}');
    expect(readCollapsed()).toEqual([]);
    localStorage.setItem(NAV_COLLAPSE_KEY, '["chat",3,null]');
    expect(readCollapsed()).toEqual(['chat']);
  });

  test('a storage that throws, or none at all, is survived', () => {
    expect(readCollapsed(throwing)).toEqual([]);
    expect(() => writeCollapsed(['x'], throwing)).not.toThrow();
    expect(readCollapsed(null)).toEqual([]);
    expect(() => writeCollapsed(['x'], null)).not.toThrow();
  });
});

describe('useCollapsedGroups', () => {
  test('starts from storage, toggles and remembers', () => {
    writeCollapsed(['system']);
    const { result } = renderHook(() => useCollapsedGroups());
    expect(result.current.collapsed).toEqual(['system']);
    act(() => result.current.toggle('search'));
    expect(result.current.collapsed).toEqual(['system', 'search']);
    act(() => result.current.toggle('system'));
    expect(result.current.collapsed).toEqual(['search']);
    expect(readCollapsed()).toEqual(['search']);
  });

  test('works without storage', () => {
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked'); });
    const { result } = renderHook(() => useCollapsedGroups());
    act(() => result.current.toggle('chat'));
    expect(result.current.collapsed).toEqual(['chat']);
    spy.mockRestore();
  });
});
