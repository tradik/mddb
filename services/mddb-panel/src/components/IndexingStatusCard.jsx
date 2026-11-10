import { useCallback, useEffect, useState } from 'react';
import { Activity, RefreshCw } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { progressPercent } from '../lib/storage-format';

// GET /v1/indexing-status (2.18.8, GO-042): is what was just written
// searchable yet? A write returns once full text and metadata are indexed;
// embeddings follow from a background worker. While work is queued the card
// refreshes itself every few seconds.

/** How often the card refreshes while work is queued. */
export const REFRESH_MS = 5000;

/** One labelled figure. */
function Figure({ label, value }) {
  return (
    <div>
      <dt className="text-xs text-gray-600">{label}</dt>
      <dd className="text-sm font-medium text-gray-900">{value}</dd>
    </div>
  );
}

export default function IndexingStatusCard() {
  const { stats } = useStore();
  const collections = (stats?.collections || []).map((c) => c.name);
  const [collection, setCollection] = useState('');
  const [status, setStatus] = useState(null);
  const [error, setError] = useState(null);

  const load = useCallback(() => mddbClient
    .getIndexingStatus(collection || undefined)
    .then((data) => {
      setStatus(data);
      setError(null);
    })
    .catch((err) => setError(err.message)), [collection]);

  useEffect(() => {
    load();
  }, [load]);

  // Refresh while embeddings or metadata are still queued.
  const pending = status != null && status.indexed === false;
  useEffect(() => {
    if (!pending) return undefined;
    const timer = setInterval(load, REFRESH_MS);
    return () => clearInterval(timer);
  }, [pending, load]);

  const percent = progressPercent(status?.progress);

  return (
    <div className="bg-white rounded-lg shadow p-6">
      <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
        <h2 className="text-lg font-semibold text-gray-900 flex items-center gap-2">
          <Activity className="w-5 h-5 text-blue-700" aria-hidden="true" />
          Indexing status
        </h2>
        <div className="flex items-end gap-2">
          <label htmlFor="indexing-collection" className="sr-only">Collection</label>
          <select
            id="indexing-collection"
            value={collection}
            onChange={(e) => setCollection(e.target.value)}
            className="px-2 py-1.5 border border-gray-300 rounded text-sm"
          >
            <option value="">Whole server</option>
            {collections.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
          <button
            type="button"
            onClick={load}
            className="px-3 py-1.5 bg-gray-100 text-gray-800 rounded hover:bg-gray-200 text-sm flex items-center gap-1"
          >
            <RefreshCw className="w-4 h-4" aria-hidden="true" /> Refresh
          </button>
        </div>
      </div>

      {error && <p role="alert" className="text-sm text-red-700">{error}</p>}

      {status && (
        <div>
          <p className="text-sm text-gray-800 mb-2">
            {status.indexed
              ? 'Everything written is searchable.'
              : 'Embeddings are still queued: semantic search sees new documents once the queue drains.'}
            {status.provider && <span className="text-gray-600"> Provider: {status.provider}.</span>}
          </p>
          <div
            className="w-full bg-gray-200 rounded-full h-2"
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={percent}
            aria-label="Embedding progress"
          >
            <div className={`h-2 rounded-full ${status.indexed ? 'bg-green-700' : 'bg-blue-700'}`} style={{ width: `${percent}%` }} />
          </div>
          <p className="mt-1 text-xs text-gray-700">{percent}% embedded</p>

          <dl className="grid grid-cols-2 md:grid-cols-5 gap-4 mt-4">
            <Figure label="Pending embeddings" value={status.pendingEmbeddings ?? 0} />
            <Figure label="Failed embeddings" value={status.failedEmbeddings ?? 0} />
            <Figure label="Completed embeddings" value={status.completedEmbeddings ?? 0} />
            <Figure label="Pending metadata" value={status.pendingMetadata ?? 0} />
            <Figure
              label="Oldest pending"
              value={status.oldestPendingAt ? new Date(status.oldestPendingAt).toLocaleString() : '—'}
            />
          </dl>

          {status.images && (
            <p className="mt-3 text-sm text-gray-800">
              Images: {status.images.embedded ?? 0} of {status.images.documents ?? 0} embedded
              {status.images.model && ` with ${status.images.model}`}
              {status.images.note && <span className="block text-xs text-amber-800">{status.images.note}</span>}
            </p>
          )}

          {status.trainingCollections?.length > 0 && (
            <p className="mt-3 text-sm text-amber-800">
              Vector indexes still training: {status.trainingCollections.join(', ')} (flat and HNSW already answer).
            </p>
          )}
        </div>
      )}
    </div>
  );
}
