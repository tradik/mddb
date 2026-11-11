import { useState } from 'react';
import { Network } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';

const DIRECTIONS = [
  { value: 'both', label: 'Both ways' },
  { value: 'in', label: 'What depends on it' },
  { value: 'out', label: 'What it depends on' },
];

/** "file:line" when the server sent a line, else the file. */
const at = (file, line) => (line ? `${file}:${line}` : file);

/**
 * CodeGraphCard answers what depends on a code document and what it depends
 * on (/v1/code-graph): the files reached within 1–3 hops and the edges with
 * the selector, call or path that justifies each. A truncated walk cannot
 * prove that nothing else depends on the file.
 */
export default function CodeGraphCard({ collection }) {
  const [key, setKey] = useState('');
  const [direction, setDirection] = useState('both');
  const [depth, setDepth] = useState('1');
  const [lines, setLines] = useState(false);
  const [graph, setGraph] = useState(null);
  const { busy, error, run } = useAction();

  const submit = (e) => {
    e.preventDefault();
    run(async () => setGraph(await mddbClient.codeGraph({ collection, key: key.trim(), direction, depth: Number(depth), lines })));
  };

  return (
    <section aria-labelledby="code-graph-title" className="mt-6 border-t border-gray-200 pt-4 space-y-3">
      <h3 id="code-graph-title" className="text-lg font-semibold text-gray-900 flex items-center gap-2">
        <Network className="w-4 h-4" aria-hidden="true" /> Code graph
      </h3>
      <form onSubmit={submit} className="flex flex-wrap gap-3 items-end text-sm">
        <label className="flex-1 min-w-[12rem] text-gray-800">
          <span className="block mb-1">Document key in {collection || '…'}</span>
          <input value={key} onChange={(e) => setKey(e.target.value)} placeholder="theme/style.css" className="w-full px-3 py-2 border border-gray-300 rounded-lg font-mono" />
        </label>
        <label className="text-gray-800">
          <span className="block mb-1">Direction</span>
          <select value={direction} onChange={(e) => setDirection(e.target.value)} className="px-2 py-2 border border-gray-300 rounded-lg">
            {DIRECTIONS.map((d) => <option key={d.value} value={d.value}>{d.label}</option>)}
          </select>
        </label>
        <label className="text-gray-800">
          <span className="block mb-1">Hops</span>
          <select value={depth} onChange={(e) => setDepth(e.target.value)} className="px-2 py-2 border border-gray-300 rounded-lg">
            {['1', '2', '3'].map((d) => <option key={d} value={d}>{d}</option>)}
          </select>
        </label>
        <label className="inline-flex items-center gap-1 text-gray-800">
          <input type="checkbox" checked={lines} onChange={(e) => setLines(e.target.checked)} /> Line numbers
        </label>
        <button type="submit" disabled={busy || !collection || !key.trim()} className="px-4 py-2 bg-blue-700 text-white rounded-lg hover:bg-blue-800 disabled:opacity-50">
          Show graph
        </button>
      </form>
      <ApiErrorNotice error={error} collection={collection} />
      {graph && (
        <div role="status" className="text-sm space-y-2">
          <p className="text-gray-800">
            {graph.nodes?.length || 0} files around <span className="font-mono">{graph.root}</span>, {graph.edges?.length || 0} edges.
            {graph.truncated && <span className="ml-2 text-amber-900">Cut at a depth or degree limit: more may depend on it.</span>}
          </p>
          {graph.edges?.length > 0 && (
            <table className="min-w-full text-xs" aria-label="Code graph edges">
              <thead>
                <tr className="text-left text-gray-800">
                  <th scope="col" className="pr-3">From</th>
                  <th scope="col" className="pr-3">To</th>
                  <th scope="col" className="pr-3">Kind</th>
                  <th scope="col" className="pr-3">Symbol</th>
                  <th scope="col">Direction</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 font-mono">
                {graph.edges.map((edge, i) => (
                  <tr key={`${edge.from}-${edge.to}-${edge.kind}-${edge.symbol}-${edge.direction}-${i}`}>
                    <td className="pr-3 text-gray-900">{at(edge.from, edge.lines?.fromLine)}</td>
                    <td className="pr-3 text-gray-900">{at(edge.to, edge.lines?.toLine)}</td>
                    <td className="pr-3 text-gray-800">{edge.kind}</td>
                    <td className="pr-3 text-gray-900">{edge.symbol}</td>
                    <td className="text-gray-800">{edge.direction}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </section>
  );
}
