import { useState } from 'react';
import { Search } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';

const STRATEGIES = [
  { value: 'hybrid', label: 'Hybrid (vector + keyword)' },
  { value: 'semantic', label: 'Semantic' },
  { value: 'keyword', label: 'Keyword' },
];

/**
 * MemoryRecallBox asks /v1/memory/recall what an agent would get back for a
 * query: the matching messages with their score, session and role. The
 * user filter of the page scopes it, as it scopes an agent's recall.
 */
export default function MemoryRecallBox({ userId }) {
  const [query, setQuery] = useState('');
  const [strategy, setStrategy] = useState('hybrid');
  const [result, setResult] = useState(null);
  const { busy, error, run } = useAction();

  const recall = (e) => {
    e.preventDefault();
    run(async () => setResult(await mddbClient.recallMemory({ query: query.trim(), userId: userId || undefined, strategy })));
  };

  const hits = result?.results || [];

  return (
    <section aria-labelledby="memory-recall-title" className="bg-white rounded-lg shadow p-4 space-y-3">
      <h3 id="memory-recall-title" className="text-base font-semibold text-gray-900">Recall</h3>
      <form onSubmit={recall} className="flex flex-wrap gap-2 items-end">
        <label className="flex-1 min-w-[12rem] text-sm text-gray-800">
          <span className="block mb-1">Query</span>
          <input value={query} onChange={(e) => setQuery(e.target.value)} className="w-full px-3 py-2 border border-gray-300 rounded-lg" />
        </label>
        <label className="text-sm text-gray-800">
          <span className="block mb-1">Strategy</span>
          <select value={strategy} onChange={(e) => setStrategy(e.target.value)} className="px-2 py-2 border border-gray-300 rounded-lg">
            {STRATEGIES.map((s) => <option key={s.value} value={s.value}>{s.label}</option>)}
          </select>
        </label>
        <button
          type="submit"
          disabled={busy || !query.trim()}
          className="inline-flex items-center gap-1 px-4 py-2 bg-blue-700 text-white rounded-lg hover:bg-blue-800 disabled:opacity-50 text-sm"
        >
          <Search className="w-4 h-4" aria-hidden="true" /> {busy ? 'Recalling…' : 'Recall'}
        </button>
      </form>
      <ApiErrorNotice error={error} />
      {result && (
        <div role="status">
          <p className="text-sm text-gray-800 mb-2">{result.total ?? hits.length} messages recalled ({result.strategy}).</p>
          <ol className="space-y-2">
            {hits.map((hit) => (
              <li key={hit.document?.id} className="border border-gray-200 rounded p-2 text-sm">
                <p className="text-xs text-gray-700">
                  #{hit.rank} · score {hit.score.toFixed(3)} · <span className="font-semibold">{hit.role}</span> · session <span className="font-mono">{hit.sessionId}</span>
                </p>
                <p className="text-gray-900 whitespace-pre-wrap">{hit.document?.contentMd}</p>
              </li>
            ))}
          </ol>
        </div>
      )}
    </section>
  );
}
