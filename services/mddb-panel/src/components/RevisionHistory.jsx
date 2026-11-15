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
    <div className="fixed inset-0 bg-white z-50 flex flex-col">
      {/* Header */}
      <div className="border-b border-gray-200 p-4 flex-shrink-0">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-xl font-semibold text-gray-900">
              Revision History
            </h2>
            <p className="text-sm text-gray-600 mt-1">
              {document.collection} / {document.key} ({document.lang})
            </p>
          </div>
          <button
            onClick={onClose}
            aria-label="Close revision history"
            className="p-2 text-gray-600 hover:text-gray-800 rounded-lg hover:bg-gray-100"
          >
            <X className="w-5 h-5" />
          </button>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-hidden flex">
        {/* Revision List */}
        <div className="w-80 border-r border-gray-200 overflow-y-auto flex-shrink-0">
          {loading && (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="w-6 h-6 text-blue-600 animate-spin" />
              <span className="ml-2 text-sm text-gray-600">Loading revisions...</span>
            </div>
          )}

          {error && (
            <div className="m-4 bg-red-50 border border-red-200 rounded-lg p-3 flex items-start space-x-2">
              <AlertCircle className="w-4 h-4 text-red-600 flex-shrink-0 mt-0.5" />
              <p className="text-sm text-red-700">{error}</p>
            </div>
          )}

          {restoreSuccess && (
            <div className="m-4 bg-green-50 border border-green-200 rounded-lg p-3 flex items-center space-x-2">
              <Check className="w-4 h-4 text-green-600" />
              <p className="text-sm font-medium text-green-900">Revision restored!</p>
            </div>
          )}

          {!loading && revisions.length === 0 && !error && (
            <div className="flex items-center justify-center py-12">
              <p className="text-sm text-gray-600">No revisions found</p>
            </div>
          )}

          {revisions.map((rev, idx) => (
            <button
              key={rev.timestamp}
              onClick={() => setSelectedRevision(rev)}
              className={`w-full text-left p-4 border-b border-gray-100 hover:bg-gray-50 transition-colors ${
                selectedRevision?.timestamp === rev.timestamp ? 'bg-blue-50 border-l-2 border-l-blue-600' : ''
              }`}
            >
              <div className="flex items-center justify-between mb-1">
                <span className="text-xs font-medium text-gray-600">
                  {idx === 0 ? 'Latest' : `Rev #${revisions.length - idx}`}
                </span>
                <Clock className="w-3 h-3 text-gray-400" />
              </div>
              <p className="text-sm font-medium text-gray-900">
                {format(new Date(rev.timestamp * 1000), 'PPpp')}
              </p>
              {rev.meta && Object.keys(rev.meta).length > 0 && (
                <div className="mt-1 flex flex-wrap gap-1">
                  {Object.entries(rev.meta).slice(0, 3).map(([k, v]) => (
                    <span key={k} className="text-xs bg-gray-100 text-gray-600 px-1.5 py-0.5 rounded">
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
                <Eye className="w-12 h-12 text-gray-300 mx-auto mb-3" />
                <p className="text-gray-600">Select a revision to preview</p>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
