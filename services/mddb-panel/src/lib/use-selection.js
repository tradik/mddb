import { useCallback, useState } from 'react';

/** A document's identity within a collection: key and language. */
export const docId = (doc) => `${doc.key}\u0000${doc.lang}`;

const NONE = new Map();

/**
 * Multi-select of documents in a list. The selection belongs to `scope` —
 * the list it was made in, compared by identity — so a reloaded or different
 * list starts with nothing selected, without an effect. `selected` keeps the
 * chosen documents in the order they were picked; `toggleAll(docs)` selects
 * every listed document, or clears them when all were selected.
 */
export function useSelection(scope) {
  const [state, setState] = useState({ scope, picked: NONE });
  const picked = state.scope === scope ? state.picked : NONE;

  const update = useCallback((change) => setState((prev) => ({
    scope,
    picked: change(prev.scope === scope ? prev.picked : NONE),
  })), [scope]);

  const isSelected = useCallback((doc) => picked.has(docId(doc)), [picked]);

  const toggle = useCallback((doc) => update((prev) => {
    const next = new Map(prev);
    if (next.has(docId(doc))) next.delete(docId(doc));
    else next.set(docId(doc), doc);
    return next;
  }), [update]);

  const toggleAll = useCallback((docs) => update((prev) => {
    const all = docs.length > 0 && docs.every((doc) => prev.has(docId(doc)));
    return all ? NONE : new Map(docs.map((doc) => [docId(doc), doc]));
  }), [update]);

  const clear = useCallback(() => update(() => NONE), [update]);

  return { selected: [...picked.values()], isSelected, toggle, toggleAll, clear };
}
