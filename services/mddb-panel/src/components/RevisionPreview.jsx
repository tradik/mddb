import { useState } from 'react';
import { RotateCcw, Loader2 } from 'lucide-react';
import { format } from 'date-fns';
import MarkdownView from './MarkdownView';
import LineDiff from './LineDiff';
import MetaDiff from './MetaDiff';
import { metaText } from '../lib/meta-diff';

/**
 * RevisionPreview is the right-hand pane of the revision history: one
 * revision, its Restore button and two views of it — "Changes" (FE-013: a
 * line diff of the content and the metadata keys, from this revision to the
 * current version, so a restore is no longer blind) and "Preview" (the
 * revision rendered as the viewer renders a document).
 */
export default function RevisionPreview({ revision, current, restoring, onRestore }) {
  const [view, setView] = useState('changes');
  const tab = (name, label) => (
    <button
      type="button"
      aria-pressed={view === name}
      onClick={() => setView(name)}
      className={`px-3 py-1.5 text-sm rounded-md ${view === name ? 'bg-white text-gray-900 shadow-sm font-medium' : 'text-gray-700 hover:text-gray-900'}`}
    >
      {label}
    </button>
  );

  return (
    <div className="p-6">
      <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
        <div>
          <h3 className="text-lg font-semibold text-gray-900">
            {format(new Date(revision.timestamp * 1000), 'PPpp')}
          </h3>
          <p className="text-sm text-gray-700 mt-1">
            Content length: {revision.contentMd?.length || 0} characters
          </p>
        </div>
        <button
          onClick={() => onRestore(revision.timestamp)}
          disabled={restoring}
          className="flex items-center space-x-2 px-4 py-2 bg-amber-700 text-white rounded-lg hover:bg-amber-800 transition-colors disabled:opacity-50"
        >
          {restoring ? <Loader2 className="w-4 h-4 animate-spin" /> : <RotateCcw className="w-4 h-4" />}
          <span>{restoring ? 'Restoring...' : 'Restore this version'}</span>
        </button>
      </div>

      <div role="group" aria-label="Revision view" className="inline-flex gap-1 p-1 mb-4 bg-gray-100 rounded-lg">
        {tab('changes', 'Changes')}
        {tab('preview', 'Preview')}
      </div>

      {view === 'changes' ? (
        <div className="space-y-4">
          <MetaDiff before={revision.meta} after={current.meta} label="Metadata, this revision → current" />
          <LineDiff before={revision.contentMd || ''} after={current.contentMd || ''} label="Content, this revision → current" />
        </div>
      ) : (
        <>
          {revision.meta && Object.keys(revision.meta).length > 0 && (
            <div className="mb-4 p-3 bg-gray-50 rounded-lg">
              <h4 className="text-xs font-semibold text-gray-700 uppercase mb-2">Metadata</h4>
              <div className="space-y-1">
                {Object.entries(revision.meta).map(([k, v]) => (
                  <div key={k} className="text-sm">
                    <span className="font-medium text-gray-800">{k}:</span>{' '}
                    <span className="text-gray-700">{metaText(v)}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
          <div className="border border-gray-200 rounded-lg p-4 bg-white">
            <MarkdownView>{revision.contentMd || '(empty)'}</MarkdownView>
          </div>
        </>
      )}
    </div>
  );
}
