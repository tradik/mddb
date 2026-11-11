import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import { parsePostcodeLines } from '../lib/postcode-lines';
import ApiErrorNotice from './ApiErrorNotice';

/**
 * GeoReindexForm rebuilds the geo indexes from the stored points
 * (/v1/geo-reindex) — one collection, or all when none is picked — and can
 * first load postcode CSVs (postcode,lat,lng; paths on the server) so
 * documents with geo_postcode get coordinates.
 */
export default function GeoReindexForm({ collection, onDone }) {
  const [postcodes, setPostcodes] = useState('');
  const [answer, setAnswer] = useState(null);
  const { busy, error, run } = useAction();

  const reindex = () => run(async () => {
    const r = await mddbClient.geoReindex({ collection, loadPostcodes: parsePostcodeLines(postcodes) });
    setAnswer(`Rebuilt ${r.collection || 'all collections'}: ${r.points} points in ${r.durationMs} ms.`);
    onDone();
  });

  return (
    <div className="space-y-2 text-sm">
      <label className="block text-xs text-gray-800">
        <span className="block mb-0.5">Postcode files to load first (optional, one &quot;country, path on the server&quot; per line)</span>
        <textarea value={postcodes} onChange={(e) => setPostcodes(e.target.value)} rows={2} placeholder="PL, /var/lib/mddb/postcodes/pl.csv" className="w-full border border-gray-300 rounded px-2 py-1 font-mono text-xs" />
      </label>
      <button type="button" onClick={reindex} disabled={busy} className="w-full bg-gray-100 text-gray-900 rounded px-3 py-1.5 hover:bg-gray-200 disabled:opacity-50">
        Rebuild geo index of {collection || 'all collections'}
      </button>
      <ApiErrorNotice error={error} collection={collection || '*'} />
      {answer && <p role="status" className="text-xs text-gray-900">{answer}</p>}
    </div>
  );
}
