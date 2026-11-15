/**
 * useDraftAutosave (FE-019): keeps the editor's unsaved changes as a draft
 * in localStorage while the user types, so a reload, an expired session or
 * a closed tab loses at most the last `delay` ms — and not even that, since
 * leaving the page or closing the editor writes the pending draft at once.
 * While changes are unsaved the browser asks before the page is left.
 */
import { useCallback, useEffect, useRef } from 'react';
import { removeDraft, writeDraft } from './drafts';

export const DRAFT_DELAY_MS = 1000;

/**
 * `doc` names the document ({ collection, key, lang }); `draft` is
 * { contentMd, meta, baseRevision }. With `dirty` the draft is written
 * `delay` ms after the last change; without it the stored draft is removed
 * (the editor matches the document again). `paused` (an older draft still
 * waits for restore or discard) neither writes nor removes.
 * Returns `discard()`, which drops the pending write and the stored draft.
 */
export function useDraftAutosave(doc, draft, { dirty, paused = false, delay = DRAFT_DELAY_MS }) {
  const pending = useRef(null);
  const { collection, key, lang } = doc;
  const { contentMd, meta, baseRevision } = draft;

  const flush = useCallback(() => {
    if (!pending.current) return;
    clearTimeout(pending.current.timer);
    writeDraft({ collection, key, lang }, pending.current.draft);
    pending.current = null;
  }, [collection, key, lang]);

  const discard = useCallback(() => {
    if (pending.current) clearTimeout(pending.current.timer);
    pending.current = null;
    removeDraft({ collection, key, lang });
  }, [collection, key, lang]);

  useEffect(() => {
    pending.current = null;
    if (paused) return undefined;
    if (!dirty) {
      removeDraft({ collection, key, lang });
      return undefined;
    }
    const value = { contentMd, meta, baseRevision };
    pending.current = { draft: value, timer: setTimeout(flush, delay) };
    return () => {
      if (pending.current) clearTimeout(pending.current.timer);
    };
  }, [collection, key, lang, contentMd, meta, baseRevision, dirty, paused, delay, flush]);

  // Unsaved changes: ask before the page goes, and keep the latest draft.
  useEffect(() => {
    if (!dirty) return undefined;
    const onBeforeUnload = (e) => {
      flush();
      e.preventDefault();
      e.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [dirty, flush]);

  // Closing the editor keeps what was typed in the last `delay` ms too.
  useEffect(() => flush, [flush]);

  return { discard };
}
