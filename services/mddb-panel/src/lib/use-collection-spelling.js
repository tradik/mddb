/**
 * useCollectionSpelling (FE-020): spelling suggestions for the editor text
 * from the server's spell index of a collection (/v1/spell-suggest; the
 * server looks a word up in the collection's dictionary first, then in its
 * language dictionary).
 *
 * Only words never checked before are sent: each answer is kept per
 * collection and language, so typing re-sends nothing already known. A
 * request waits until typing pauses for `delay` ms and carries at most
 * `batch` words; a longer text is checked over several requests.
 */
import { useEffect, useState } from 'react';
import mddbClient from './mddb-client';
import { extractWords } from './spell-words';

export const SPELL_DELAY_MS = 1500;
export const SPELL_BATCH = 200;

/**
 * Returns `{ suggestions, checking, error }`: `suggestions` lists
 * `{ word, corrected }` for words of `text` the dictionary would correct.
 * With `enabled` false nothing is sent and the list is empty.
 */
export function useCollectionSpelling({ enabled, collection, lang, text, delay = SPELL_DELAY_MS, batch = SPELL_BATCH }) {
  const scope = `${collection}|${lang}`;
  // Checked words of one scope: word -> correction, or null when it is fine.
  const [checked, setChecked] = useState({ scope, words: new Map() });
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState(null);

  const known = checked.scope === scope ? checked.words : new Map();
  const words = enabled ? extractWords(text) : [];
  const pending = words.filter((w) => !known.has(w)).slice(0, batch).join(' ');

  useEffect(() => {
    if (!pending) return undefined;
    const timer = setTimeout(async () => {
      setChecking(true);
      try {
        const list = pending.split(' ');
        const result = await mddbClient.spellSuggest({ collection, lang, text: pending, maxSuggestions: list.length });
        const answers = new Map(list.map((w) => [w, null]));
        for (const s of result?.tokenSuggestions || []) answers.set(s.original.toLowerCase(), s.corrected);
        setChecked((prev) => ({
          scope,
          words: new Map([...(prev.scope === scope ? prev.words : []), ...answers]),
        }));
        setError(null);
      } catch (err) {
        setError(err.message);
      } finally {
        setChecking(false);
      }
    }, delay);
    return () => clearTimeout(timer);
  }, [pending, scope, collection, lang, delay]);

  const suggestions = words.filter((w) => known.get(w)).map((w) => ({ word: w, corrected: known.get(w) }));
  return { suggestions, checking, error };
}
