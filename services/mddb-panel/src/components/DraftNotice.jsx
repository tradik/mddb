import { useState } from 'react';
import { readDraft, removeDraft, draftDiffers } from '../lib/drafts';

/**
 * DraftNotice (FE-019): in the document viewer, says that the document has
 * an unsaved draft (left by a reload, an expired session or a closed
 * editor) and offers to open it in the editor — which then asks to restore
 * or discard it — or to discard it here. Nothing shows without a draft, or
 * when the draft equals the stored document.
 */
export default function DraftNotice({ document, onOpen }) {
  // The draft is read from storage on each render; this state only records
  // which document's draft was discarded here, so the notice goes away.
  const [discardedFor, setDiscardedFor] = useState(null);
  const documentId = `${document.collection}|${document.key}|${document.lang}`;
  const draft = discardedFor === documentId ? null : readDraft(document);
  if (!draft || !draftDiffers(draft, document)) return null;

  const discard = () => {
    removeDraft(document);
    setDiscardedFor(documentId);
  };

  return (
    <output className="flex flex-wrap items-center gap-3 border-b border-warning-line bg-warning-soft px-4 py-2 text-sm text-warning-strong">
      <span className="flex-1 min-w-0">
        This document has an unsaved draft from {new Date(draft.savedAt).toLocaleString()}.
      </span>
      <button type="button" onClick={onOpen} className="rounded bg-amber-800 px-3 py-1 text-white hover:bg-amber-900">
        Open in editor
      </button>
      <button type="button" onClick={discard} className="rounded px-3 py-1 underline hover:bg-warning-muted">
        Discard draft
      </button>
    </output>
  );
}
