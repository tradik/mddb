/**
 * How other parts of the panel open the Corpus packs page with something
 * already chosen (FE-022): the collection list's "Create pack from this
 * collection", the header's "Corpus packs", a read-only collection's link
 * to the pack it came from. The page takes the intent once and clears it.
 *
 * An intent is `{ build: [collection, …] }` to start a pack of those
 * collections, `{ pack: name }` to show an installed pack, or `{}`.
 */
import { create } from 'zustand';
import { useStore } from './store';

export const useCorpusIntent = create((set) => ({
  intent: null,
  setIntent: (intent) => set({ intent }),
}));

/** Opens the Corpus packs page with `intent`. */
export function openCorpusPage(intent = {}) {
  useCorpusIntent.getState().setIntent(intent);
  useStore.getState().setViewMode('corpus');
}
