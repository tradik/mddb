import { useId, useState } from 'react';
import { useCollectionSpelling } from '../lib/use-collection-spelling';
import { replaceWord } from '../lib/spell-words';

/**
 * CollectionSpelling (FE-020): an opt-in check of the editor text against
 * the collection's spelling dictionary, below the editor. Off by default —
 * the browser's own spellcheck stays as it is; when switched on, each word
 * with a suggestion is listed with a button that replaces it everywhere.
 */
/** "1 suggestion", "3 suggestions". */
function suggestionCount(n) {
  return n === 1 ? '1 suggestion' : `${n} suggestions`;
}

export default function CollectionSpelling({ collection, lang, text, onChange }) {
  const [enabled, setEnabled] = useState(false);
  const toggleId = useId();
  const { suggestions, checking, error } = useCollectionSpelling({ enabled, collection, lang, text });

  return (
    <div className="border-t border-line bg-surface px-4 py-2 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <label htmlFor={toggleId} className="flex items-center gap-2 text-fg-soft">
          <input
            id={toggleId}
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          <span>Check spelling against the collection dictionary</span>
        </label>
        {enabled && (
          <output className="text-xs text-fg-muted">
            {checking ? 'Checking…' : suggestionCount(suggestions.length)}
          </output>
        )}
      </div>
      {enabled && error && (
        <p role="alert" className="mt-1 text-xs text-danger">Spelling check failed: {error}</p>
      )}
      {enabled && suggestions.length > 0 && (
        <ul aria-label="Spelling suggestions" className="mt-2 flex flex-wrap gap-2">
          {suggestions.map(({ word, corrected }) => (
            <li key={word} className="flex items-center gap-1 rounded border border-warning-line bg-warning-soft px-2 py-0.5 text-warning-strong">
              <span className="line-through">{word}</span>
              <span aria-hidden="true">→</span>
              <span className="sr-only">suggested:</span>
              <span className="font-medium">{corrected}</span>
              <button
                type="button"
                onClick={() => onChange?.(replaceWord(text, word, corrected))}
                aria-label={`Replace ${word} with ${corrected}`}
                className="ml-1 rounded px-1 text-xs underline hover:bg-warning-muted"
              >
                Replace
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
