import { useEffect, useRef, useState } from 'react';
import { Gauge, Trash2, Play, Square } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import {
  EVAL_PRESETS, parseKeys, parseGrades, formatGrades, clampK, bestConfig, formatScore,
} from '../lib/eval-format';

// SRCH-019 (2.18.8): measure retrieval on a collection's own questions. The
// stored set lives in the collection config (evalSet) but changes only through
// /v1/eval-queries; POST /v1/eval runs it under up to 8 search configurations
// and reports recall@k, nDCG@k, MRR, latency and tokens per configuration. It
// only reads, so a read-only server runs it too.

const inputClass = 'w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-primary-500';
const emptyForm = { id: '', query: '', expectedKeys: '', grades: '' };

/** The stored evaluation queries of a collection, with delete buttons. */
function QueryList({ queries, onDelete, busy }) {
  if (queries.length === 0) {
    return <p className="text-sm text-gray-600">No stored queries. Add some below, then run.</p>;
  }
  return (
    <ul className="divide-y divide-gray-200">
      {queries.map((q) => (
        <li key={q.id} className="py-2 flex items-start justify-between gap-3">
          <div className="min-w-0 text-sm">
            <div><span className="font-medium text-gray-900">{q.id}</span> <span className="text-gray-700">{q.query}</span></div>
            <div className="text-xs text-gray-600">
              expects {(q.expectedKeys || []).join(', ')}
              {Object.keys(q.grades || {}).length > 0 && ` · grades ${formatGrades(q.grades)}`}
            </div>
          </div>
          <button
            type="button"
            onClick={() => onDelete(q.id)}
            disabled={busy}
            aria-label={`Delete query ${q.id}`}
            className="shrink-0 rounded p-1.5 text-red-700 hover:bg-red-50 disabled:opacity-50"
          >
            <Trash2 className="h-4 w-4" />
          </button>
        </li>
      ))}
    </ul>
  );
}

/** One row per configuration; the best nDCG@k is highlighted. */
function ResultsTable({ run }) {
  const best = bestConfig(run.results);
  return (
    <div className="overflow-x-auto">
      <p className="text-sm text-gray-700 mb-2">{run.queries} queries, k = {run.k}</p>
      <table className="min-w-full text-sm">
        <thead>
          <tr className="text-left text-gray-700 border-b border-gray-200">
            <th className="py-1 pr-3">Configuration</th>
            <th className="py-1 pr-3">Recall@k</th>
            <th className="py-1 pr-3">nDCG@k</th>
            <th className="py-1 pr-3">MRR</th>
            <th className="py-1 pr-3">p50 ms</th>
            <th className="py-1 pr-3">p99 ms</th>
            <th className="py-1">Tokens</th>
          </tr>
        </thead>
        <tbody>
          {(run.results || []).map((r) => (
            <tr key={r.config} className={`border-b border-gray-100 ${r.config === best ? 'bg-green-50 font-medium' : ''}`}>
              <td className="py-1 pr-3 text-gray-900">
                {r.config}
                {r.config === best && <span className="ml-2 text-xs text-green-800">best nDCG</span>}
              </td>
              {r.error ? (
                <td colSpan={6} className="py-1 text-red-800">{r.error}</td>
              ) : (
                <>
                  <td className="py-1 pr-3">{formatScore(r.recallAtK)}</td>
                  <td className="py-1 pr-3">{formatScore(r.ndcgAtK)}</td>
                  <td className="py-1 pr-3">{formatScore(r.mrr)}</td>
                  <td className="py-1 pr-3">{r.p50Ms}</td>
                  <td className="py-1 pr-3">{r.p99Ms}</td>
                  <td className="py-1">{r.tokens?.toLocaleString()}</td>
                </>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Retrieval evaluation view: the stored set and a run over chosen configs. */
export default function EvalPanel() {
  const { currentCollection, stats } = useStore();
  const collections = (stats?.collections || []).map((c) => c.name);
  const [collection, setCollection] = useState(currentCollection || '');
  const [queries, setQueries] = useState([]);
  const [form, setForm] = useState(emptyForm);
  const [k, setK] = useState(10);
  const [presets, setPresets] = useState(EVAL_PRESETS.map((p) => p.id));
  const [run, setRun] = useState(null);
  const [running, setRunning] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const controller = useRef(null);

  const load = (name) => mddbClient
    .listEvalQueries(name)
    .then((data) => setQueries(data.queries || []))
    .catch((err) => setError(err.message));

  useEffect(() => {
    if (collection) load(collection);
    return () => controller.current?.abort();
  }, [collection]);

  // Runs one change to the stored set, then reloads it.
  const change = async (action) => {
    setBusy(true);
    setError(null);
    try {
      await action();
      await load(collection);
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const save = (e) => {
    e.preventDefault();
    change(async () => {
      await mddbClient.saveEvalQuery({
        collection,
        id: form.id.trim(),
        query: form.query.trim(),
        expectedKeys: parseKeys(form.expectedKeys),
        grades: parseGrades(form.grades),
      });
      setForm(emptyForm);
    });
  };

  const remove = (id) => {
    if (!confirm(`Delete the evaluation query "${id}"?`)) return;
    change(() => mddbClient.deleteEvalQuery(collection, id));
  };

  const togglePreset = (id) => setPresets((on) => (on.includes(id) ? on.filter((p) => p !== id) : [...on, id]));

  const start = async () => {
    controller.current = new AbortController();
    setRunning(true);
    setError(null);
    setRun(null);
    try {
      const configs = EVAL_PRESETS.filter((p) => presets.includes(p.id)).map((p) => p.config);
      setRun(await mddbClient.runEval({ collection, k: clampK(k), configs, signal: controller.current.signal }));
    } catch (err) {
      if (err.name !== 'AbortError') setError(err.message);
    } finally {
      setRunning(false);
    }
  };

  const formValid = form.id.trim() && form.query.trim() && parseKeys(form.expectedKeys).length > 0;

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-gray-50">
      <div className="max-w-5xl mx-auto space-y-6">
        <div className="bg-white rounded-lg shadow p-6">
          <div className="flex items-center mb-2">
            <Gauge className="w-5 h-5 text-primary-600 mr-2" aria-hidden="true" />
            <h2 className="text-lg font-semibold text-gray-900">Retrieval Evaluation</h2>
          </div>
          <p className="text-sm text-gray-600 mb-4">
            Questions with the documents that should answer them, run under several search configurations. With a
            stored set, the Search Advisor recommends the configuration that scored best. Evaluation only reads.
          </p>
          <label htmlFor="eval-collection" className="block text-xs text-gray-600">Collection</label>
          <select
            id="eval-collection"
            value={collection}
            onChange={(e) => {
              setQueries([]);
              setRun(null);
              setCollection(e.target.value);
            }}
            className={`${inputClass} max-w-xs`}
          >
            <option value="">Select…</option>
            {collections.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
          {error && <p role="alert" className="mt-3 rounded border border-red-200 bg-red-50 p-2 text-sm text-red-800">{error}</p>}
        </div>

        {collection && (
          <>
            <div className="bg-white rounded-lg shadow p-6">
              <h3 className="text-sm font-semibold text-gray-900 mb-2">Stored queries</h3>
              <QueryList queries={queries} onDelete={remove} busy={busy} />
              <form onSubmit={save} className="mt-4 grid gap-2 md:grid-cols-2">
                <div>
                  <label htmlFor="eval-id" className="block text-xs text-gray-600">Id (an existing id is replaced)</label>
                  <input id="eval-id" value={form.id} onChange={(e) => setForm({ ...form, id: e.target.value })} className={inputClass} />
                </div>
                <div>
                  <label htmlFor="eval-query" className="block text-xs text-gray-600">Query</label>
                  <input id="eval-query" value={form.query} onChange={(e) => setForm({ ...form, query: e.target.value })} className={inputClass} />
                </div>
                <div>
                  <label htmlFor="eval-keys" className="block text-xs text-gray-600">Expected keys (comma-separated)</label>
                  <input id="eval-keys" value={form.expectedKeys} onChange={(e) => setForm({ ...form, expectedKeys: e.target.value })} placeholder="BACKUP.md, RESTORE.md" className={inputClass} />
                </div>
                <div>
                  <label htmlFor="eval-grades" className="block text-xs text-gray-600">Grades for nDCG (optional, key=0..3)</label>
                  <input id="eval-grades" value={form.grades} onChange={(e) => setForm({ ...form, grades: e.target.value })} placeholder="BACKUP.md=3" className={inputClass} />
                </div>
                <div className="md:col-span-2">
                  <button
                    type="submit"
                    disabled={busy || !formValid}
                    className="px-3 py-1.5 bg-primary-700 text-white text-sm rounded hover:bg-primary-800 disabled:opacity-50"
                  >
                    Save query
                  </button>
                </div>
              </form>
            </div>

            <div className="bg-white rounded-lg shadow p-6">
              <h3 className="text-sm font-semibold text-gray-900 mb-2">Run</h3>
              <fieldset className="mb-3">
                <legend className="text-xs text-gray-600 mb-1">Configurations</legend>
                <div className="flex flex-wrap gap-4">
                  {EVAL_PRESETS.map((p) => (
                    <label key={p.id} className="flex items-center gap-1.5 text-sm text-gray-800">
                      <input type="checkbox" checked={presets.includes(p.id)} onChange={() => togglePreset(p.id)} />
                      {p.label}
                    </label>
                  ))}
                </div>
              </fieldset>
              <div className="flex items-end gap-3">
                <div>
                  <label htmlFor="eval-k" className="block text-xs text-gray-600">k (1–100)</label>
                  <input id="eval-k" type="number" min={1} max={100} value={k} onChange={(e) => setK(e.target.value)} className={`${inputClass} w-24`} />
                </div>
                {running ? (
                  <button type="button" onClick={() => controller.current.abort()} className="flex items-center px-3 py-1.5 bg-gray-700 text-white text-sm rounded hover:bg-gray-800">
                    <Square className="w-4 h-4 mr-1.5" aria-hidden="true" /> Cancel
                  </button>
                ) : (
                  <button
                    type="button"
                    onClick={start}
                    disabled={presets.length === 0}
                    className="flex items-center px-3 py-1.5 bg-primary-700 text-white text-sm rounded hover:bg-primary-800 disabled:opacity-50"
                  >
                    <Play className="w-4 h-4 mr-1.5" aria-hidden="true" /> Run evaluation
                  </button>
                )}
              </div>
              {running && <p className="mt-3 text-sm text-gray-700">Running…</p>}
              {run && <div className="mt-4"><ResultsTable run={run} /></div>}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
