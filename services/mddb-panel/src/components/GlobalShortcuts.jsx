import { useEffect, useState } from 'react';
import { globalCommand, isMacPlatform } from '../lib/editor-shortcuts';
import ShortcutsDialog from './ShortcutsDialog';

/** The element `/` focuses: the menu's search box (NavMenu marks it). */
export const SEARCH_SELECTOR = '[data-shortcut="search"]';

/**
 * GlobalShortcuts (FE-016): the panel-wide keys. `/` focuses the menu
 * search, `?` opens the shortcut list. Keys typed into a form field — the
 * editor included — are left alone (see globalCommand).
 */
export default function GlobalShortcuts() {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const onKeyDown = (e) => {
      const command = globalCommand(e);
      if (command === 'search') {
        const box = document.querySelector(SEARCH_SELECTOR);
        if (!box) return;
        e.preventDefault();
        box.focus();
      } else if (command === 'help') {
        e.preventDefault();
        setOpen(true);
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, []);

  return open ? <ShortcutsDialog isMac={isMacPlatform()} onClose={() => setOpen(false)} /> : null;
}
