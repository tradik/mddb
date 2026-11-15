import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';

/**
 * ClassifyBox ranks candidate labels by embedding similarity
 * (/v1/classify, zero-shot): either the open document (its stored vector is
 * reused) or text typed here. Labels the server scores below the threshold
 * (default 0) are left out of its answer. Needs an embedding provider.
 */
export default function ClassifyBox({ document: doc }) {
  const [source, setSource] = useState('document');
  const [text, setText] = useState('');
  const [labels, setLabels] = useState('');
  const [result, setResult] = useState(null);
  const { busy, error, run } = useAction();
  const labelList = labels.split(',').map((l) => l.trim()).filter(Boolean);

  const classify = () => run(async () => {
    const target = source === 'document'
      ? { collection: doc.collection, key: doc.key, lang: doc.lang }
      : { text };
    setResult(await mddbClient.classify({ ...target, labels: labelList }));
  });

  return (
    <div className="space-y-2 text-sm">
      <fieldset className="flex gap-4 text-fg-soft">
        <legend className="sr-only">What to classify</legend>
        <label className="inline-flex items-center gap-1"><input type="radio" name="classify-source" checked={source === 'document'} onChange={() => setSource('document')} /> This document</label>
        <label className="inline-flex items-center gap-1"><input type="radio" name="classify-source" checked={source === 'text'} onChange={() => setSource('text')} /> Other text</label>
      </fieldset>
      {source === 'text' && (
        <label className="block text-fg-soft">
          <span className="block mb-1">Text to classify</span>
          <textarea value={text} onChange={(e) => setText(e.target.value)} rows={3} className="w-full px-2 py-1.5 border border-line-strong rounded" />
        </label>
      )}
      <label className="block text-fg-soft">
        <span className="block mb-1">Labels (comma-separated)</span>
        <input value={labels} onChange={(e) => setLabels(e.target.value)} placeholder="devops, finance, cooking" className="w-full px-2 py-1.5 border border-line-strong rounded" />
      </label>
      <button
        type="button"
        onClick={classify}
        disabled={busy || labelList.length === 0 || (source === 'text' && !text.trim())}
        className="px-3 py-1.5 bg-blue-700 text-white rounded hover:bg-blue-800 disabled:opacity-50"
      >
        Classify
      </button>
      <ApiErrorNotice error={error} collection={doc.collection} />
      {result && (
        <div role="status">
          {(result.results || []).length === 0 ? (
            <p className="text-fg-soft">No label scored above the threshold.</p>
          ) : (
            <table className="w-full text-xs" aria-label="Label scores">
              <thead><tr className="text-left text-fg-soft"><th scope="col">Label</th><th scope="col" className="text-right">Score</th></tr></thead>
              <tbody>
                {result.results.map((r) => (
                  <tr key={r.label}><td className="text-fg">{r.label}</td><td className="text-right text-fg">{r.score.toFixed(3)}</td></tr>
                ))}
              </tbody>
            </table>
          )}
          <p className="text-xs text-fg-muted mt-1">Model {result.model}, {result.dimensions} dimensions.</p>
        </div>
      )}
    </div>
  );
}
