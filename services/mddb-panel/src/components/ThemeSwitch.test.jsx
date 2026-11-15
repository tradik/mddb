// @vitest-environment jsdom
// FE-015: the header's light / dark / system switch.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import ThemeSwitch from './ThemeSwitch';
import { THEME_KEY, resetThemeState } from '../lib/theme';

beforeEach(() => {
  localStorage.clear();
  resetThemeState();
  document.documentElement.classList.remove('dark');
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })));
});
afterEach(cleanup);

describe('ThemeSwitch', () => {
  test('is a named group of three buttons with System pressed by default', () => {
    render(<ThemeSwitch />);
    expect(screen.getByRole('group', { name: 'Colour theme' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'System theme' }).getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByRole('button', { name: 'Light theme' }).getAttribute('aria-pressed')).toBe('false');
    expect(screen.getByRole('button', { name: 'Dark theme' }).getAttribute('aria-pressed')).toBe('false');
  });

  test('Dark applies the class and remembers the choice; Light undoes it', () => {
    render(<ThemeSwitch />);
    fireEvent.click(screen.getByRole('button', { name: 'Dark theme' }));
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    expect(localStorage.getItem(THEME_KEY)).toBe('dark');
    expect(screen.getByRole('button', { name: 'Dark theme' }).getAttribute('aria-pressed')).toBe('true');

    fireEvent.click(screen.getByRole('button', { name: 'Light theme' }));
    expect(document.documentElement.classList.contains('dark')).toBe(false);
    expect(localStorage.getItem(THEME_KEY)).toBe('light');
  });

  test('starts from the stored choice', () => {
    localStorage.setItem(THEME_KEY, 'dark');
    render(<ThemeSwitch />);
    expect(screen.getByRole('button', { name: 'Dark theme' }).getAttribute('aria-pressed')).toBe('true');
  });
});
