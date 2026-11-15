import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { useAction } from '../lib/use-action';
import { langOf, formatScore } from '../lib/duplicates-format';
import ApiErrorNotice from './ApiErrorNotice';
import ConfirmDialog from './ConfirmDialog';

const TYPE_LABEL = { exact: 'Identical content', similar: 'Similar meaning', minhash: 'Overlapping text' };

/**
 * DuplicateGroups lists groups of duplicate documents. Each document opens in
 * the document viewer beside the page, or is deleted after a confirmation
 * naming the collection and the key; a deleted document leaves its group.
 */
export default function DuplicateGroups({ collection, groups, onDeleted }) {
  const setCurrentDocument = useStore((s) => s.setCurrentDocument);
  const currentDocument = useStore((s) => s.currentDocument);
  const [deleting, setDeleting] = useState(null);
  const { busy, error, run } = useAction();

  const open = (doc) => run(async () => {
    const full = await mddbClient.getDocument({ collection, key: doc.key, lang: langOf(doc) });
    setCurrentDocument({ ...full, collection });
  });

  const remove = () => run(async () => {
    await mddbClient.deleteDocument({ collection, key: deleting.key, lang: langOf(deleting) });
    if (currentDocument?.key === deleting.key && currentDocument?.collection === collection) setCurrentDocument(null);
    onDeleted(deleting.docId);
    setDeleting(null);
  });

  return (
    <div className="space-y-3">
      <ApiErrorNotice error={error} collection={collection} />
      {groups.map((group) => (
        <section key={group.id} aria-label={`${TYPE_LABEL[group.type] || group.type}, group ${group.groupId}`} className="bg-surface rounded-lg shadow p-3">
          <h4 className="text-sm font-semibold text-fg mb-2">
            {TYPE_LABEL[group.type] || group.type} · group {group.groupId}
            {group.score !== undefined && <span className="ml-2 font-normal text-fg-muted">average {formatScore(group.score)}</span>}
          </h4>
          <ul className="divide-y divide-line-soft">
            {group.documents.map((doc) => (
              <li key={doc.docId} className="flex items-center gap-3 py-1.5 text-sm">
                <span className="flex-1 font-mono text-fg break-all">{doc.key} <span className="text-fg-muted">({langOf(doc)})</span></span>
                <span className="text-xs text-fg-muted">{formatScore(doc.score)}</span>
                <button type="button" onClick={() => open(doc)} className="text-info-strong underline">
                  Open <span className="sr-only">{doc.key}</span>
                </button>
                <button type="button" onClick={() => setDeleting(doc)} className="text-danger-strong underline">
                  Delete <span className="sr-only">{doc.key}</span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      ))}
      {deleting && (
        <ConfirmDialog
          title="Delete document"
          message={<>Delete <strong>{deleting.key}</strong> ({langOf(deleting)}) from <strong>{collection}</strong>? This cannot be undone.</>}
          busy={busy}
          onConfirm={remove}
          onCancel={() => setDeleting(null)}
        />
      )}
    </div>
  );
}
