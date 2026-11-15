import { AlertTriangle } from 'lucide-react';

/**
 * RevisionConflictNotice (FE-019): a save was refused with 409 because the
 * document changed on the server after this edit started. The edit is not
 * lost (it stays in the editor and its draft); the user may overwrite the
 * newer version on purpose — a save conditional on the revision just
 * reported — or close the editor and compare first.
 */
export default function RevisionConflictNotice({ expected, current, busy, onOverwrite }) {
  return (
    <div role="alert" className="flex items-start space-x-3 rounded-lg border border-amber-300 bg-amber-50 p-4">
      <AlertTriangle className="mt-0.5 h-5 w-5 flex-shrink-0 text-amber-800" aria-hidden="true" />
      <div className="text-sm text-amber-900">
        <h4 className="font-medium">Not saved: the document changed on the server</h4>
        <p className="mt-1">
          {current
            ? `Someone saved revision ${current} after you started editing revision ${expected}.`
            : `The document was deleted after you started editing revision ${expected}.`}{' '}
          Your changes are still here and in the draft.
        </p>
        <button
          type="button"
          onClick={onOverwrite}
          disabled={busy}
          className="mt-2 rounded bg-amber-800 px-3 py-1.5 text-white hover:bg-amber-900 disabled:opacity-50"
        >
          Save my version anyway
        </button>
      </div>
    </div>
  );
}
