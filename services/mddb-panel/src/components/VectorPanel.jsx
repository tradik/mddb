import { useEffect, useState } from 'react';
import { Database, RefreshCw, AlertCircle, CheckCircle, Zap } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import IndexTrainingNotice from './IndexTrainingNotice';

export default function VectorPanel() {
  const [stats, setStats] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [reindexing, setReindexing] = useState({});
  const [reindexStatus, setReindexStatus] = useState({});

  useEffect(() => {
    loadStats();
  }, []);

  const loadStats = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await mddbClient.getVectorStats();
      setStats(data);
    } catch (err) {
      setError(err.message);
      console.error('Failed to load vector stats:', err);
    } finally {
      setLoading(false);
    }
  };

  const handleReindex = async (collection) => {
    setReindexing(prev => ({ ...prev, [collection]: true }));
    setReindexStatus(prev => ({ ...prev, [collection]: null }));

    try {
      const result = await mddbClient.reindexVectors(collection);
      setReindexStatus(prev => ({
        ...prev,
        [collection]: {
          success: true,
          message: `Reindexed ${result.indexed} documents`,
        },
      }));
      // Reload stats after reindex
      await loadStats();
    } catch (err) {
      setReindexStatus(prev => ({
        ...prev,
        [collection]: {
          success: false,
          message: err.message,
        },
      }));
    } finally {
      setReindexing(prev => ({ ...prev, [collection]: false }));
      // Clear status after 5 seconds
      setTimeout(() => {
        setReindexStatus(prev => {
          const newStatus = { ...prev };
          delete newStatus[collection];
          return newStatus;
        });
      }, 5000);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-600 mx-auto mb-4"></div>
          <p className="text-gray-500">Loading vector search stats...</p>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <AlertCircle className="w-12 h-12 text-red-600 mx-auto mb-4" />
          <p className="text-red-600 mb-2">Error loading vector stats</p>
          <p className="text-sm text-gray-500 mb-4">{error}</p>
          <button
            onClick={loadStats}
            className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700"
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  if (!stats || !stats.enabled) {
    return (
      <div className="h-full flex flex-col bg-gray-50">
        <div className="bg-white border-b px-6 py-4">
          <h1 className="text-2xl font-bold text-gray-900 mb-1">Vector Search</h1>
          <p className="text-gray-600">Manage vector embeddings and search</p>
        </div>

        <div className="flex-1 flex items-center justify-center p-6">
          <div className="max-w-lg text-center">
            <Database className="w-16 h-16 text-gray-400 mx-auto mb-4" />
            <h2 className="text-xl font-semibold text-gray-900 mb-2">
              Vector Search Disabled
            </h2>
            <p className="text-gray-600 mb-4">
              Vector search is not enabled on this server. To enable it, configure an embedding
              provider (OpenAI or Ollama) in your server environment variables.
            </p>
            <div className="bg-gray-50 border border-gray-200 rounded-lg p-4 text-left text-sm">
              <p className="font-medium text-gray-900 mb-2">Required environment variables:</p>
              <ul className="space-y-1 text-gray-700">
                <li>
                  <code className="bg-gray-200 px-1 py-0.5 rounded">
                    MDDB_EMBEDDING_PROVIDER
                  </code>{' '}
                  - "openai" or "ollama"
                </li>
                <li>
                  <code className="bg-gray-200 px-1 py-0.5 rounded">MDDB_EMBEDDING_MODEL</code> -
                  Model name
                </li>
                <li>
                  <code className="bg-gray-200 px-1 py-0.5 rounded">
                    MDDB_EMBEDDING_DIMENSIONS
                  </code>{' '}
                  - Embedding dimensions
                </li>
                <li>
                  <code className="bg-gray-200 px-1 py-0.5 rounded">
                    MDDB_EMBEDDING_API_KEY
                  </code>{' '}
                  - API key (OpenAI only)
                </li>
                <li>
                  <code className="bg-gray-200 px-1 py-0.5 rounded">
                    MDDB_EMBEDDING_API_URL
                  </code>{' '}
                  - API URL (Ollama only)
                </li>
              </ul>
            </div>
          </div>
        </div>
      </div>
    );
  }

  // Calculate totals from collections map (API returns snake_case fields)
  const collectionsMap = stats.collections || {};
  const collections = Object.keys(collectionsMap);
  const totalDocs = collections.reduce((a, c) => a + (collectionsMap[c]?.total_documents || 0), 0);
  const totalVectors = collections.reduce((a, c) => a + (collectionsMap[c]?.embedded_documents || 0), 0);
  const trainingSet = new Set(stats.indexTraining?.pending || []);

  return (
    <div className="h-full flex flex-col bg-gray-50">
      <div className="bg-white border-b px-6 py-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-gray-900 mb-1">Vector Search</h1>
            <p className="text-gray-600">Manage vector embeddings and search</p>
          </div>
          <button
            onClick={loadStats}
            className="px-4 py-2 bg-gray-100 text-gray-700 rounded hover:bg-gray-200 flex items-center gap-2"
          >
            <RefreshCw className="w-4 h-4" />
            Refresh
          </button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        <div className="max-w-6xl mx-auto space-y-6">
          <IndexTrainingNotice training={stats.indexTraining} />

          {/* Provider Info */}
          <div className="bg-white rounded-lg shadow p-6">
            <h2 className="text-lg font-semibold text-gray-900 mb-4 flex items-center gap-2">
              <Zap className="w-5 h-5 text-blue-600" />
              Embedding Provider
            </h2>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              <div>
                <div className="text-sm text-gray-500 mb-1">Provider</div>
                <div className="text-lg font-medium text-gray-900">
                  {stats.provider || 'Unknown'}
                </div>
              </div>
              <div>
                <div className="text-sm text-gray-500 mb-1">Model</div>
                <div className="text-lg font-medium text-gray-900">{stats.model || 'N/A'}</div>
              </div>
              <div>
                <div className="text-sm text-gray-500 mb-1">Dimensions</div>
                <div className="text-lg font-medium text-gray-900">
                  {stats.dimensions || 'N/A'}
                </div>
              </div>
            </div>
          </div>

          {/* Overview Stats */}
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
            <div className="bg-white rounded-lg shadow p-6">
              <div className="text-sm text-gray-500 mb-1">Total Collections</div>
              <div className="text-3xl font-bold text-gray-900">{collections.length}</div>
            </div>
            <div className="bg-white rounded-lg shadow p-6">
              <div className="text-sm text-gray-500 mb-1">Total Documents</div>
              <div className="text-3xl font-bold text-gray-900">{totalDocs}</div>
            </div>
            <div className="bg-white rounded-lg shadow p-6">
              <div className="text-sm text-gray-500 mb-1">Indexed Vectors</div>
              <div className="text-3xl font-bold text-blue-600">{totalVectors}</div>
            </div>
          </div>

          {/* Collections */}
          <div className="bg-white rounded-lg shadow">
            <div className="px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold text-gray-900">Collections</h2>
              <p className="text-sm text-gray-600 mt-1">
                Manage vector embeddings for each collection
              </p>
            </div>

            {collections.length === 0 ? (
              <div className="p-12 text-center text-gray-500">
                <Database className="w-12 h-12 mx-auto mb-2 text-gray-400" />
                <p>No collections found</p>
              </div>
            ) : (
              <div className="divide-y divide-gray-200">
                {collections.map((collection) => {
                  const docCount = collectionsMap[collection]?.total_documents || 0;
                  const vectorCount = collectionsMap[collection]?.embedded_documents || 0;
                  const coverage = docCount > 0 ? ((vectorCount / docCount) * 100).toFixed(1) : 0;
                  const isReindexing = reindexing[collection];
                  const status = reindexStatus[collection];

                  return (
                    <div key={collection} className="p-6">
                      <div className="flex items-start justify-between">
                        <div className="flex-1">
                          <h3 className="text-lg font-medium text-gray-900 mb-2 flex items-center gap-2">
                            {collection}
                            {trainingSet.has(collection) && (
                              <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-900">
                                {stats.indexTraining.current === collection ? 'training now' : 'waiting to train'}
                              </span>
                            )}
                          </h3>

                          <div className="grid grid-cols-3 gap-4 mb-3">
                            <div>
                              <div className="text-xs text-gray-500">Documents</div>
                              <div className="text-sm font-medium text-gray-900">{docCount}</div>
                            </div>
                            <div>
                              <div className="text-xs text-gray-500">Indexed</div>
                              <div className="text-sm font-medium text-gray-900">
                                {vectorCount}
                              </div>
                            </div>
                            <div>
                              <div className="text-xs text-gray-500">Coverage</div>
                              <div className="text-sm font-medium text-gray-900">{coverage}%</div>
                            </div>
                          </div>

                          {/* Progress bar */}
                          <div className="w-full bg-gray-200 rounded-full h-2">
                            <div
                              className={`h-2 rounded-full transition-all ${
                                coverage >= 100
                                  ? 'bg-green-600'
                                  : coverage >= 50
                                  ? 'bg-blue-600'
                                  : 'bg-yellow-600'
                              }`}
                              style={{ width: `${Math.min(coverage, 100)}%` }}
                            ></div>
                          </div>

                          {/* Status message */}
                          {status && (
                            <div
                              className={`mt-3 text-sm flex items-center gap-2 ${
                                status.success ? 'text-green-600' : 'text-red-600'
                              }`}
                            >
                              {status.success ? (
                                <CheckCircle className="w-4 h-4" />
                              ) : (
                                <AlertCircle className="w-4 h-4" />
                              )}
                              {status.message}
                            </div>
                          )}
                        </div>

                        <button
                          onClick={() => handleReindex(collection)}
                          disabled={isReindexing}
                          className="ml-4 px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2"
                        >
                          {isReindexing ? (
                            <>
                              <div className="animate-spin rounded-full h-4 w-4 border-2 border-white border-t-transparent"></div>
                              Reindexing...
                            </>
                          ) : (
                            <>
                              <RefreshCw className="w-4 h-4" />
                              Reindex
                            </>
                          )}
                        </button>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
