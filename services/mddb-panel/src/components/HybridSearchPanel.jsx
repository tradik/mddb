import { useState, useRef, useEffect } from 'react';
import { Search, AlertCircle, Tag, Terminal, Ban, ChevronDown, ChevronUp, Plus, X } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import CommandModal from './CommandModal';
import MetaFilterBar from './MetaFilterBar';

export default function HybridSearchPanel() {
  const {
    currentCollection,
    hybridQuery, setHybridQuery,
    hybridTopK, setHybridTopK,
    hybridAlpha, setHybridAlpha,
    hybridStrategy, setHybridStrategy,
    hybridRrfK, setHybridRrfK,
    hybridSignals, setHybridSignals,
    hybridFtsAlgorithm, setHybridFtsAlgorithm,
    hybridVectorAlgorithm, setHybridVectorAlgorithm,
    hybridLang, setHybridLang,
    hybridDistanceMetric, setHybridDistanceMetric,
    hybridFuzzy, setHybridFuzzy,
    hybridThreshold, setHybridThreshold,
    hybridBoost, setHybridBoostEntry, removeHybridBoostEntry,
    hybridResults, setHybridResults,
    hybridLoading, setHybridLoading,
    hybridError, setHybridError,
    hybridSearchStats, setHybridSearchStats,
    searchFilterMeta,
    setCurrentDocument,
  } = useStore();

  const [includeContent, setIncludeContent] = useState(false);
  const [showCommand, setShowCommand] = useState(false);
  const [availableLangs, setAvailableLangs] = useState([]);
  const [boostOpen, setBoostOpen] = useState(false);
  const [newBoostKey, setNewBoostKey] = useState('');
  const [newBoostValue, setNewBoostValue] = useState('1');
  const abortRef = useRef(null);

  const handleAddBoost = () => {
    const key = newBoostKey.trim();
    const value = parseFloat(newBoostValue);
    if (!key.includes(':') || key.startsWith(':') || key.endsWith(':')) return;
    if (Number.isNaN(value) || value === 0) return;
    setHybridBoostEntry(key, value);
    setNewBoostKey('');
    setNewBoostValue('1');
  };

  useEffect(() => {
    mddbClient.ftsLanguages().then((data) => {
      setAvailableLangs(data.languages || []);
    }).catch(() => {});
  }, []);

  const handleCancel = () => {
    if (abortRef.current) {
      abortRef.current.abort();
      abortRef.current = null;
    }
  };

  const handleSearch = async () => {
    if (!currentCollection || !hybridQuery.trim()) return;

    handleCancel();
    const controller = new AbortController();
    abortRef.current = controller;

    setHybridLoading(true);
    setHybridError(null);
    try {
      const data = await mddbClient.hybridSearch({
        collection: currentCollection,
        query: hybridQuery.trim(),
        topK: hybridTopK,
        algorithm: hybridFtsAlgorithm,
        vectorAlgorithm: hybridVectorAlgorithm,
        alpha: hybridAlpha,
        strategy: hybridStrategy,
        rrfK: hybridRrfK,
        signals: hybridStrategy === 'weighted' ? hybridSignals : undefined,
        fuzzy: hybridFuzzy,
        threshold: hybridThreshold,
        includeContent,
        distanceMetric: hybridDistanceMetric,
        filterMeta: searchFilterMeta,
        lang: hybridLang || undefined,
        boost: hybridBoost,
        signal: controller.signal,
      });
      setHybridResults(data.results || []);
      setHybridSearchStats(data.searchStats || null);
    } catch (error) {
      if (error.name === 'AbortError') {
        setHybridError(null);
      } else {
        setHybridError(error.message);
        setHybridResults([]);
        setHybridSearchStats(null);
      }
    } finally {
      setHybridLoading(false);
      abortRef.current = null;
    }
  };

  const handleResultClick = async (result) => {
    const doc = result.document;
    if (!doc) return;

    const initialDocument = {
      ...doc,
      collection: currentCollection,
      contentMd: doc.contentMd || 'Loading content...',
    };
    setCurrentDocument(initialDocument);

    try {
      const fullDocument = await mddbClient.getDocument({
        collection: currentCollection,
        key: doc.key,
        lang: doc.lang,
      });
      setCurrentDocument({ ...fullDocument, collection: currentCollection });
    } catch (error) {
      setCurrentDocument({
        ...initialDocument,
        contentMd: `Error loading content: ${error.message}`,
      });
    }
  };

  if (!currentCollection) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <Search className="w-12 h-12 text-gray-300 mx-auto mb-3" />
          <p className="text-gray-500">Select a collection to search</p>
        </div>
      </div>
    );
  }

  const maxScore = hybridResults.length > 0 ? Math.max(...hybridResults.map(r => r.combinedScore)) : 1;

  return (
    <div className="h-full flex flex-col">
      {/* Search Controls */}
      <div className="p-4 border-b border-gray-200 space-y-4">
        <div>
          <label className="block text-xs font-semibold text-gray-500 uppercase tracking-wider mb-1">
            Hybrid Query
          </label>
          <input
            type="text"
            value={hybridQuery}
            onChange={(e) => setHybridQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleSearch()}
            placeholder="Search with keyword + semantic matching..."
            className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
          />
        </div>

        {/* Strategy */}
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">Strategy</label>
            <select
              value={hybridStrategy}
              onChange={(e) => setHybridStrategy(e.target.value)}
              className="w-full px-2 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
            >
              <option value="alpha">Alpha Blending</option>
              <option value="rrf">RRF (Reciprocal Rank Fusion)</option>
              <option value="weighted">Weighted (alpha + signals)</option>
            </select>
          </div>

          {(hybridStrategy === 'alpha' || hybridStrategy === 'weighted') && (
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">
                Alpha: {hybridAlpha.toFixed(2)}
              </label>
              <input
                type="range"
                min={0}
                max={100}
                value={Math.round(hybridAlpha * 100)}
                onChange={(e) => setHybridAlpha(parseInt(e.target.value) / 100)}
                className="w-full accent-primary-600"
              />
              <div className="flex justify-between text-[10px] text-gray-400 mt-0.5">
                <span>More Keyword</span>
                <span>More Semantic</span>
              </div>
            </div>
          )}

          {hybridStrategy === 'rrf' && (
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">
                RRF K
              </label>
              <input
                type="number"
                min={1}
                max={1000}
                value={hybridRrfK}
                onChange={(e) => setHybridRrfK(parseInt(e.target.value) || 60)}
                className="w-full px-2 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
              />
            </div>
          )}
        </div>

        {/* SRCH-002: the signals the weighted strategy adds on top of alpha's
            blend. Diversity is the one that earns its keep on most corpora —
            a document copied and edited scores almost identically to its
            original, and a vector score rewards the duplication rather than
            noticing it. */}
        {hybridStrategy === 'weighted' && (
          <div className="grid grid-cols-3 gap-3 rounded-lg bg-gray-50 p-3">
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">
                Diversity: {hybridSignals.diversity.toFixed(2)}
              </label>
              <input
                type="range"
                min={0}
                max={100}
                value={Math.round(hybridSignals.diversity * 100)}
                onChange={(e) =>
                  setHybridSignals({ ...hybridSignals, diversity: parseInt(e.target.value) / 100 })
                }
                className="w-full accent-primary-600"
                aria-describedby="signal-diversity-help"
              />
              <p id="signal-diversity-help" className="text-[10px] text-gray-500 mt-0.5">
                Demotes near-copies of higher results
              </p>
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">
                Proximity: {hybridSignals.proximity.toFixed(2)}
              </label>
              <input
                type="range"
                min={0}
                max={100}
                value={Math.round(hybridSignals.proximity * 100)}
                onChange={(e) =>
                  setHybridSignals({ ...hybridSignals, proximity: parseInt(e.target.value) / 100 })
                }
                className="w-full accent-primary-600"
                aria-describedby="signal-proximity-help"
              />
              <p id="signal-proximity-help" className="text-[10px] text-gray-500 mt-0.5">
                Rewards the same directory
              </p>
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">
                Freshness: {hybridSignals.freshness.toFixed(2)}
              </label>
              <input
                type="range"
                min={0}
                max={100}
                value={Math.round(hybridSignals.freshness * 100)}
                onChange={(e) =>
                  setHybridSignals({ ...hybridSignals, freshness: parseInt(e.target.value) / 100 })
                }
                className="w-full accent-primary-600"
                aria-describedby="signal-freshness-help"
              />
              <p id="signal-freshness-help" className="text-[10px] text-gray-500 mt-0.5">
                Rewards recent edits — wrong for reference material
              </p>
            </div>
          </div>
        )}

        {/* Algorithms */}
        <div className="grid grid-cols-4 gap-3">
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">Language</label>
            <select
              value={hybridLang}
              onChange={(e) => setHybridLang(e.target.value)}
              className="w-full px-2 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
            >
              <option value="">Auto (default)</option>
              {availableLangs.map((l) => (
                <option key={l.code} value={l.code}>{l.name} ({l.code})</option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">FTS Algorithm</label>
            <select
              value={hybridFtsAlgorithm}
              onChange={(e) => setHybridFtsAlgorithm(e.target.value)}
              className="w-full px-2 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
            >
              <option value="bm25">BM25</option>
              <option value="bm25f">BM25F (Field-Weighted)</option>
              <option value="pmisparse">PMISparse</option>
            </select>
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">Vector Algorithm</label>
            <select
              value={hybridVectorAlgorithm}
              onChange={(e) => setHybridVectorAlgorithm(e.target.value)}
              className="w-full px-2 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
            >
              <option value="flat">Flat (Exact)</option>
              <option value="hnsw">HNSW (Approximate)</option>
              <option value="ivf">IVF (Clustered)</option>
              <option value="pq">PQ (Compressed)</option>
              <option value="opq">OPQ (Optimized PQ)</option>
              <option value="sq">SQ (int8, 4x smaller)</option>
              <option value="sq4">SQ4 (int4, 8x smaller)</option>
              <option value="bq">BQ (Binary Quantized)</option>
            </select>
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">Distance Metric</label>
            <select
              value={hybridDistanceMetric}
              onChange={(e) => setHybridDistanceMetric(e.target.value)}
              className="w-full px-2 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
            >
              <option value="cosine">Cosine Similarity</option>
              <option value="dot_product">Dot Product</option>
              <option value="euclidean">Euclidean</option>
            </select>
          </div>
        </div>

        {/* Top K, Threshold, Fuzzy */}
        <div className="grid grid-cols-3 gap-3">
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">
              Top K: {hybridTopK}
            </label>
            <input
              type="range"
              min={1}
              max={50}
              value={hybridTopK}
              onChange={(e) => setHybridTopK(parseInt(e.target.value))}
              className="w-full accent-primary-600"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">
              Threshold: {Math.round(hybridThreshold * 100)}%
            </label>
            <input
              type="range"
              min={0}
              max={100}
              value={Math.round(hybridThreshold * 100)}
              onChange={(e) => setHybridThreshold(parseInt(e.target.value) / 100)}
              className="w-full accent-primary-600"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">Typo Tolerance</label>
            <select
              value={hybridFuzzy}
              onChange={(e) => setHybridFuzzy(parseInt(e.target.value))}
              className="w-full px-2 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
            >
              <option value={0}>Off</option>
              <option value={1}>Low (1 edit)</option>
              <option value={2}>Medium (2 edits)</option>
            </select>
          </div>
        </div>

        {/* Per-query boost map: metaKey:metaValue → multiplier */}
        <div className="border border-gray-200 rounded-lg">
          <button
            onClick={() => setBoostOpen(!boostOpen)}
            className="w-full flex items-center justify-between px-3 py-2 text-xs font-semibold text-gray-600 uppercase tracking-wider hover:bg-gray-50"
          >
            <span>
              Boost / Demote
              {Object.keys(hybridBoost).length > 0 && (
                <span className="ml-2 text-primary-600 normal-case text-[11px] font-normal">
                  {Object.keys(hybridBoost).length} active
                </span>
              )}
            </span>
            {boostOpen ? <ChevronUp className="w-3.5 h-3.5" /> : <ChevronDown className="w-3.5 h-3.5" />}
          </button>
          {boostOpen && (
            <div className="px-3 pb-3 space-y-2">
              <p className="text-[11px] text-gray-500">
                Applied to combined score after fusion. Key <code className="bg-gray-100 px-1 rounded">metaKey:metaValue</code>. Positive = boost, negative = demote.
              </p>
              {Object.entries(hybridBoost).map(([key, value]) => (
                <div key={key} className="flex items-center space-x-2">
                  <span className="text-xs text-gray-600 flex-1 truncate" title={key}>{key}</span>
                  <input
                    type="number"
                    step={0.5}
                    value={value}
                    onChange={(e) => setHybridBoostEntry(key, parseFloat(e.target.value) || 0)}
                    className="w-20 px-2 py-1 border border-gray-300 rounded text-xs text-center focus:outline-none focus:ring-1 focus:ring-primary-500"
                  />
                  <button
                    onClick={() => removeHybridBoostEntry(key)}
                    className="p-0.5 text-gray-400 hover:text-red-500"
                    title="Remove boost entry"
                  >
                    <X className="w-3.5 h-3.5" />
                  </button>
                </div>
              ))}
              <div className="flex items-center space-x-2 pt-1">
                <input
                  type="text"
                  value={newBoostKey}
                  onChange={(e) => setNewBoostKey(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && handleAddBoost()}
                  placeholder="tag:featured"
                  className="flex-1 px-2 py-1 border border-gray-300 rounded text-xs focus:outline-none focus:ring-1 focus:ring-primary-500"
                />
                <input
                  type="number"
                  step={0.5}
                  value={newBoostValue}
                  onChange={(e) => setNewBoostValue(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && handleAddBoost()}
                  className="w-20 px-2 py-1 border border-gray-300 rounded text-xs text-center focus:outline-none focus:ring-1 focus:ring-primary-500"
                />
                <button
                  onClick={handleAddBoost}
                  disabled={!newBoostKey.includes(':')}
                  className="flex items-center space-x-1 px-2 py-1 text-xs text-primary-600 hover:bg-primary-50 rounded disabled:opacity-40"
                >
                  <Plus className="w-3 h-3" />
                  <span>Add</span>
                </button>
              </div>
            </div>
          )}
        </div>

        <MetaFilterBar collection={currentCollection} />

        <div className="flex items-center justify-between">
          <label className="flex items-center space-x-2 text-sm text-gray-600">
            <input
              type="checkbox"
              checked={includeContent}
              onChange={(e) => setIncludeContent(e.target.checked)}
              className="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
            />
            <span>Include content</span>
          </label>
          <div className="flex items-center space-x-2">
            <button
              onClick={() => setShowCommand(true)}
              className="flex items-center space-x-2 px-3 py-2 bg-gray-100 text-gray-700 rounded-lg hover:bg-gray-200 transition-colors"
            >
              <Terminal className="w-4 h-4" />
              <span className="text-sm font-medium">Command</span>
            </button>
            {hybridLoading ? (
              <button
                onClick={handleCancel}
                className="flex items-center space-x-2 px-4 py-2 bg-red-600 text-white rounded-lg hover:bg-red-700 transition-colors"
              >
                <Ban className="w-4 h-4" />
                <span className="text-sm font-medium">Cancel</span>
              </button>
            ) : (
              <button
                onClick={handleSearch}
                disabled={!hybridQuery.trim()}
                className="flex items-center space-x-2 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <Search className="w-4 h-4" />
                <span className="text-sm font-medium">Search</span>
              </button>
            )}
          </div>
        </div>
      </div>

      {/* Results */}
      <div className="flex-1 overflow-y-auto">
        {hybridResults.length > 0 && (
          <div className="px-4 pt-3 pb-1 flex items-center justify-between">
            <span className="text-xs font-medium text-gray-500">
              {hybridResults.length} result{hybridResults.length !== 1 ? 's' : ''} found
            </span>
            {hybridSearchStats && (
              <span className="text-xs text-gray-400">
                {hybridSearchStats.durationMs}ms | {hybridSearchStats.totalTokens} token{hybridSearchStats.totalTokens !== 1 ? 's' : ''}{hybridSearchStats.queryTerms?.length > 0 ? ` | ${hybridSearchStats.queryTerms.join(', ')}` : ''}
              </span>
            )}
          </div>
        )}

        {hybridError && (
          <div className="m-4 p-3 bg-red-50 border border-red-200 rounded-lg flex items-start space-x-2">
            <AlertCircle className="w-4 h-4 text-red-500 mt-0.5 flex-shrink-0" />
            <p className="text-sm text-red-700">{hybridError}</p>
          </div>
        )}

        {hybridResults.length === 0 && !hybridLoading && !hybridError && hybridQuery && (
          <div className="flex items-center justify-center h-32">
            <p className="text-gray-400 text-sm">No results found</p>
          </div>
        )}

        {hybridResults.length > 0 && (
          <div className="divide-y divide-gray-200">
            {hybridResults.map((result, idx) => {
              const doc = result.document;
              const pct = maxScore > 0 ? Math.round((result.combinedScore / maxScore) * 100) : 0;
              return (
                <button
                  key={`${doc?.key}-${doc?.lang}-${idx}`}
                  onClick={() => handleResultClick(result)}
                  className="w-full text-left p-4 hover:bg-gray-50 transition-colors"
                >
                  <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center space-x-2">
                      <span className="inline-flex items-center justify-center w-6 h-6 rounded-full bg-primary-100 text-primary-700 text-xs font-bold">
                        {result.rank || idx + 1}
                      </span>
                      <h4 className="font-medium text-gray-900 truncate">
                        {doc?.key}
                      </h4>
                      <span className="text-xs text-gray-500">{doc?.lang}</span>
                    </div>
                    <span className="text-sm font-semibold text-primary-600">
                      {result.combinedScore.toFixed(4)}
                    </span>
                  </div>

                  {/* Individual scores */}
                  <div className="flex items-center space-x-3 mb-2">
                    {result.ftsScore !== undefined && (
                      <span className="text-[10px] text-gray-400">
                        FTS: {result.ftsScore.toFixed(4)}
                      </span>
                    )}
                    {result.vectorScore !== undefined && (
                      <span className="text-[10px] text-gray-400">
                        Vector: {result.vectorScore.toFixed(4)}
                      </span>
                    )}
                  </div>

                  {/* Score bar */}
                  <div className="w-full bg-gray-200 rounded-full h-1.5 mb-2">
                    <div
                      className="bg-primary-500 h-1.5 rounded-full transition-all"
                      style={{ width: `${pct}%` }}
                    />
                  </div>

                  {/* Matched terms */}
                  {result.matchedTerms && result.matchedTerms.length > 0 && (
                    <div className="flex flex-wrap gap-1 mt-1">
                      {result.matchedTerms.map((term) => (
                        <span
                          key={term}
                          className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-yellow-100 text-yellow-800"
                        >
                          <Tag className="w-3 h-3 mr-1" />
                          {term}
                        </span>
                      ))}
                    </div>
                  )}

                  {/* Meta tags */}
                  {doc?.meta && Object.keys(doc.meta).length > 0 && (
                    <div className="flex flex-wrap gap-1 mt-1">
                      {Object.entries(doc.meta).slice(0, 3).map(([key, values]) => (
                        <span
                          key={key}
                          className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-700"
                        >
                          {key}: {Array.isArray(values) ? values.join(', ') : values}
                        </span>
                      ))}
                    </div>
                  )}
                </button>
              );
            })}
          </div>
        )}
      </div>

      {/* Command Modal */}
      <CommandModal
        isOpen={showCommand}
        onClose={() => setShowCommand(false)}
        type="hybrid"
        params={{
          collection: currentCollection,
          query: hybridQuery,
          topK: hybridTopK,
          algorithm: hybridFtsAlgorithm,
          vectorAlgorithm: hybridVectorAlgorithm,
          alpha: hybridAlpha,
          strategy: hybridStrategy,
          rrfK: hybridRrfK,
          signals: hybridStrategy === 'weighted' ? hybridSignals : undefined,
          fuzzy: hybridFuzzy,
          threshold: hybridThreshold,
          distanceMetric: hybridDistanceMetric,
          lang: hybridLang || undefined,
          includeContent,
          filterMeta: searchFilterMeta,
          boost: hybridBoost,
        }}
      />
    </div>
  );
}
