import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';

/**
 * SpellCleanupBox applies the best correction to every token of the text
 * (/v1/spell-cleanup) and returns the cleaned text with how many corrections
 * were applied — what a search would run instead of the typed query.
 */
export default function SpellCleanupBox({ collection, text, lang }) {
  const [result, setResult] = useState(null);
  const { busy, error, run } = useAction();

  const cleanup = () => run(async () => setResult(await mddbClient.spellCleanup({ collection, text, lang })));

  return (
    <div className="space-y-2">
      <button
        type="button"
        onClick={cleanup}
        disabled={busy || !text.trim()}
        className="px-4 py-2 bg-surface-subtle text-fg rounded-lg hover:bg-surface-strong disabled:opacity-50 text-sm font-medium"
      >
        {busy ? 'Cleaning…' : 'Clean up text'}
      </button>
      {error?.status === 404
        ? <p role="alert" className="text-sm text-warning-strong">Spell correction is off on this server: start it with MDDB_SPELL=true.</p>
        : <ApiErrorNotice error={error} collection={collection || '*'} />}
      {result && (
        <div role="status" className="p-3 bg-success-soft rounded-lg border border-success-line text-sm">
          <p className="text-xs text-success-strong mb-1">Cleaned text ({result.correctionsApplied} corrections)</p>
          <p className="font-mono text-fg">{result.cleaned}</p>
        </div>
      )}
    </div>
  );
}
