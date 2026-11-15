import { useRef, useState } from 'react';
import { BookOpen } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';
import CollectionNameInput from './CollectionNameInput';

/**
 * WikiImportCard uploads a MediaWiki XML dump (.xml or .xml.bz2) to
 * /v1/import-wiki, which streams it into a collection — articles only unless
 * other namespaces are named. A large dump takes minutes; Cancel only stops
 * waiting — the server finishes the import.
 */
export default function WikiImportCard() {
  const [file, setFile] = useState(null);
  const [collection, setCollection] = useState('wikipedia');
  const [lang, setLang] = useState('en');
  const [namespaces, setNamespaces] = useState('');
  const [maxPages, setMaxPages] = useState('');
  const [skipRedirects, setSkipRedirects] = useState(true);
  const [skipFts, setSkipFts] = useState(false);
  const [result, setResult] = useState(null);
  const { busy, error, run } = useAction();
  const abortRef = useRef(null);

  const submit = (e) => {
    e.preventDefault();
    setResult(null);
    const controller = new AbortController();
    abortRef.current = controller;
    run(async () => {
      try {
        setResult(await mddbClient.importWiki({
          file, collection: collection.trim(), lang: lang.trim(), namespaces: namespaces.trim(),
          skipRedirects, skipFts, maxPages: Number(maxPages) || 0, signal: controller.signal,
        }));
      } catch (err) {
        if (err.name !== 'AbortError') throw err;
      }
    });
  };

  const check = (label, value, setValue) => (
    <label className="inline-flex items-center gap-1 text-sm text-fg-soft">
      <input type="checkbox" checked={value} onChange={(e) => setValue(e.target.checked)} /> {label}
    </label>
  );

  return (
    <section aria-labelledby="wiki-import-title" className="bg-surface rounded-lg shadow p-4">
      <h3 id="wiki-import-title" className="text-base font-semibold text-fg flex items-center gap-2 mb-3">
        <BookOpen className="w-4 h-4" aria-hidden="true" /> Import a Wikipedia dump
      </h3>
      <form onSubmit={submit} className="space-y-3">
        <label className="block text-sm text-fg-soft">
          <span className="block mb-1">Dump file (.xml or .xml.bz2)</span>
          <input type="file" accept=".xml,.bz2" onChange={(e) => setFile(e.target.files[0] || null)} className="text-sm" />
        </label>
        <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
          <CollectionNameInput value={collection} onChange={setCollection} />
          {[['Language', lang, setLang], ['Namespaces (default 0)', namespaces, setNamespaces], ['Max pages (optional)', maxPages, setMaxPages]].map(([label, value, setValue]) => (
            <label key={label} className="block text-sm text-fg-soft">
              <span className="block mb-1">{label}</span>
              <input value={value} onChange={(e) => setValue(e.target.value)} className="w-full px-3 py-2 border border-line-strong rounded-lg" />
            </label>
          ))}
        </div>
        <div className="flex flex-wrap gap-4">
          {check('Skip redirect pages', skipRedirects, setSkipRedirects)}
          {check('Skip full-text indexing (run an FTS reindex afterwards)', skipFts, setSkipFts)}
        </div>
        <ApiErrorNotice error={error} collection={collection.trim() || '*'} />
        {result && (
          <p role="status" className="text-sm text-success-strong">
            Imported {result.imported} pages into {result.collection}, skipped {result.skipped}, failed {result.failed} ({Math.round(result.durationMs / 1000)} s).
          </p>
        )}
        <div className="flex gap-2">
          <button
            type="submit"
            disabled={busy || !file || !collection.trim() || !lang.trim()}
            className="px-4 py-2 bg-blue-700 text-white rounded-lg hover:bg-blue-800 disabled:opacity-50 text-sm"
          >
            {busy ? 'Importing…' : 'Import dump'}
          </button>
          {busy && (
            <button type="button" onClick={() => abortRef.current.abort()} className="px-4 py-2 bg-surface-subtle text-fg rounded-lg text-sm">
              Stop waiting
            </button>
          )}
          {busy && <p className="text-xs text-fg-muted self-center">The server goes on importing if you stop waiting.</p>}
        </div>
      </form>
    </section>
  );
}
