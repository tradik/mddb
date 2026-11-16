import { VECTOR_ALGORITHMS } from '../lib/search-options';
import { QUOTA_FIELDS } from '../lib/collection-config';

// Collection settings added in 2.18.9 and 2.18.10, edited in the collection
// settings dialog: the retrieval profile's vector index (SRCH-013), summary
// mode and weight (RAG-009), result cap (#368) and model reranking (RAG-011,
// 2.19.8), meta keys kept out of full-text search (#367), and the
// collection's quota (GO-043).

const input = 'w-full px-2 py-1.5 border border-line-strong rounded text-sm focus:outline-none focus:ring-2 focus:ring-focus';
const small = 'block text-xs text-fg-subtle mb-0.5';

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
          <p className="text-xs text-fg-subtle mt-0.5">Lowers MDDB_SEARCH_MAX_LIMIT for this collection; never raises it.</p>
        </div>
        <div>
          <label htmlFor="cc-summary-mode" className={small}>Document summaries</label>
          <select id="cc-summary-mode" value={value.summaryMode} onChange={set('summaryMode')} className={input}>
            <option value="">MDDB default (off)</option>
            <option value="off">Off</option>
            <option value="blend">Blend: best of chunk and summary</option>
            <option value="expand">Expand: also return the summary&apos;s best chunks</option>
          </select>
          <p className="text-xs text-fg-subtle mt-0.5">Uses each document&apos;s meta.summary, written by its client.</p>
        </div>
        <div>
          <label htmlFor="cc-summary-weight" className={small}>Summary weight</label>
          <input id="cc-summary-weight" type="number" min="0" step="0.1" value={value.summaryWeight} onChange={set('summaryWeight')} placeholder="2.0" className={input} />
        </div>
      </div>

      <fieldset>
        <legend className="block text-sm font-medium text-fg-muted mb-1">Model reranking</legend>
        <p className="text-xs text-fg-subtle mb-2">
          A reranking model orders each search&apos;s best candidates by reading the query and each passage together.
          Needs a reranking provider on the server (MDDB_RERANK_* or a stored rerank configuration). A search&apos;s
          own rerank block wins; if the model times out or fails, the search keeps its own order.
        </p>
        <div className="grid grid-cols-3 gap-3">
          <div>
            <label htmlFor="cc-rerank-enabled" className={small}>Default</label>
            <select id="cc-rerank-enabled" value={value.rerankEnabled} onChange={set('rerankEnabled')} className={input}>
              <option value="">MDDB default (off)</option>
              <option value="on">On for every search</option>
              <option value="off">Off</option>
            </select>
          </div>
          <div>
            <label htmlFor="cc-rerank-candidates" className={small}>Candidates</label>
            <input id="cc-rerank-candidates" type="number" min="0" max="200" value={value.rerankCandidates} onChange={set('rerankCandidates')} placeholder="50" className={input} />
          </div>
          <div>
            <label htmlFor="cc-rerank-top-n" className={small}>Keep (topN)</label>
            <input id="cc-rerank-top-n" type="number" min="0" value={value.rerankTopN} onChange={set('rerankTopN')} placeholder="topK" className={input} />
          </div>
        </div>
      </fieldset>

      <div>
        <label htmlFor="cc-exclude-meta" className="block text-sm font-medium text-fg-muted mb-1">Meta keys kept out of full-text search</label>
        <input
          id="cc-exclude-meta"
          value={value.excludeMetaKeys}
          onChange={set('excludeMetaKeys')}
          placeholder="e.g. url, license, sha"
          className={input}
        />
        <p className="text-xs text-fg-subtle mt-1">
          Comma-separated. Keys used only as filters: they get no postings, and filters and facets still work on them.
          Run an FTS reindex after changing the list.
        </p>
      </div>

      <fieldset>
        <legend className="block text-sm font-medium text-fg-muted mb-1">Quota</legend>
        <p className="text-xs text-fg-subtle mb-2">
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
