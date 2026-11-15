/**
 * The colour theme (FE-015): light, dark, or the system's setting. The
 * choice is kept in localStorage under THEME_KEY; the inline script in
 * index.html reads the same key and sets the `dark` class on <html> before
 * the first paint, and this module keeps that class in step afterwards —
 * when the user picks a theme in the header, and, on "system", when the OS
 * setting changes. Every storage and matchMedia access is guarded: without
 * them the panel stays light and forgets the choice on reload.
 */
import { useSyncExternalStore } from 'react';

/** The localStorage key; index.html's inline script uses the same string. */
export const THEME_KEY = 'mddb.theme';

/** The choices the header offers, in its order. */
export const THEMES = ['light', 'dark', 'system'];

const DARK_QUERY = '(prefers-color-scheme: dark)';

/** The stored choice; "system" when nothing (valid) is stored. */
export function readTheme(storage = globalThis.localStorage) {
  try {
    const theme = storage.getItem(THEME_KEY);
    return THEMES.includes(theme) ? theme : 'system';
  } catch {
    return 'system';
  }
}

/** Stores the choice; a storage that refuses is ignored. */
export function writeTheme(theme, storage = globalThis.localStorage) {
  try {
    storage.setItem(THEME_KEY, theme);
  } catch {
    // No storage: the choice lasts until the page reloads.
  }
}

/** The OS's dark-mode media query, or null where there is none. */
function darkQuery(win = globalThis.window) {
  try {
    return win.matchMedia(DARK_QUERY);
  } catch {
    return null;
  }
}

/** Whether the OS asks for dark mode; false where it cannot be read. */
export function systemPrefersDark(win = globalThis.window) {
  return Boolean(darkQuery(win)?.matches);
}

/** The mode a choice shows: "dark" or "light". */
export function resolveTheme(theme, prefersDark) {
  if (theme === 'dark') return 'dark';
  if (theme === 'light') return 'light';
  return prefersDark ? 'dark' : 'light';
}

/** Sets or clears the `dark` class on `root` (the <html> element). */
export function applyTheme(resolved, root = globalThis.document?.documentElement) {
  root?.classList.toggle('dark', resolved === 'dark');
}

// The shared state behind useTheme: one choice for every component, read
// from storage on first use.
let choice = null;
const listeners = new Set();
let query = null;

/** The current choice. */
export function currentTheme() {
  if (choice === null) choice = readTheme();
  return choice;
}

/** The mode on screen now. */
export function currentResolvedTheme() {
  return resolveTheme(currentTheme(), systemPrefersDark());
}

function refresh() {
  applyTheme(currentResolvedTheme());
  listeners.forEach((listener) => listener());
}

/** Picks a theme: remembers it, applies it and re-renders every user. */
export function setTheme(theme) {
  choice = THEMES.includes(theme) ? theme : 'system';
  writeTheme(choice);
  refresh();
}

/**
 * Subscribes `listener` to theme changes. The first subscriber also starts
 * listening to the OS setting, the last one to leave stops.
 */
function subscribe(listener) {
  listeners.add(listener);
  if (listeners.size === 1) {
    query = darkQuery();
    query?.addEventListener?.('change', refresh);
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      query?.removeEventListener?.('change', refresh);
      query = null;
    }
  };
}

/** Forgets the in-memory choice, so the next read goes to storage (tests). */
export function resetThemeState() {
  choice = null;
}

/**
 * `{ theme, resolved, setTheme }`: the choice ("light", "dark", "system"),
 * the mode it shows ("light" or "dark") and the setter. Rendered on the
 * server (the HTML export), it reports light.
 */
export function useTheme() {
  const theme = useSyncExternalStore(subscribe, currentTheme, () => 'system');
  const resolved = useSyncExternalStore(subscribe, currentResolvedTheme, () => 'light');
  return { theme, resolved, setTheme };
}
