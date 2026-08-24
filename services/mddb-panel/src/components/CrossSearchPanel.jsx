import { useState, useRef } from 'react';
import { Search, Shuffle, AlertCircle, Ban } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';

export default function CrossSearchPanel() {
  const {
    stats,
    crossSearchResults, setCrossSearchResults,
    crossSearchLoading, setCrossSearchLoading,
    crossSearchError, setCrossSearchError,
    setCurrentDocument,
  } = useStore();

  const [sourceMode, setSourceMode] = useState('text');
  const [sourceCollection, setSourceCollection] = useState('');
  const [sourceDocID, setSourceDocID] = useState('');
  const [query, setQuery] = useState('');
  const [targetCollections, setTargetCollections] = useState([]);
  const [topK, setTopK] = useState(10);
  const [threshold, setThreshold] = useState(0.0);
  const [algorithm, setAlgorithm] = useState('flat');
  const [distanceMetric, setDistanceMetric] = useState('cosine');
  const [includeContent, setIncludeContent] = useState(false);
  const [searchStats, setSearchStats] = useState(null);
  const abortRef = useRef(null);

  const collections = (stats?.collections || []).map((c) => c.name);

  const handleCancel = () => {
    if (abortRef.current) {
      abortRef.current.abort();
      abortRef.current = null;
    }
  };

  const handleSearch = async () => {
    const params = {
      targetCollections: targetCollections.length > 0 ? targetCollections : undefined,
      topK,
      threshold,
      algorithm,
      distanceMetric,
      includeContent,
    };

    if (sourceMode === 'document') {
      if (!sourceCollection || !sourceDocID.trim()) return;
      params.sourceCollection = sourceCollection;
      params.sourceDocID = sourceDocID.trim();
    } else {
      if (!query.trim()) return;
      params.query = query.trim();
    }

    handleCancel();
    const controller = new AbortController();
    abortRef.current = controller;

    setCrossSearchLoading(true);
    setCrossSearchError(null);
    setSearchStats(null);
    try {
      const data = await mddbClient.crossSearch(params, { signal: controller.signal });
      setCrossSearchResults(data.results || []);
      setSearchStats({
        total: data.total,
        durationMs: data.durationMs,
        collectionsSearched: data.collectionsSearched,
      });
    } catch (error) {
      if (error.name === 'AbortError') {
        setCrossSearchError(null);
      } else {
        setCrossSearchError(error.message);
        setCrossSearchResults([]);
        setSearchStats(null);
      }
    } finally {
      setCrossSearchLoading(false);
      abortRef.current = null;
    }
  };

  const handleResultClick = async (result) => {
    const doc = result.document;
    if (!doc) return;

    const collection = result.collection;
    const initialDocument = {
      ...doc,
      collection,
      contentMd: doc.contentMd || 'Loading content...',
    };
    setCurrentDocument(initialDocument);

    try {
      const fullDocument = await mddbClient.getDocument({
        collection,
        key: doc.key,
        lang: doc.lang,
      });
      setCurrentDocument({ ...fullDocument, collection });
    } catch (error) {
      setCurrentDocument({
        ...initialDocument,
        contentMd: `Error loading content: ${error.message}`,
      });
    }
  };

  const toggleTargetCollection = (name) => {
    setTargetCollections((prev) =>
      prev.includes(name) ? prev.filter((c) => c !== name) : [...prev, name]
    );
  };

  return (
    <div className="h-full flex flex-col">
      {/* Search Controls */}
      <div className="p-4 border-b border-gray-200 space-y-4">
        <div className="flex items-center gap-2 mb-2">
          <Shuffle className="w-5 h-5 text-primary-600" />
          <h2 className="text-lg font-semibold text-gray-900">Cross-Collection Search</h2>
        </div>

        {/* Source Mode Toggle */}
        <div>
          <label className="block text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2">
            Source
          </label>
          <div className="flex gap-2 mb-3">
            <button
              onClick={() => setSourceMode('text')}
              className={`px-3 py-1.5 text-sm rounded-lg transition-colors ${
                sourceMode === 'text'
                  ? 'bg-primary-100 text-primary-700 font-medium'
                  : 'bg-gray-100 text-gray-600 hover:bg-gray-200'
              }`}
            >
              Text Query
            </button>
            <button
              onClick={() => setSourceMode('document')}
              className={`px-3 py-1.5 text-sm rounded-lg transition-colors ${
                sourceMode === 'document'
                  ? 'bg-primary-100 text-primary-700 font-medium'
                  : 'bg-gray-100 text-gray-600 hover:bg-gray-200'
              }`}
            >
              Document
            </button>
          </div>

          {sourceMode === 'text' ? (
            <textarea
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && !e.shiftKey && (e.preventDefault(), handleSearch())}
              placeholder="Describe what you're looking for across collections..."
              rows={2}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500 resize-none"
            />
          ) : (
            <div className="grid grid-cols-2 gap-2">
              <div>
                <label className="block text-xs font-medium text-gray-600 mb-1">Source Collection</label>
                <select
                  value={sourceCollection}
                  onChange={(e) => setSourceCollection(e.target.value)}
                  className="w-full px-2 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
                >
                  <option value="">Select...</option>
                  {collections.map((name) => (
                    <option key={name} value={name}>{name}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-xs font-medium text-gray-600 mb-1">Document ID</label>
                <input
                  type="text"
                  value={sourceDocID}
                  onChange={(e) => setSourceDocID(e.target.value)}
                  placeholder="doc-key"
                  className="w-full px-2 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
                />
              </div>
            </div>
          )}
        </div>

        {/* Target Collections */}
        <div>
          <label className="block text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2">
            Target Collections
          </label>
          <div className="flex flex-wrap gap-2">
            {collections.map((name) => (
              <label
                key={name}
                className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-sm cursor-pointer transition-colors ${
                  targetCollections.includes(name)
                    ? 'bg-primary-100 text-primary-700 border border-primary-300'
                    : 'bg-gray-50 text-gray-600 border border-gray-200 hover:bg-gray-100'
                }`}
              >
                <input
                  type="checkbox"
                  checked={targetCollections.includes(name)}
                  onChange={() => toggleTargetCollection(name)}
                  className="sr-only"
                />
                {name}
              </label>
            ))}
            {collections.length === 0 && (
              <span className="text-xs text-gray-400">No collections available</span>
            )}
          </div>
          {targetCollections.length === 0 && collections.length > 0 && (
            <p className="text-xs text-gray-400 mt-1">All collections will be searched if none selected</p>
          )}
        </div>

        {/* Settings Row */}
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">Algorithm</label>
            <select
              value={algorithm}
              onChange={(e) => setAlgorithm(e.target.value)}
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
              value={distanceMetric}
              onChange={(e) => setDistanceMetric(e.target.value)}
              className="w-full px-2 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
            >
              <option value="cosine">Cosine Similarity</option>
              <option value="dot_product">Dot Product</option>
              <option value="euclidean">Euclidean</option>
            </select>
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">
              Top K: {topK}
            </label>
            <input
              type="range"
              min={1}
              max={50}
              value={topK}
              onChange={(e) => setTopK(parseInt(e.target.value))}
              className="w-full accent-primary-600"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">
              Threshold: {Math.round(threshold * 100)}%
            </label>
            <input
              type="range"
              min={0}
              max={100}
              value={Math.round(threshold * 100)}
              onChange={(e) => setThreshold(parseInt(e.target.value) / 100)}
              className="w-full accent-primary-600"
            />
          </div>
        </div>

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

          {crossSearchLoading ? (
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
              disabled={sourceMode === 'text' ? !query.trim() : (!sourceCollection || !sourceDocID.trim())}
              className="flex items-center space-x-2 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <Search className="w-4 h-4" />
              <span className="text-sm font-medium">Search</span>
            </button>
          )}
        </div>
      </div>

      {/* Results */}
      <div className="flex-1 overflow-y-auto">
        {crossSearchResults.length > 0 && (
          <div className="px-4 pt-3 pb-1 flex items-center justify-between">
            <span className="text-xs font-medium text-gray-500">
              {crossSearchResults.length} result{crossSearchResults.length !== 1 ? 's' : ''} found
            </span>
            {searchStats && (
              <span className="text-xs text-gray-400">
                {searchStats.durationMs}ms | {searchStats.collectionsSearched} collection{searchStats.collectionsSearched !== 1 ? 's' : ''} searched
              </span>
            )}
          </div>
        )}

        {crossSearchError && (
          <div className="m-4 p-3 bg-red-50 border border-red-200 rounded-lg flex items-start space-x-2">
            <AlertCircle className="w-4 h-4 text-red-500 mt-0.5 flex-shrink-0" />
            <p className="text-sm text-red-700">{crossSearchError}</p>
          </div>
        )}

        {crossSearchResults.length === 0 && !crossSearchLoading && !crossSearchError && (query || sourceDocID) && (
          <div className="flex items-center justify-center h-32">
            <p className="text-gray-400 text-sm">No results found</p>
          </div>
        )}

        {!crossSearchLoading && crossSearchResults.length === 0 && !crossSearchError && !query && !sourceDocID && (
          <div className="flex items-center justify-center h-full">
            <div className="text-center">
              <Shuffle className="w-12 h-12 text-gray-300 mx-auto mb-3" />
              <p className="text-gray-500">Search across multiple collections</p>
              <p className="text-gray-400 text-sm mt-1">Find similar documents using semantic search</p>
            </div>
          </div>
        )}

        {crossSearchResults.length > 0 && (
          <div className="divide-y divide-gray-200">
            {crossSearchResults.map((result, idx) => {
              const doc = result.document;
              const pct = Math.round(result.score * 100);
              return (
                <button
                  key={`${result.collection}-${doc?.key}-${doc?.lang}-${idx}`}
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
                      <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-700">
                        {result.collection}
                      </span>
                    </div>
                    <span className="text-sm font-semibold text-primary-600">
                      {pct}%
                    </span>
                  </div>

                  {/* Score bar */}
                  <div className="w-full bg-gray-200 rounded-full h-1.5 mb-2">
                    <div
                      className="bg-primary-500 h-1.5 rounded-full transition-all"
                      style={{ width: `${pct}%` }}
                    />
                  </div>

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
    </div>
  );
}
