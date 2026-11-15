// @vitest-environment jsdom
// FE-015: the theme choice — storage, the system setting, the `dark` class
// on <html>, and the hook every themed component reads.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, act } from '@testing-library/react';
import { renderToStaticMarkup } from 'react-dom/server';
import {
  THEME_KEY, THEMES, readTheme, writeTheme, systemPrefersDark, resolveTheme, applyTheme,
  currentTheme, setTheme, resetThemeState, useTheme,
} from './theme';

/** A matchMedia stub whose `matches` and change listeners the test drives. */
function mediaStub(matches) {
  const listeners = new Set();
  const query = {
    matches,
    addEventListener: vi.fn((_, fn) => listeners.add(fn)),
    removeEventListener: vi.fn((_, fn) => listeners.delete(fn)),
    change(next) {
      query.matches = next;
      listeners.forEach((fn) => fn());
    },
  };
  return query;
}

let media;
beforeEach(() => {
  localStorage.clear();
  resetThemeState();
  document.documentElement.classList.remove('dark');
  media = mediaStub(false);
  vi.stubGlobal('matchMedia', vi.fn(() => media));
});
afterEach(cleanup);

describe('storage', () => {
  test('reads a stored choice, and "system" for nothing or junk', () => {
    expect(readTheme()).toBe('system');
    localStorage.setItem(THEME_KEY, 'dark');
    expect(readTheme()).toBe('dark');
    localStorage.setItem(THEME_KEY, 'purple');
    expect(readTheme()).toBe('system');
  });

  test('a storage that throws reads as "system" and ignores writes', () => {
    const broken = { getItem: () => { throw new Error('blocked'); }, setItem: () => { throw new Error('full'); } };
    expect(readTheme(broken)).toBe('system');
    expect(() => writeTheme('dark', broken)).not.toThrow();
  });

  test('writes the choice under the shared key', () => {
    writeTheme('light');
    expect(localStorage.getItem(THEME_KEY)).toBe('light');
    expect(THEMES).toEqual(['light', 'dark', 'system']);
  });
});

describe('resolving', () => {
  test('light and dark are fixed; system follows the OS', () => {
    expect(resolveTheme('light', true)).toBe('light');
    expect(resolveTheme('dark', false)).toBe('dark');
    expect(resolveTheme('system', true)).toBe('dark');
    expect(resolveTheme('system', false)).toBe('light');
  });

  test('the OS setting comes from matchMedia; without one it is light', () => {
    media.matches = true;
    expect(systemPrefersDark()).toBe(true);
    expect(systemPrefersDark({ matchMedia: () => { throw new Error('none'); } })).toBe(false);
    expect(systemPrefersDark({})).toBe(false);
  });

  test('applyTheme sets and clears the dark class, and tolerates no root', () => {
    applyTheme('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    applyTheme('light');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
    expect(() => applyTheme('dark', null)).not.toThrow();
  });
});

describe('setTheme', () => {
  test('stores, applies, and falls back to system for an unknown value', () => {
    setTheme('dark');
    expect(localStorage.getItem(THEME_KEY)).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    setTheme('nonsense');
    expect(currentTheme()).toBe('system');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });
});

function Probe() {
  const { theme, resolved } = useTheme();
  return <output>{`${theme}/${resolved}`}</output>;
}

describe('useTheme', () => {
  test('starts from storage and re-renders on setTheme', () => {
    localStorage.setItem(THEME_KEY, 'light');
    render(<Probe />);
    expect(screen.getByRole('status').textContent).toBe('light/light');
    act(() => setTheme('dark'));
    expect(screen.getByRole('status').textContent).toBe('dark/dark');
  });

  test('on "system" it follows an OS change, and stops listening on unmount', () => {
    const { unmount } = render(<Probe />);
    expect(screen.getByRole('status').textContent).toBe('system/light');
    act(() => media.change(true));
    expect(screen.getByRole('status').textContent).toBe('system/dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    unmount();
    expect(media.removeEventListener).toHaveBeenCalledWith('change', expect.any(Function));
  });

  test('two users share one OS listener', () => {
    render(<><Probe /><Probe /></>);
    expect(media.addEventListener).toHaveBeenCalledTimes(1);
  });

  test('works where matchMedia is missing', () => {
    vi.stubGlobal('matchMedia', undefined);
    render(<Probe />);
    expect(screen.getByRole('status').textContent).toBe('system/light');
  });

  test('renders light on the server', () => {
    localStorage.setItem(THEME_KEY, 'dark');
    expect(renderToStaticMarkup(<Probe />)).toBe('<output>system/light</output>');
  });
});
