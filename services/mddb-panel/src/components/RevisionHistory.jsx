import { useState, useEffect, useCallback } from 'react';
import { X, Eye, Loader2, AlertCircle, Check, Clock } from 'lucide-react';
import { format } from 'date-fns';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import RevisionPreview from './RevisionPreview';

/**
 * RevisionHistory is the full-page list of a document's revisions. Picking
 * one opens RevisionPreview: what it changed against the current version
 * (FE-013), its rendering, and Restore.
 */
export default function RevisionHistory({ document, onClose }) {
  const { setCurrentDocument } = useStore();
  const [revisions, setRevisions] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [selectedRevision, setSelectedRevision] = useState(null);
  const [restoring, setRestoring] = useState(false);
  const [restoreSuccess, setRestoreSuccess] = useState(false);

  const loadRevisions = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await mddbClient.getRevisions({
        collection: document.collection,
        key: document.key,
        lang: document.lang,
      });
      setRevisions(data.revisions || []);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, [document]);

  useEffect(() => {
    loadRevisions();
  }, [loadRevisions]);

  const handleRestore = async (timestamp) => {
    setRestoring(true);
    setRestoreSuccess(false);
    try {
      const restored = await mddbClient.restoreRevision({
        collection: document.collection,
        key: document.key,
        lang: document.lang,
        timestamp,
      });
      setCurrentDocument({ ...restored, collection: document.collection });
      setRestoreSuccess(true);
      // Reload revisions to show the new state
      await loadRevisions();
      setTimeout(() => {
        setRestoreSuccess(false);
        onClose();
      }, 1500);
    } catch (err) {
      setError(err.message);
    } finally {
      setRestoring(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-surface z-50 flex flex-col">
      {/* Header */}
      <div className="border-b border-line p-4 flex-shrink-0">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-xl font-semibold text-fg">
              Revision History
            </h2>
            <p className="text-sm text-fg-subtle mt-1">
              {document.collection} / {document.key} ({document.lang})
            </p>
          </div>
          <button
            onClick={onClose}
            aria-label="Close revision history"
            className="p-2 text-fg-subtle hover:text-fg-soft rounded-lg hover:bg-surface-subtle"
          >
            <X className="w-5 h-5" />
          </button>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-hidden flex">
        {/* Revision List */}
        <div className="w-80 border-r border-line overflow-y-auto flex-shrink-0">
          {loading && (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="w-6 h-6 text-info animate-spin" />
              <span className="ml-2 text-sm text-fg-subtle">Loading revisions...</span>
            </div>
          )}

          {error && (
            <div className="m-4 bg-danger-soft border border-danger-line rounded-lg p-3 flex items-start space-x-2">
              <AlertCircle className="w-4 h-4 text-danger flex-shrink-0 mt-0.5" />
              <p className="text-sm text-danger">{error}</p>
            </div>
          )}

          {restoreSuccess && (
            <div className="m-4 bg-success-soft border border-success-line rounded-lg p-3 flex items-center space-x-2">
              <Check className="w-4 h-4 text-success" />
              <p className="text-sm font-medium text-success-strong">Revision restored!</p>
            </div>
          )}

          {!loading && revisions.length === 0 && !error && (
            <div className="flex items-center justify-center py-12">
              <p className="text-sm text-fg-subtle">No revisions found</p>
            </div>
          )}

          {revisions.map((rev, idx) => (
            <button
              key={rev.timestamp}
              onClick={() => setSelectedRevision(rev)}
              className={`w-full text-left p-4 border-b border-line-soft hover:bg-surface-muted transition-colors ${
                selectedRevision?.timestamp === rev.timestamp ? 'bg-info-soft border-l-2 border-l-blue-600' : ''
              }`}
            >
              <div className="flex items-center justify-between mb-1">
                <span className="text-xs font-medium text-fg-subtle">
                  {idx === 0 ? 'Latest' : `Rev #${revisions.length - idx}`}
                </span>
                <Clock className="w-3 h-3 text-fg-faint" />
              </div>
              <p className="text-sm font-medium text-fg">
                {format(new Date(rev.timestamp * 1000), 'PPpp')}
              </p>
              {rev.meta && Object.keys(rev.meta).length > 0 && (
                <div className="mt-1 flex flex-wrap gap-1">
                  {Object.entries(rev.meta).slice(0, 3).map(([k, v]) => (
                    <span key={k} className="text-xs bg-surface-subtle text-fg-subtle px-1.5 py-0.5 rounded">
                      {k}: {Array.isArray(v) ? v[0] : v}
                    </span>
                  ))}
                </div>
              )}
            </button>
          ))}
        </div>

        {/* Revision Preview */}
        <div className="flex-1 overflow-y-auto">
          {selectedRevision ? (
            <RevisionPreview
              revision={selectedRevision}
              current={document}
              restoring={restoring}
              onRestore={handleRestore}
            />
          ) : (
            <div className="flex items-center justify-center h-full">
              <div className="text-center">
                <Eye className="w-12 h-12 text-line-strong mx-auto mb-3" />
                <p className="text-fg-subtle">Select a revision to preview</p>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
