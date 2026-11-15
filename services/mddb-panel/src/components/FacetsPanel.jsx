import { useEffect, useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';

/**
 * FacetsPanel counts a collection's documents per value of chosen metadata
 * keys (/v1/aggregate), within the metadata filters active in the list. The
 * keys come from /v1/meta-keys; each key's counts are an accessible table.
 */
export default function FacetsPanel({ collection, filters }) {
  const [keys, setKeys] = useState([]);
  const [chosen, setChosen] = useState([]);
  const [orderBy, setOrderBy] = useState('count');
  const [result, setResult] = useState(null);
  const [keysError, setKeysError] = useState(null);
  const { busy, error, run } = useAction();

  useEffect(() => {
    mddbClient.getMetaKeys(collection)
      .then((data) => setKeys(Object.keys(data?.meta || {}).sort((a, b) => a.localeCompare(b))))
      .catch(setKeysError);
  }, [collection]);

  const toggle = (key) => setChosen((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]));

  const count = () => run(async () => setResult(await mddbClient.aggregate({ collection, fields: chosen, filterMeta: filters, orderBy })));

  return (
    <section aria-label="Facets" className="border border-line rounded-lg p-3 mt-2 space-y-2 bg-surface-muted">
      <ApiErrorNotice error={keysError || error} collection={collection} />
      {!keysError && keys.length === 0 && <p className="text-xs text-fg-muted">This collection has no metadata keys to count.</p>}
      {keys.length > 0 && (
        <>
          <fieldset className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-fg max-h-24 overflow-y-auto">
            <legend className="text-xs font-semibold text-fg-soft mb-1">Metadata keys to count</legend>
            {keys.map((key) => (
              <label key={key} className="inline-flex items-center gap-1">
                <input type="checkbox" checked={chosen.includes(key)} onChange={() => toggle(key)} /> {key}
              </label>
            ))}
          </fieldset>
          <div className="flex items-end gap-2">
            <label className="text-xs text-fg-soft">
              <span className="block mb-0.5">Order</span>
              <select value={orderBy} onChange={(e) => setOrderBy(e.target.value)} className="px-2 py-1 border border-line-strong rounded text-xs">
                <option value="count">Most documents first</option>
                <option value="value">By value</option>
              </select>
            </label>
            <button type="button" onClick={count} disabled={busy || chosen.length === 0} className="px-3 py-1 text-xs bg-blue-700 text-white rounded hover:bg-blue-800 disabled:opacity-50">
              Count
            </button>
          </div>
        </>
      )}
      {result && (
        <div role="status" className="space-y-2">
          <p className="text-xs text-fg-soft">{result.totalDocs} documents match the filters.</p>
          {Object.entries(result.facets || {}).map(([field, buckets]) => (
            <table key={field} className="w-full text-xs bg-surface">
              <caption className="text-left font-semibold text-fg">{field}</caption>
              <thead>
                <tr className="text-left text-fg-soft">
                  <th scope="col" className="px-2 py-1">Value</th>
                  <th scope="col" className="px-2 py-1 text-right">Documents</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line-soft">
                {buckets.map((b) => (
                  <tr key={b.value}>
                    <td className="px-2 py-1 text-fg break-all">{b.value}</td>
                    <td className="px-2 py-1 text-right text-fg">{b.count}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ))}
        </div>
      )}
    </section>
  );
}
