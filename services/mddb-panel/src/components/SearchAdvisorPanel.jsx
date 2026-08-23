import { useCallback, useEffect, useState } from 'react';
import { Compass, RefreshCw, AlertCircle, AlertTriangle, Check } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import {
  embeddedCoverage,
  isSampled,
  formatBytes,
  vocabularyVerdict,
} from '../lib/advisor-format';

// SRCH-010. The server offers eight vector algorithms, four ranking algorithms,
// three fusion strategies and four retrieval modes. Nobody chooses well from a
// dropdown of names, and until now the panel asked them to.
//
// This measures the collection and shows what it found, what it recommends, and
// why — with the reasons in full, because a recommendation nobody can argue with
// is one nobody should follow.

function Stat({ label, value, hint }) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-3">
      <div className="text-xs font-medium text-gray-500">{label}</div>
      <div className="mt-0.5 text-lg font-semibold text-gray-900">{value}</div>
      {hint && <div className="mt-0.5 text-[11px] text-gray-500">{hint}</div>}
    </div>
  );
}

function Setting({ label, value }) {
  if (!value && value !== 0) return null;
  return (
    <div className="flex items-baseline justify-between gap-3 border-b border-gray-100 py-1.5 last:border-0">
      <span className="text-xs text-gray-600">{label}</span>
      <code className="rounded bg-gray-100 px-1.5 py-0.5 text-xs font-semibold text-gray-900">
        {String(value)}
      </code>
    </div>
  );
}

export default function SearchAdvisorPanel() {
  const { currentCollection } = useStore();
  const [collection, setCollection] = useState(currentCollection || '');
  const [advice, setAdvice] = useState(null);
  const [loading, setLoading] = useState(false);
  const [applying, setApplying] = useState(false);
  const [applied, setApplied] = useState(false);
  const [error, setError] = useState(null);

  const measure = useCallback(
    async ({ apply = false } = {}) => {
      if (!collection) return;
      apply ? setApplying(true) : setLoading(true);
      setError(null);
      try {
        const result = await mddbClient.searchAdvisor(collection, { apply });
        setAdvice(result);
        if (apply) setApplied(true);
      } catch (err) {
        setError(err.message || String(err));
      } finally {
        setLoading(false);
        setApplying(false);
      }
    },
    [collection]
  );

  useEffect(() => {
    setCollection(currentCollection || '');
    setAdvice(null);
    setApplied(false);
  }, [currentCollection]);

  const profile = advice?.profile;
  const measuredFromSample = isSampled(profile);
  const coverage = embeddedCoverage(profile);

  return (
    <div className="flex h-full flex-col overflow-y-auto bg-gray-50 p-4">
      <div className="mb-4 flex items-center gap-2">
        <Compass className="h-5 w-5 text-primary-600" aria-hidden="true" />
        <h2 className="text-base font-semibold text-gray-900">Search Advisor</h2>
      </div>

      <p className="mb-4 max-w-2xl text-sm text-gray-600">
        Measures this collection and recommends how to search it — search type,
        ranking algorithm, vector index, result shape — with a reason for every
        choice. It measures the corpus, not your queries, so disagreeing with a
        reason is a legitimate outcome.
      </p>

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <div>
          <label
            htmlFor="advisor-collection"
            className="mb-1 block text-xs font-medium text-gray-600"
          >
            Collection
          </label>
          <input
            id="advisor-collection"
            type="text"
            value={collection}
            onChange={(e) => {
              setCollection(e.target.value);
              setApplied(false);
            }}
            placeholder="collection name"
            className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
          />
        </div>
        <button
          type="button"
          onClick={() => measure()}
          disabled={!collection || loading}
          className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-3 py-2 text-sm font-medium text-white hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <RefreshCw
            className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`}
            aria-hidden="true"
          />
          {loading ? 'Measuring…' : 'Measure'}
        </button>
        {advice && (
          <button
            type="button"
            onClick={() => measure({ apply: true })}
            disabled={applying}
            className="inline-flex items-center gap-2 rounded-lg border border-primary-600 px-3 py-2 text-sm font-medium text-primary-700 hover:bg-primary-50 disabled:cursor-not-allowed disabled:opacity-50"
            title="Store this as the collection's retrieval profile, so every client inherits it"
          >
            {applied ? (
              <Check className="h-4 w-4" aria-hidden="true" />
            ) : null}
            {applying ? 'Storing…' : applied ? 'Stored as the profile' : 'Apply to collection'}
          </button>
        )}
      </div>

      {error && (
        <div
          role="alert"
          className="mb-4 flex items-start gap-2 rounded-lg border border-red-300 bg-red-50 p-3 text-sm text-red-800"
        >
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <span>{error}</span>
        </div>
      )}

      {advice?.warnings?.length > 0 && (
        <div
          role="alert"
          className="mb-4 rounded-lg border border-amber-300 bg-amber-50 p-3"
        >
          {advice.warnings.map((w) => (
            <div key={w} className="flex items-start gap-2 text-sm text-amber-900">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
              <span>{w}</span>
            </div>
          ))}
        </div>
      )}

      {profile && (
        <>
          <h3 className="mb-2 text-sm font-semibold text-gray-900">
            What this collection looks like
          </h3>
          <div className="mb-4 grid grid-cols-2 gap-3 md:grid-cols-4">
            <Stat
              label="Documents"
              value={profile.documents ?? 0}
              hint={measuredFromSample ? `${profile.sampled} sampled` : undefined}
            />
            <Stat
              label="Embedded"
              value={profile.embeddedDocuments ?? 0}
              hint={
                coverage === null
                  ? undefined
                  : `${coverage}% of the collection${
                      profile.estimatedVectorBytes
                        ? `, ${formatBytes(profile.estimatedVectorBytes)} of vectors`
                        : ''
                    }`
              }
            />
            <Stat
              label="Median length"
              value={`${profile.medianWords ?? 0} words`}
              hint={
                profile.longDocumentRatio > 0
                  ? `${Math.round(profile.longDocumentRatio * 100)}% over 500 words`
                  : undefined
              }
            />
            <Stat
              label="New terms per document"
              value={(profile.termsPerDocument ?? 0).toFixed(2)}
              hint={vocabularyVerdict(profile.termsPerDocument)}
            />
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <div className="rounded-lg border border-gray-200 bg-white p-4">
              <h3 className="mb-2 text-sm font-semibold text-gray-900">Recommended</h3>
              <Setting label="Search type" value={advice.searchType} />
              <Setting label="Ranking algorithm" value={advice.ftsAlgorithm} />
              <Setting label="Vector index" value={advice.vectorAlgorithm} />
              <Setting label="Fusion strategy" value={advice.hybridStrategy} />
              <Setting
                label="Alpha"
                value={
                  advice.hybridAlpha !== undefined && advice.hybridStrategy
                    ? advice.hybridAlpha
                    : ''
                }
              />
              <Setting label="Retrieval mode" value={advice.retrievalMode} />
              <Setting label="Top K" value={advice.topK} />
              {advice.signals?.diversity ? (
                <Setting label="Diversity signal" value={advice.signals.diversity} />
              ) : null}
            </div>

            <div className="rounded-lg border border-gray-200 bg-white p-4">
              <h3 className="mb-2 text-sm font-semibold text-gray-900">Why</h3>
              <ul className="space-y-2">
                {(advice.reasons || []).map((reason) => (
                  <li key={reason} className="text-xs leading-relaxed text-gray-700">
                    {reason}
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </>
      )}

      {!advice && !loading && !error && (
        <p className="text-sm text-gray-500">
          Pick a collection and measure it.
        </p>
      )}
    </div>
  );
}
