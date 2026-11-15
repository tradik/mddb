import { useState } from 'react';
import LineDiff from './LineDiff';

/**
 * DraftRestoreBanner (FE-019): the editor found an unsaved draft of this
 * document. It says when the draft was saved, warns when the document has
 * changed on the server since the draft was started, shows the draft's
 * changes as a line diff on request, and lets the user restore or discard
 * it. Autosave waits until one of the two is chosen.
 */
export default function DraftRestoreBanner({ draft, document, onRestore, onDiscard }) {
  const [showDiff, setShowDiff] = useState(false);
  const moved = Boolean(draft.baseRevision && document.revision && draft.baseRevision !== document.revision);

  return (
    <div role="alert" className="rounded-lg border border-warning-line bg-warning-soft p-4 text-sm text-warning-strong">
      <p className="font-medium">
        You have an unsaved draft of this document from {new Date(draft.savedAt).toLocaleString()}.
      </p>
      {moved && (
        <p className="mt-1">
          The document has changed on the server since this draft was started
          (revision {draft.baseRevision} → {document.revision}). Saving a restored draft
          will report the conflict instead of overwriting that change.
        </p>
      )}
      <p className="mt-1">Autosave is paused until you restore or discard the draft.</p>
      <div className="mt-3 flex flex-wrap gap-2">
        <button type="button" onClick={onRestore} className="rounded bg-amber-800 px-3 py-1.5 text-white hover:bg-amber-900">
          Restore draft
        </button>
        <button type="button" onClick={onDiscard} className="rounded border border-amber-800 px-3 py-1.5 hover:bg-warning-muted">
          Discard draft
        </button>
        <button
          type="button"
          onClick={() => setShowDiff(!showDiff)}
          aria-expanded={showDiff}
          className="rounded px-3 py-1.5 underline hover:bg-warning-muted"
        >
          {showDiff ? 'Hide changes' : 'Show changes'}
        </button>
      </div>
      {showDiff && <LineDiff before={document.contentMd || ''} after={draft.contentMd} label="Draft against the document" />}
    </div>
  );
}
