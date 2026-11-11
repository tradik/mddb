import { useState } from 'react';
import { CopyX, Search } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { useAction } from '../lib/use-action';
import { duplicateGroups } from '../lib/duplicates-format';
import ApiErrorNotice from './ApiErrorNotice';
import CollectionPicker from './CollectionPicker';
import DuplicateGroups from './DuplicateGroups';

const MODES = [
  { value: 'both', label: 'Identical and similar' },
  { value: 'exact', label: 'Identical content only' },
  { value: 'similar', label: 'Similar meaning (embeddings)' },
  { value: 'minhash', label: 'Overlapping text (forked pages)' },
];
const METRICS = ['cosine', 'dot_product', 'euclidean'];

/**
 * DuplicatesPanel is the Duplicates page: it scans a collection with
 * /v1/find-duplicates — identical content hashes, close embeddings, or
 * overlapping text (minhash) — and lists the groups, each document openable
 * and deletable.
 */
export default function DuplicatesPanel() {
  const currentCollection = useStore((s) => s.currentCollection);
  const firstCollection = useStore((s) => s.stats?.collections?.[0]?.name);
  const [collection, setCollection] = useState(currentCollection || firstCollection || '');
  const [mode, setMode] = useState('both');
  const [threshold, setThreshold] = useState('0.9');
  const [maxDocs, setMaxDocs] = useState('5000');
  const [distanceMetric, setDistanceMetric] = useState('cosine');
  const [result, setResult] = useState(null);
  const { busy, error, run } = useAction();

  const scan = (e) => {
    e.preventDefault();
    run(async () => setResult({
      collection,
      answer: await mddbClient.findDuplicates({ collection, mode, threshold: Number(threshold), maxDocs: Number(maxDocs), distanceMetric }),
      removed: [],
    }));
  };

  const groups = duplicateGroups(result?.answer)
    .map((g) => ({ ...g, documents: g.documents.filter((d) => !result.removed.includes(d.docId)) }))
    .filter((g) => g.documents.length > 0);
  const removed = (docId) => setResult((prev) => ({ ...prev, removed: [...prev.removed, docId] }));

  const select = (label, value, setValue, options) => (
    <label className="text-sm text-gray-800">
      <span className="block mb-1">{label}</span>
      <select value={value} onChange={(e) => setValue(e.target.value)} className="px-2 py-2 border border-gray-300 rounded-lg">
        {options.map((o) => <option key={o.value ?? o} value={o.value ?? o}>{o.label ?? o}</option>)}
      </select>
    </label>
  );

  return (
    <div className="h-full overflow-y-auto p-6 bg-gray-50 space-y-4">
      <div>
        <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
          <CopyX className="w-5 h-5" aria-hidden="true" /> Duplicates
        </h2>
        <p className="text-sm text-gray-700">Find documents stored twice, near-copies and forked pages in one collection.</p>
      </div>
      <form onSubmit={scan} className="bg-white rounded-lg shadow p-4 flex flex-wrap gap-3 items-end">
        <div className="min-w-[10rem]"><CollectionPicker value={collection} onChange={setCollection} /></div>
        {select('Mode', mode, setMode, MODES)}
        <label className="text-sm text-gray-800">
          <span className="block mb-1">Threshold (0–1)</span>
          <input type="number" min="0" max="1" step="0.01" value={threshold} onChange={(e) => setThreshold(e.target.value)} className="w-24 px-3 py-2 border border-gray-300 rounded-lg" />
        </label>
        <label className="text-sm text-gray-800">
          <span className="block mb-1">Max documents</span>
          <input type="number" min="1" value={maxDocs} onChange={(e) => setMaxDocs(e.target.value)} className="w-28 px-3 py-2 border border-gray-300 rounded-lg" />
        </label>
        {select('Distance', distanceMetric, setDistanceMetric, METRICS)}
        <button type="submit" disabled={busy || !collection} className="inline-flex items-center gap-1 px-4 py-2 bg-blue-700 text-white rounded-lg hover:bg-blue-800 disabled:opacity-50 text-sm">
          <Search className="w-4 h-4" aria-hidden="true" /> {busy ? 'Scanning…' : 'Find duplicates'}
        </button>
      </form>
      <ApiErrorNotice error={error} collection={collection} />
      {result && (
        <>
          <p role="status" className="text-sm text-gray-800">
            Scanned {result.answer.totalDocuments} documents ({result.answer.totalEmbedded} with embeddings): {groups.length} groups.
          </p>
          <DuplicateGroups collection={result.collection} groups={groups} onDeleted={removed} />
        </>
      )}
    </div>
  );
}
