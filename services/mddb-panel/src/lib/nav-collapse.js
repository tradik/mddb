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

/** `{ collapsed, toggle }`: the folded ids and a toggle that remembers them. */
export function useCollapsedGroups() {
  const [collapsed, setCollapsed] = useState(() => readCollapsed());
  const toggle = (id) => {
    setCollapsed((current) => {
      const next = current.includes(id) ? current.filter((c) => c !== id) : [...current, id];
      writeCollapsed(next);
      return next;
    });
  };
  return { collapsed, toggle };
}
