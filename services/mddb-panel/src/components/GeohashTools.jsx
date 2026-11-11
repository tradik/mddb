import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';

/**
 * GeohashTools converts between coordinates and geohashes on the server
 * (/v1/geo-encode, /v1/geo-decode) — handy for checking a document's
 * geo_hash metadata. Encoding starts from the map's current point.
 */
export default function GeohashTools({ lat, lng }) {
  const [precision, setPrecision] = useState('8');
  const [geohash, setGeohash] = useState('');
  const [answer, setAnswer] = useState(null);
  const { busy, error, run } = useAction();

  const encode = () => run(async () => {
    const r = await mddbClient.geoEncode({ lat, lng, precision: Number(precision) });
    setGeohash(r.geohash);
    setAnswer(`${lat.toFixed(6)}, ${lng.toFixed(6)} → ${r.geohash} (precision ${r.precision})`);
  });

  const decode = () => run(async () => {
    const r = await mddbClient.geoDecode({ geohash: geohash.trim() });
    setAnswer(`${geohash.trim()} → ${r.lat.toFixed(6)}, ${r.lng.toFixed(6)}; cell ${r.minLat.toFixed(5)}–${r.maxLat.toFixed(5)} lat, ${r.minLng.toFixed(5)}–${r.maxLng.toFixed(5)} lng`);
  });

  return (
    <div className="space-y-2 text-sm">
      <div className="flex items-end gap-2">
        <label className="text-xs text-gray-800">
          <span className="block mb-0.5">Precision (1–12)</span>
          <input type="number" min="1" max="12" value={precision} onChange={(e) => setPrecision(e.target.value)} className="w-20 border border-gray-300 rounded px-2 py-1" />
        </label>
        <button type="button" onClick={encode} disabled={busy} className="px-3 py-1.5 bg-gray-100 text-gray-900 rounded hover:bg-gray-200 disabled:opacity-50">Encode map point</button>
      </div>
      <div className="flex items-end gap-2">
        <label className="flex-1 text-xs text-gray-800">
          <span className="block mb-0.5">Geohash</span>
          <input value={geohash} onChange={(e) => setGeohash(e.target.value)} className="w-full border border-gray-300 rounded px-2 py-1 font-mono" />
        </label>
        <button type="button" onClick={decode} disabled={busy || !geohash.trim()} className="px-3 py-1.5 bg-gray-100 text-gray-900 rounded hover:bg-gray-200 disabled:opacity-50">Decode</button>
      </div>
      <ApiErrorNotice error={error} />
      {answer && <p role="status" className="text-xs font-mono text-gray-900 break-all">{answer}</p>}
    </div>
  );
}
