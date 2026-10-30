import { VECTOR_ALGORITHMS } from '../lib/search-options';
import { QUOTA_FIELDS } from '../lib/collection-config';

// Collection settings added in 2.18.9 and 2.18.10, edited in the collection
// settings dialog: the retrieval profile's vector index (SRCH-013), summary
// mode and weight (RAG-009) and result cap (#368), meta keys kept out of
// full-text search (#367), and the collection's quota (GO-043).

const input = 'w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500';
const small = 'block text-xs text-gray-600 mb-0.5';

export default function CollectionAdvancedSettings({ value, onChange }) {
  const set = (key) => (e) => onChange({ ...value, [key]: e.target.value });
  const setQuota = (key) => (e) => onChange({ ...value, quota: { ...value.quota, [key]: e.target.value } });

  return (
    <>
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label htmlFor="cc-vector-algorithm" className={small}>Vector index</label>
          <select id="cc-vector-algorithm" value={value.vectorAlgorithm} onChange={set('vectorAlgorithm')} className={input}>
            <option value="">MDDB default (flat)</option>
            {VECTOR_ALGORITHMS.map((a) => <option key={a.value} value={a.value}>{a.label}</option>)}
          </select>
        </div>
        <div>
          <label htmlFor="cc-max-top-k" className={small}>Result cap (maxTopK)</label>
          <input id="cc-max-top-k" type="number" min="0" value={value.maxTopK} onChange={set('maxTopK')} placeholder="server cap" className={input} />
          <p className="text-xs text-gray-600 mt-0.5">Lowers MDDB_SEARCH_MAX_LIMIT for this collection; never raises it.</p>
        </div>
        <div>
          <label htmlFor="cc-summary-mode" className={small}>Document summaries</label>
          <select id="cc-summary-mode" value={value.summaryMode} onChange={set('summaryMode')} className={input}>
            <option value="">MDDB default (off)</option>
            <option value="off">Off</option>
            <option value="blend">Blend: best of chunk and summary</option>
            <option value="expand">Expand: also return the summary&apos;s best chunks</option>
          </select>
          <p className="text-xs text-gray-600 mt-0.5">Uses each document&apos;s meta.summary, written by its client.</p>
        </div>
        <div>
          <label htmlFor="cc-summary-weight" className={small}>Summary weight</label>
          <input id="cc-summary-weight" type="number" min="0" step="0.1" value={value.summaryWeight} onChange={set('summaryWeight')} placeholder="2.0" className={input} />
        </div>
      </div>

      <div>
        <label htmlFor="cc-exclude-meta" className="block text-sm font-medium text-gray-700 mb-1">Meta keys kept out of full-text search</label>
        <input
          id="cc-exclude-meta"
          value={value.excludeMetaKeys}
          onChange={set('excludeMetaKeys')}
          placeholder="e.g. url, license, sha"
          className={input}
        />
        <p className="text-xs text-gray-600 mt-1">
          Comma-separated. Keys used only as filters: they get no postings, and filters and facets still work on them.
          Run an FTS reindex after changing the list.
        </p>
      </div>

      <fieldset>
        <legend className="block text-sm font-medium text-gray-700 mb-1">Quota</legend>
        <p className="text-xs text-gray-600 mb-2">
          Writes past a storage limit are refused (507); embeddings past a daily budget wait until midnight UTC.
          Leave empty for no limit. Only an admin on &quot;*&quot; can change a quota.
        </p>
        <div className="grid grid-cols-2 gap-3">
          {QUOTA_FIELDS.map(({ key, label }) => (
            <div key={key}>
              <label htmlFor={`cc-quota-${key}`} className={small}>{label}</label>
              <input id={`cc-quota-${key}`} type="number" min="0" value={value.quota[key]} onChange={setQuota(key)} placeholder="no limit" className={input} />
            </div>
          ))}
        </div>
      </fieldset>
    </>
  );
}
