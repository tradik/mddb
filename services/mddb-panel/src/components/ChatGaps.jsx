import { useEffect, useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { inputClass, labelClass } from '../lib/chat-scenario';
import { gapParams } from '../lib/chat-log';
import { formatUnix } from '../lib/time-format';

// #406: docs gaps — what visitors asked whose searches found nothing or
// scored low, grouped by the question's normalised text, most frequent
// first: what to write next. Shares the Log tab's scenario and dates.

export default function ChatGaps({ filters }) {
  const [minCount, setMinCount] = useState('1');
  const [threshold, setThreshold] = useState('');
  const [result, setResult] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    let live = true;
    mddbClient
      .chatGaps(gapParams(filters, { minCount, threshold }))
      .then((data) => {
        if (!live) return;
        setResult(data);
        setError(null);
      })
      .catch((err) => live && setError(err.message));
    return () => { live = false; };
  }, [filters, minCount, threshold]);

  const gaps = result?.gaps || [];
  return (
    <section aria-labelledby="chat-gaps-title" className="rounded-lg bg-white p-4 shadow">
      <h3 id="chat-gaps-title" className="text-base font-semibold text-gray-900">Docs gaps</h3>
      <p className="mb-3 text-sm text-gray-700">
        Questions whose searches found nothing, or whose best result scored below the threshold
        {result ? ` (${result.threshold})` : ''}. Asked most often first.
      </p>
      <div className="mb-3 grid max-w-md gap-3 md:grid-cols-2">
        <div>
          <label htmlFor="chat-gaps-min" className={labelClass}>Asked at least</label>
          <input id="chat-gaps-min" type="number" min="1" value={minCount} onChange={(e) => setMinCount(e.target.value)} className={inputClass} />
        </div>
        <div>
          <label htmlFor="chat-gaps-threshold" className={labelClass}>Score threshold</label>
          <input id="chat-gaps-threshold" type="number" min="0" step="0.05" value={threshold} placeholder="server default"
            onChange={(e) => setThreshold(e.target.value)} className={inputClass} />
        </div>
      </div>
      {error && <p role="alert" className="mb-3 rounded border border-red-200 bg-red-50 p-2 text-sm text-red-800">{error}</p>}
      {result && gaps.length === 0 && <p className="text-sm text-gray-700">No gaps in this window.</p>}
      {gaps.length > 0 && (
        <table className="w-full text-left text-sm">
          <thead className="text-xs text-gray-700">
            <tr><th scope="col" className="p-2">Question</th><th scope="col" className="p-2">Asked</th><th scope="col" className="p-2">Found nothing</th><th scope="col" className="p-2">Last asked</th><th scope="col" className="p-2">Scenarios</th></tr>
          </thead>
          <tbody className="text-gray-900">
            {gaps.map((g) => (
              <tr key={g.question} className="border-t border-gray-200 align-top">
                <td className="p-2">{g.example}</td>
                <td className="p-2">{g.count}</td>
                <td className="p-2">{g.noResults}</td>
                <td className="whitespace-nowrap p-2">{formatUnix(g.lastAskedAt)}</td>
                <td className="p-2">{(g.scenarios || []).join(', ')}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
