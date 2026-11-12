/**
 * Which sidebar groups the user folded (2.19.0), kept in localStorage. Every
 * access is guarded: a private window, blocked site data or a full quota
 * leaves the menu working, only without memory. All groups start open.
 */
import { useState } from 'react';

export const NAV_COLLAPSE_KEY = 'mddb.nav.collapsed';

/** The ids of the folded groups; empty when nothing (valid) is stored. */
export function readCollapsed(storage = globalThis.localStorage) {
  try {
    const ids = JSON.parse(storage.getItem(NAV_COLLAPSE_KEY));
    return Array.isArray(ids) ? ids.filter((id) => typeof id === 'string') : [];
  } catch {
    return [];
  }
}

/** Stores the folded ids; a storage that refuses is ignored. */
export function writeCollapsed(ids, storage = globalThis.localStorage) {
  try {
    storage.setItem(NAV_COLLAPSE_KEY, JSON.stringify(ids));
  } catch {
    // No storage: the state lasts until the page reloads.
  }
}

/**
 * `{ collapsed, toggle, expand }`: the folded ids, a toggle that remembers
 * them, and `expand(id)`, which unfolds one group (a no-op when it is open).
 */
export function useCollapsedGroups() {
  const [collapsed, setCollapsed] = useState(() => readCollapsed());
  const update = (change) => {
    setCollapsed((current) => {
      const next = change(current);
      if (next === current) return current;
      writeCollapsed(next);
      return next;
    });
  };
  const toggle = (id) => update((current) => (current.includes(id) ? current.filter((c) => c !== id) : [...current, id]));
  const expand = (id) => update((current) => (current.includes(id) ? current.filter((c) => c !== id) : current));
  return { collapsed, toggle, expand };
}
