// @vitest-environment jsdom
// use-action: busy while running, the result on success, the error kept on
// failure; and the Unix-time formatting the pages share.
import { describe, test, expect } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useAction } from './use-action';
import { formatUnix, dateInputToUnix } from './time-format';

describe('useAction', () => {
  test('returns the result and clears a previous error', async () => {
    const { result } = renderHook(() => useAction());
    await act(() => result.current.run(async () => { throw new Error('boom'); }));
    expect(result.current.error.message).toBe('boom');
    expect(result.current.busy).toBe(false);
    let value;
    await act(async () => { value = await result.current.run(async () => 42); });
    expect(value).toBe(42);
    expect(result.current.error).toBeNull();
    act(() => result.current.setError(new Error('set')));
    expect(result.current.error.message).toBe('set');
  });

  test('is busy while the action runs', async () => {
    const { result } = renderHook(() => useAction());
    let finish;
    let pending;
    act(() => { pending = result.current.run(() => new Promise((resolve) => { finish = resolve; })); });
    expect(result.current.busy).toBe(true);
    await act(async () => { finish(); await pending; });
    expect(result.current.busy).toBe(false);
  });
});

describe('time-format', () => {
  test('formatUnix', () => {
    expect(formatUnix(0)).toBe('—');
    expect(formatUnix(undefined)).toBe('—');
    expect(formatUnix(86400)).toBe(new Date(86400000).toLocaleString());
  });

  test('dateInputToUnix', () => {
    expect(dateInputToUnix('')).toBe(0);
    expect(dateInputToUnix('2026-01-02')).toBe(Math.floor(new Date('2026-01-02T00:00:00').getTime() / 1000));
  });
});
