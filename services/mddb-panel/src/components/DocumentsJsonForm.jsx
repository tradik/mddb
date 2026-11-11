import { useId, useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { parseDocuments } from '../lib/json-text';
import { useAction } from '../lib/use-action';
import { useStore } from '../lib/store';
import ApiErrorNotice from './ApiErrorNotice';
import JsonTextarea from './JsonTextarea';

const EXAMPLE = '[\n  { "key": "post-1", "contentMd": "# First post", "meta": { "category": ["blog"] } },\n  { "key": "post-2", "lang": "pl", "contentMd": "# Drugi wpis" }\n]';

/** One line for an /v1/add-batch answer. */
const describeBatch = (r) => `Added ${r.added || 0}, updated ${r.updated || 0}, failed ${r.failed || 0}.`;

/**
 * DocumentsJsonForm adds several documents given as a JSON array or NDJSON,
 * pasted or loaded from a file. "Add now" sends one /v1/add-batch request and
 * reports per-document failures; "Queue as a job" hands the batch to the
 * bulk ingest worker (/v1/bulk-ingest-job) for large imports, and `onQueued`
 * receives the job so its progress can be followed.
 */
export default function DocumentsJsonForm({ onQueued }) {
  const listId = useId();
  const stats = useStore((s) => s.stats);
  const currentCollection = useStore((s) => s.currentCollection);
  const names = (stats?.collections || []).map((c) => c.name);
  const [collection, setCollection] = useState(currentCollection || '');
  const [lang, setLang] = useState('en');
  const [text, setText] = useState('');
  const [result, setResult] = useState(null);
  const { busy, error, run, setError } = useAction();

  const parsed = text.trim() ? parseDocuments(text, lang.trim() || 'en') : null;

  const loadFile = async (file) => {
    if (file) setText(await file.text());
  };

  const send = (queue) => run(async () => {
    setResult(null);
    if (!collection.trim()) {
      setError(new Error('Name the collection the documents go to.'));
      return;
    }
    const request = { collection: collection.trim(), documents: parsed.documents };
    if (queue) {
      const job = await mddbClient.startBulkIngestJob(request);
      setResult({ text: `Queued job ${job.id}: ${job.total} documents.` });
      onQueued(job);
    } else {
      const answer = await mddbClient.addBatch(request);
      setResult({ text: describeBatch(answer), errors: answer.errors || [] });
    }
  });

  const ready = parsed && !parsed.error && !busy;

  return (
    <section aria-labelledby="documents-json-title" className="bg-white rounded-lg shadow p-4 space-y-3">
      <h3 id="documents-json-title" className="text-base font-semibold text-gray-900">Add documents from JSON</h3>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <label className="text-sm text-gray-800">
          <span className="block mb-1">Collection</span>
          <input list={listId} value={collection} onChange={(e) => setCollection(e.target.value)} className="w-full px-3 py-2 border border-gray-300 rounded-lg" />
          <datalist id={listId}>{names.map((n) => <option key={n} value={n} />)}</datalist>
        </label>
        <label className="text-sm text-gray-800">
          <span className="block mb-1">Language of documents that name none</span>
          <input value={lang} onChange={(e) => setLang(e.target.value)} className="w-full px-3 py-2 border border-gray-300 rounded-lg" />
        </label>
      </div>
      <JsonTextarea
        label="Documents (JSON array or one JSON object per line)"
        value={text}
        onChange={(value) => { setText(value); setResult(null); }}
        error={parsed?.error}
        hint="Each document needs a key and contentMd; meta is an object of value lists."
        placeholder={EXAMPLE}
        rows={10}
      />
      <label className="block text-sm text-gray-800">
        <span className="block mb-1">Or load a .json or .ndjson file</span>
        <input type="file" accept=".json,.ndjson,.jsonl,application/json" onChange={(e) => loadFile(e.target.files[0])} className="text-sm" />
      </label>
      {parsed?.documents && <p className="text-sm text-gray-800">{parsed.documents.length} documents ready.</p>}
      <ApiErrorNotice error={error} collection={collection.trim() || '*'} />
      {result && (
        <div role="status" className="text-sm text-gray-900">
          <p>{result.text}</p>
          {result.errors?.length > 0 && <ul className="list-disc pl-5 text-red-800">{result.errors.map((e) => <li key={e}>{e}</li>)}</ul>}
        </div>
      )}
      <div className="flex gap-2">
        <button type="button" onClick={() => send(false)} disabled={!ready} className="px-4 py-2 bg-blue-700 text-white rounded-lg hover:bg-blue-800 disabled:opacity-50 text-sm">
          Add now
        </button>
        <button type="button" onClick={() => send(true)} disabled={!ready} className="px-4 py-2 bg-gray-100 text-gray-900 rounded-lg hover:bg-gray-200 disabled:opacity-50 text-sm">
          Queue as a job
        </button>
      </div>
    </section>
  );
}
