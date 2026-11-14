import { useRef, useState } from 'react';
import { Code2, Search, X } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import { DEFAULT_RELATIONS, MAX_SYMBOL_LIMIT, RELATIONS } from '../lib/code-symbol';
import { CandidatePicker, LanguagesList, PrecisionBadge, RelationSection } from './CodeSymbolParts';
import CodeGraphCard from './CodeGraphCard';

/**
 * CodeSymbolPanel answers where a code symbol is defined, used, called and
 * tested (2.18.8, `GET /v1/symbol`): each answer is a list of passages to
 * read, not documents to read through. Since 2.19.3 it shows how precise the
 * answer is, the implementations of an interface, and — when a name has
 * several definitions — the candidates to choose from.
 */
export default function CodeSymbolPanel() {
  const { stats, currentCollection } = useStore();
  const collections = (stats?.collections || []).map((c) => c.name);
  const [collection, setCollection] = useState(currentCollection || collections[0] || '');
  const [name, setName] = useState('');
  const [path, setPath] = useState('');
  const [include, setInclude] = useState(DEFAULT_RELATIONS);
  const [limit, setLimit] = useState(20);
  const [includeContent, setIncludeContent] = useState(false);
  const [result, setResult] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const abortRef = useRef(null);

  const toggle = (id) => {
    setInclude((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));
  };

  const run = async (symbol, file) => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setLoading(true);
    setError(null);
    try {
      const data = await mddbClient.codeSymbol({
        collection,
        name: symbol.trim(),
        path: file.trim(),
        include,
        limit: Math.min(Math.max(Number(limit) || 20, 1), MAX_SYMBOL_LIMIT),
        includeContent,
        signal: controller.signal,
      });
      setResult(data);
    } catch (err) {
      if (err.name !== 'AbortError') setError(err.message);
    } finally {
      if (abortRef.current === controller) {
        abortRef.current = null;
        setLoading(false);
      }
    }
  };

  const lookup = (e) => {
    e.preventDefault();
    run(name, path);
  };

  // A candidate is asked for by its qualified name in its own file.
  const pick = (candidate) => {
    setName(candidate.name);
    setPath(candidate.path);
    run(candidate.name, candidate.path);
  };

  const cancel = () => {
    abortRef.current?.abort();
    abortRef.current = null;
    setLoading(false);
  };

  const shown = RELATIONS.filter((r) => Array.isArray(result?.[r.id]) || (r.id === 'implementations' && result && include.includes(r.id)));

  return (
    <div className="h-full overflow-y-auto p-6">
      <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2 mb-1">
        <Code2 className="w-5 h-5" aria-hidden="true" /> Code Symbols
      </h2>
      <p className="text-sm text-gray-600 mb-4">
        Where a function, type or selector is defined, used, called and tested, as passages with line ranges.
        Collections indexed with <code>mddb-cli autoindex --semantic</code> answer from a language server&apos;s facts.
      </p>
      <form onSubmit={lookup} className="space-y-3 mb-6">
        <div className="flex flex-wrap gap-3 items-end">
          <label className="text-sm text-gray-800">
            <span className="block mb-1">Collection</span>
            <select
              value={collection}
              onChange={(e) => setCollection(e.target.value)}
              className="px-3 py-2 border border-gray-300 rounded-lg"
            >
              {collections.map((c) => <option key={c} value={c}>{c}</option>)}
            </select>
          </label>
          <label className="text-sm text-gray-800 flex-1 min-w-[12rem]">
            <span className="block mb-1">Symbol</span>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="SearchHybrid or Server.Close"
              className="w-full px-3 py-2 border border-gray-300 rounded-lg font-mono"
            />
          </label>
          <label className="text-sm text-gray-800 min-w-[10rem]">
            <span className="block mb-1">Definition file (optional)</span>
            <input
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder="internal/server/conn.go"
              className="w-full px-3 py-2 border border-gray-300 rounded-lg font-mono"
            />
          </label>
          <label className="text-sm text-gray-800">
            <span className="block mb-1">Limit per relation</span>
            <input
              type="number"
              min="1"
              max={MAX_SYMBOL_LIMIT}
              value={limit}
              onChange={(e) => setLimit(e.target.value)}
              className="w-24 px-3 py-2 border border-gray-300 rounded-lg"
            />
          </label>
        </div>
        <fieldset className="flex flex-wrap gap-3 text-sm text-gray-800">
          <legend className="sr-only">Relations</legend>
          {RELATIONS.map((r) => (
            <label key={r.id} className="inline-flex items-center gap-1">
              <input type="checkbox" checked={include.includes(r.id)} onChange={() => toggle(r.id)} />
              {r.label}
            </label>
          ))}
          <label className="inline-flex items-center gap-1 ml-4">
            <input type="checkbox" checked={includeContent} onChange={(e) => setIncludeContent(e.target.checked)} />
            Include source text
          </label>
        </fieldset>
        <div className="flex gap-2">
          <button
            type="submit"
            disabled={!collection || !name.trim() || include.length === 0}
            className="px-4 py-2 bg-blue-700 text-white rounded-lg hover:bg-blue-800 disabled:opacity-50 inline-flex items-center gap-1"
          >
            <Search className="w-4 h-4" aria-hidden="true" /> Look up
          </button>
          {loading && (
            <button type="button" onClick={cancel} className="px-4 py-2 bg-gray-100 text-gray-800 rounded-lg inline-flex items-center gap-1">
              <X className="w-4 h-4" aria-hidden="true" /> Cancel
            </button>
          )}
        </div>
      </form>
      {error && <p role="alert" className="text-sm text-red-700 mb-4">{error}</p>}
      {result && (
        <div>
          <p className="text-sm text-gray-700 mb-3">
            <span className="font-mono">{result.symbol}</span> in {result.collection}
            <PrecisionBadge precision={result.precision} source={result.semanticSource} />
            {result.truncated && <span className="ml-2 text-amber-800">Some relations were cut at the limit.</span>}
          </p>
          {result.ambiguous && result.candidates?.length > 0 && (
            <CandidatePicker candidates={result.candidates} onPick={pick} />
          )}
          {shown.map((r) => <RelationSection key={r.id} label={r.label} passages={result[r.id] || []} />)}
        </div>
      )}
      <LanguagesList />
      <CodeGraphCard collection={collection} />
    </div>
  );
}
