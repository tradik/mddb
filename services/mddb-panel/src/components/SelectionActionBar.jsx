import { useState } from 'react';
import { Download, Trash2 } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { saveBlob } from '../lib/download';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';
import ConfirmDialog from './ConfirmDialog';

/** How many keys the confirmation lists before "and N more". */
const LISTED = 5;

/** The export formats offered, in the order their buttons appear. */
const EXPORT_FORMATS = ['zip', 'ndjson'];

const ACTION = 'inline-flex items-center gap-1 px-2 py-1 text-xs rounded disabled:opacity-60';

/**
 * SelectionActionBar acts on the documents selected in the list. It says
 * how many are selected and offers:
 *
 * - Export (FE-014): the selection as a ZIP of Markdown files or as NDJSON,
 *   one /v1/export request with `keys`; the file is named by the server's
 *   Content-Disposition (`<collection>.zip|.ndjson`). Offered on a read-only
 *   collection too — exporting reads.
 * - Delete (2.19.0): one /v1/delete-batch request after a confirmation
 *   naming the collection and the first keys. Hidden when `readOnly`.
 *   `onDeleted` receives one line summing up the server's counts (deleted,
 *   notFound — not_found from a server before 2.19.0 — and failed); the
 *   list reloads, so the caller shows it.
 */
export default function SelectionActionBar({ collection, documents, readOnly = false, onClear, onDeleted }) {
  const [confirming, setConfirming] = useState(false);
  const { busy, error, run } = useAction();
  const count = documents.length;

  const exportAs = (format) => run(async () => {
    const keys = documents.map(({ key, lang }) => ({ key, lang }));
    saveBlob(await mddbClient.export({ collection, format, keys }), `${collection}.${format}`);
  });

  const remove = () => run(async () => {
    const result = await mddbClient.deleteBatch({ collection, documents });
    setConfirming(false);
    const parts = [`Deleted ${result.deleted || 0}`];
    const notFound = result.notFound ?? result.not_found;
    if (notFound) parts.push(`${notFound} already gone`);
    if (result.failed) {
      const reasons = result.errors?.length ? `: ${result.errors.join('; ')}` : '';
      parts.push(`${result.failed} failed${reasons}`);
    }
    onDeleted(`${parts.join(', ')}.`);
  });

  const listed = documents.slice(0, LISTED).map((d) => `${d.key} (${d.lang})`).join(', ');
  const more = count > LISTED ? ` and ${count - LISTED} more` : '';

  return (
    <div className="mt-2 space-y-1">
      {count > 0 && (
        <div className="flex flex-wrap items-center gap-2 rounded bg-info-soft px-2 py-1.5 text-sm text-info-strong">
          <span>{count} selected</span>
          {EXPORT_FORMATS.map((format) => (
            <button
              key={format}
              type="button"
              onClick={() => exportAs(format)}
              disabled={busy}
              className={`${ACTION} text-fg bg-surface border border-line-strong hover:bg-surface-subtle`}
            >
              <Download className="w-3.5 h-3.5" aria-hidden="true" /> Export {format.toUpperCase()}
            </button>
          ))}
          {!readOnly && (
            <button
              type="button"
              onClick={() => setConfirming(true)}
              disabled={busy}
              className={`${ACTION} text-white bg-red-700 hover:bg-red-800`}
            >
              <Trash2 className="w-3.5 h-3.5" aria-hidden="true" /> Delete selected
            </button>
          )}
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
