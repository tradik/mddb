import { useRef, useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { passageLocation, precisionStyle } from '../lib/code-symbol';

/** One relation: its passages, or a line saying there are none. */
export function RelationSection({ label, passages }) {
  return (
    <section className="mb-4">
      <h3 className="text-sm font-semibold text-gray-900 mb-1">
        {label} <span className="text-gray-600 font-normal">({passages.length})</span>
      </h3>
      {passages.length === 0 ? (
        <p className="text-xs text-gray-600">None.</p>
      ) : (
        <ul className="space-y-2">
          {passages.map((p, i) => (
            <li key={`${passageLocation(p)}-${i}`} className="border border-gray-200 rounded p-2">
              <div className="text-xs">
                <span className="font-mono text-blue-800">{passageLocation(p)}</span>
                {p.symbol && <span className="ml-2 text-gray-700">in {p.symbol}</span>}
                {p.kind && <span className="ml-2 text-gray-600">{p.kind}</span>}
                {p.target && <span className="ml-2 text-gray-700">for <span className="font-mono">{p.target}</span></span>}
              </div>
              {p.content && (
                <pre className="mt-1 text-xs bg-gray-50 p-2 rounded overflow-x-auto">{p.content}</pre>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/**
 * Where the answer came from (2.19.3): a language server's facts, text
 * matching, or both. Shown as a word, with colour only as a second cue.
 */
export function PrecisionBadge({ precision, source }) {
  const style = precisionStyle(precision);
  return (
    <span className={`inline-flex items-center ml-2 px-2 py-0.5 rounded border text-xs font-medium ${style.className}`}>
      Precision: {style.label}
      {source && <span className="ml-1 font-normal">({source})</span>}
    </span>
  );
}

/**
 * The definitions an ambiguous name may mean (2.19.3); choosing one asks
 * again with its qualified name and path.
 */
export function CandidatePicker({ candidates, onPick }) {
  return (
    <section className="mb-4 border border-amber-700 bg-amber-50 rounded p-3" aria-label="Candidates">
      <p className="text-sm text-gray-900 mb-2">
        This name has {candidates.length} definitions. Pick one to see only its references:
      </p>
      <ul className="space-y-1">
        {candidates.map((c) => (
          <li key={`${c.path}#${c.name}`}>
            <button
              type="button"
              onClick={() => onPick(c)}
              className="text-sm text-blue-800 underline hover:text-blue-900"
            >
              <span className="font-mono">{c.name}</span> ({c.kind}) in <span className="font-mono">{c.path}</span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** The extension → language table, loaded the first time it is opened. */
export function LanguagesList() {
  const [table, setTable] = useState(null);
  const [error, setError] = useState(null);
  const requested = useRef(false);

  const onToggle = async (e) => {
    if (!e.currentTarget.open || requested.current) return;
    requested.current = true;
    try {
      setTable(await mddbClient.codeLanguages());
    } catch (err) {
      setError(err.message);
    }
  };

  const extensions = Object.entries(table?.extensions || {}).sort(([a], [b]) => a.localeCompare(b));

  return (
    <details onToggle={onToggle} className="mt-4 text-sm">
      <summary className="cursor-pointer text-gray-800 font-medium">Recognised languages</summary>
      {error && <p role="alert" className="text-xs text-red-700 mt-1">{error}</p>}
      {!error && !table && <p className="text-xs text-gray-600 mt-1">Loading…</p>}
      {table && (
        <ul className="mt-2 grid grid-cols-2 md:grid-cols-4 gap-1 text-xs">
          {extensions.map(([ext, lang]) => (
            <li key={ext}><span className="font-mono">{ext}</span> → {lang}</li>
          ))}
        </ul>
      )}
    </details>
  );
}
