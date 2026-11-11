import { useState } from 'react';
import { Trash2 } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';
import ConfirmDialog from './ConfirmDialog';

/** How many keys the confirmation lists before "and N more". */
const LISTED = 5;

/**
 * BatchDeleteBar acts on the documents selected in the list: it says how
 * many are selected and deletes them in one /v1/delete-batch request after a
 * confirmation naming the collection and the first keys. `onDeleted`
 * receives one line summing up the server's counts (deleted, notFound —
 * not_found from a server before 2.19.0 — and failed); the list reloads, so
 * the caller shows it.
 */
export default function BatchDeleteBar({ collection, documents, onClear, onDeleted }) {
  const [confirming, setConfirming] = useState(false);
  const { busy, error, run } = useAction();
  const count = documents.length;

  const remove = () => run(async () => {
    const result = await mddbClient.deleteBatch({ collection, documents });
    setConfirming(false);
    const parts = [`Deleted ${result.deleted || 0}`];
    const notFound = result.notFound ?? result.not_found;
    if (notFound) parts.push(`${notFound} already gone`);
    if (result.failed) parts.push(`${result.failed} failed${result.errors?.length ? `: ${result.errors.join('; ')}` : ''}`);
    onDeleted(`${parts.join(', ')}.`);
  });

  const listed = documents.slice(0, LISTED).map((d) => `${d.key} (${d.lang})`).join(', ');
  const more = count > LISTED ? ` and ${count - LISTED} more` : '';

  return (
    <div className="mt-2 space-y-1">
      {count > 0 && (
        <div className="flex items-center gap-2 rounded bg-blue-50 px-2 py-1.5 text-sm text-blue-950">
          <span>{count} selected</span>
          <button
            type="button"
            onClick={() => setConfirming(true)}
            className="inline-flex items-center gap-1 px-2 py-1 text-xs text-white bg-red-700 rounded hover:bg-red-800"
          >
            <Trash2 className="w-3.5 h-3.5" aria-hidden="true" /> Delete selected
          </button>
          <button type="button" onClick={onClear} className="text-xs underline">Clear selection</button>
        </div>
      )}
      <ApiErrorNotice error={error} collection={collection} />
      {confirming && (
        <ConfirmDialog
          title={`Delete ${count} documents`}
          message={<>Delete {count} documents from <strong>{collection}</strong>: {listed}{more}? This cannot be undone.</>}
          busy={busy}
          onConfirm={remove}
          onCancel={() => setConfirming(false)}
        />
      )}
    </div>
  );
}
