import { useEffect, useRef, useState } from 'react';
import { HardDrive } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import {
  formatBytes, formatDuration, categoryRows, categorySummary, sortedCollections, sortedBuckets,
} from '../lib/storage-format';

// GET /v1/storage (2.18.2): where the bytes of the database file are, per
// bucket and per collection, and the free space a compaction gives back. The
// full breakdown reads every key, so it is measured on demand and can be
// cancelled; one collection reads only its own keys.

/** Bar colours per category; each meets 3:1 against white (WCAG 2.2 1.4.11). */
const CATEGORY_COLOURS = {
  content: 'bg-blue-700',
  metadata: 'bg-teal-700',
  fullText: 'bg-amber-700',
  vectors: 'bg-purple-700',
  other: 'bg-gray-600',
};

/** One collection's size, its category bar and the bar's text alternative. */
function CollectionRow({ name, entry }) {
  const rows = categoryRows(entry || {});
  const summary = categorySummary(rows);
  return (
    <li className="py-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <span className="font-medium text-gray-900">{name}</span>
        <span className="text-sm text-gray-700">
          {formatBytes(entry?.totalBytes)}
          {entry?.fileBytes > 0 && ` · own file ${formatBytes(entry.fileBytes)}`}
        </span>
      </div>
      <div className="mt-1 flex h-2 w-full overflow-hidden rounded bg-gray-200" role="img" aria-label={`${name}: ${summary}`}>
        {rows.map((r) => (
          <div key={r.key} className={CATEGORY_COLOURS[r.key]} style={{ width: `${r.percent}%` }} />
        ))}
      </div>
      <p className="mt-1 text-xs text-gray-600">{summary}</p>
    </li>
  );
}

export default function StorageCard() {
  const { stats } = useStore();
  const collections = (stats?.collections || []).map((c) => c.name);
  const [collection, setCollection] = useState('');
  const [report, setReport] = useState(null);
  const [error, setError] = useState(null);
  const [measuring, setMeasuring] = useState(false);
  const controller = useRef(null);

  // Abort a measurement still running when the card goes away.
  useEffect(() => () => controller.current?.abort(), []);

  const measure = async () => {
    const abort = new AbortController();
    controller.current = abort;
    setMeasuring(true);
    setError(null);
    try {
      setReport(await mddbClient.getStorage({ collection: collection || undefined, signal: abort.signal }));
    } catch (err) {
      setError(err.name === 'AbortError' ? 'Measurement cancelled.' : err.message);
    } finally {
      controller.current = null;
      setMeasuring(false);
    }
  };

  const cancel = () => controller.current?.abort();
  const buckets = sortedBuckets(report?.buckets);

  return (
    <div className="bg-white rounded-lg shadow p-6">
      <h2 className="text-lg font-semibold text-gray-900 mb-1 flex items-center gap-2">
        <HardDrive className="w-5 h-5 text-blue-700" aria-hidden="true" />
        Storage
      </h2>
      <p className="text-sm text-gray-600 mb-4">
        Bytes per collection and bucket. The full breakdown reads every key and can take minutes on a
        large file; one collection reads only its own keys.
      </p>

      <div className="flex flex-wrap items-end gap-3 mb-4">
        <div>
          <label htmlFor="storage-collection" className="block text-xs font-medium text-gray-700 mb-1">Collection</label>
          <select
            id="storage-collection"
            value={collection}
            onChange={(e) => setCollection(e.target.value)}
            disabled={measuring}
            className="px-2 py-1.5 border border-gray-300 rounded text-sm"
          >
            <option value="">All collections</option>
            {collections.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
        </div>
        <button
          type="button"
          onClick={measure}
          disabled={measuring}
          className="px-4 py-2 bg-blue-700 text-white rounded hover:bg-blue-800 disabled:opacity-50 text-sm"
        >
          {measuring ? 'Measuring…' : 'Measure'}
        </button>
        {measuring && (
          <button type="button" onClick={cancel} className="px-4 py-2 bg-gray-100 text-gray-800 rounded hover:bg-gray-200 text-sm">
            Cancel
          </button>
        )}
      </div>

      {error && <p role="alert" className="mb-3 text-sm text-red-700">{error}</p>}

      {report && (
        <div>
          <dl className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4 text-sm">
            <div><dt className="text-gray-600">File size</dt><dd className="font-medium text-gray-900">{formatBytes(report.fileBytes)}</dd></div>
            <div><dt className="text-gray-600">Reclaimable by compaction</dt><dd className="font-medium text-gray-900">{formatBytes(report.freeBytes)}</dd></div>
            <div><dt className="text-gray-600">Free pages</dt><dd className="font-medium text-gray-900">{(report.freePages || 0).toLocaleString()}</dd></div>
            <div><dt className="text-gray-600">Measured in</dt><dd className="font-medium text-gray-900">{formatDuration(report.elapsedMs)}</dd></div>
          </dl>

          <h3 className="text-sm font-semibold text-gray-900">Collections</h3>
          {sortedCollections(report.collections).length === 0 ? (
            <p className="text-sm text-gray-600 py-2">No collections reported.</p>
          ) : (
            <ul className="divide-y divide-gray-200">
              {sortedCollections(report.collections).map(([name, entry]) => (
                <CollectionRow key={name} name={name} entry={entry} />
              ))}
            </ul>
          )}

          {buckets.length > 0 && (
            <div className="mt-4 overflow-x-auto">
              <table className="min-w-full text-sm">
                <caption className="text-left text-sm font-semibold text-gray-900 mb-1">Buckets</caption>
                <thead>
                  <tr className="text-left text-gray-700">
                    <th scope="col" className="py-1 pr-4">Bucket</th>
                    <th scope="col" className="py-1 pr-4">In use</th>
                    <th scope="col" className="py-1">Keys</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {buckets.map(([name, b]) => (
                    <tr key={name}>
                      <td className="py-1 pr-4 font-mono text-gray-900">{name}</td>
                      <td className="py-1 pr-4 text-gray-800">{formatBytes(b?.inUseBytes)}</td>
                      <td className="py-1 text-gray-800">{(b?.keys || 0).toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
