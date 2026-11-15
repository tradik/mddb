import { useEffect, useState } from 'react';
import { Database, RefreshCw, AlertCircle, CheckCircle, Zap } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import IndexTrainingNotice from './IndexTrainingNotice';
import ReindexJobsTable from './ReindexJobsTable';
import { useReindexJobs, describeJob } from '../lib/use-reindex-jobs';

export default function VectorPanel() {
  const [stats, setStats] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [force, setForce] = useState(false);

  const loadStats = async ({ quiet = false } = {}) => {
    if (!quiet) setLoading(true);
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

  // 2.18.4: a reindex runs as a background job, polled until it stops; the
  // stats reload quietly when it does.
  const reindex = useReindexJobs(() => loadStats({ quiet: true }));

  useEffect(() => {
    loadStats();
  }, []);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-600 mx-auto mb-4"></div>
          <p className="text-fg-faint">Loading vector search stats...</p>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <AlertCircle className="w-12 h-12 text-danger mx-auto mb-4" />
          <p className="text-danger mb-2">Error loading vector stats</p>
          <p className="text-sm text-fg-faint mb-4">{error}</p>
          <button
            onClick={() => loadStats()}
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
      <div className="h-full flex flex-col bg-surface-muted">
        <div className="bg-surface border-b px-6 py-4">
          <h1 className="text-2xl font-bold text-fg mb-1">Vector Search</h1>
          <p className="text-fg-subtle">Manage vector embeddings and search</p>
        </div>

        <div className="flex-1 flex items-center justify-center p-6">
          <div className="max-w-lg text-center">
            <Database className="w-16 h-16 text-fg-faint mx-auto mb-4" />
            <h2 className="text-xl font-semibold text-fg mb-2">
              Vector Search Disabled
            </h2>
            <p className="text-fg-subtle mb-4">
              Vector search is not enabled on this server. To enable it, configure an embedding
              provider (OpenAI or Ollama) in your server environment variables.
            </p>
            <div className="bg-surface-muted border border-line rounded-lg p-4 text-left text-sm">
              <p className="font-medium text-fg mb-2">Required environment variables:</p>
              <ul className="space-y-1 text-fg-muted">
                <li>
                  <code className="bg-surface-strong px-1 py-0.5 rounded">
                    MDDB_EMBEDDING_PROVIDER
                  </code>{' '}
                  - "openai" or "ollama"
                </li>
                <li>
                  <code className="bg-surface-strong px-1 py-0.5 rounded">MDDB_EMBEDDING_MODEL</code> -
                  Model name
                </li>
                <li>
                  <code className="bg-surface-strong px-1 py-0.5 rounded">
                    MDDB_EMBEDDING_DIMENSIONS
                  </code>{' '}
                  - Embedding dimensions
                </li>
                <li>
                  <code className="bg-surface-strong px-1 py-0.5 rounded">
                    MDDB_EMBEDDING_API_KEY
                  </code>{' '}
                  - API key (OpenAI only)
                </li>
                <li>
                  <code className="bg-surface-strong px-1 py-0.5 rounded">
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
    <div className="h-full flex flex-col bg-surface-muted">
      <div className="bg-surface border-b px-6 py-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-fg mb-1">Vector Search</h1>
            <p className="text-fg-subtle">Manage vector embeddings and search</p>
          </div>
          <button
            onClick={() => loadStats()}
            className="px-4 py-2 bg-surface-subtle text-fg-muted rounded hover:bg-surface-strong flex items-center gap-2"
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
          <div className="bg-surface rounded-lg shadow p-6">
            <h2 className="text-lg font-semibold text-fg mb-4 flex items-center gap-2">
              <Zap className="w-5 h-5 text-info" />
              Embedding Provider
            </h2>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              <div>
                <div className="text-sm text-fg-faint mb-1">Provider</div>
                <div className="text-lg font-medium text-fg">
                  {stats.provider || 'Unknown'}
                </div>
              </div>
              <div>
                <div className="text-sm text-fg-faint mb-1">Model</div>
                <div className="text-lg font-medium text-fg">{stats.model || 'N/A'}</div>
              </div>
              <div>
                <div className="text-sm text-fg-faint mb-1">Dimensions</div>
                <div className="text-lg font-medium text-fg">
                  {stats.dimensions || 'N/A'}
                </div>
              </div>
            </div>
          </div>

          {/* Overview Stats */}
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
            <div className="bg-surface rounded-lg shadow p-6">
              <div className="text-sm text-fg-faint mb-1">Total Collections</div>
              <div className="text-3xl font-bold text-fg">{collections.length}</div>
            </div>
            <div className="bg-surface rounded-lg shadow p-6">
              <div className="text-sm text-fg-faint mb-1">Total Documents</div>
              <div className="text-3xl font-bold text-fg">{totalDocs}</div>
            </div>
            <div className="bg-surface rounded-lg shadow p-6">
              <div className="text-sm text-fg-faint mb-1">Indexed Vectors</div>
              <div className="text-3xl font-bold text-info">{totalVectors}</div>
            </div>
          </div>

          {/* Collections */}
          <div className="bg-surface rounded-lg shadow">
            <div className="px-6 py-4 border-b border-line">
              <h2 className="text-lg font-semibold text-fg">Collections</h2>
              <p className="text-sm text-fg-subtle mt-1">
                Manage vector embeddings for each collection
              </p>
              <label className="mt-2 flex items-center gap-2 text-sm text-fg-soft">
                <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} />
                Force re-embed (also documents whose content has not changed)
              </label>
            </div>

            {collections.length === 0 ? (
              <div className="p-12 text-center text-fg-faint">
                <Database className="w-12 h-12 mx-auto mb-2 text-fg-faint" />
                <p>No collections found</p>
              </div>
            ) : (
              <div className="divide-y divide-line">
                {collections.map((collection) => {
                  const docCount = collectionsMap[collection]?.total_documents || 0;
                  const vectorCount = collectionsMap[collection]?.embedded_documents || 0;
                  const coverage = docCount > 0 ? ((vectorCount / docCount) * 100).toFixed(1) : 0;
                  const job = reindex.active[collection];
                  const isReindexing = Boolean(job);
                  const status = reindex.messages[collection];

                  return (
                    <div key={collection} className="p-6">
                      <div className="flex items-start justify-between">
                        <div className="flex-1">
                          <h3 className="text-lg font-medium text-fg mb-2 flex items-center gap-2">
                            {collection}
                            {trainingSet.has(collection) && (
                              <span className="rounded-full bg-warning-muted px-2 py-0.5 text-xs font-medium text-warning-strong">
                                {stats.indexTraining.current === collection ? 'training now' : 'waiting to train'}
                              </span>
                            )}
                          </h3>

                          <div className="grid grid-cols-3 gap-4 mb-3">
                            <div>
                              <div className="text-xs text-fg-faint">Documents</div>
                              <div className="text-sm font-medium text-fg">{docCount}</div>
                            </div>
                            <div>
                              <div className="text-xs text-fg-faint">Indexed</div>
                              <div className="text-sm font-medium text-fg">
                                {vectorCount}
                              </div>
                            </div>
                            <div>
                              <div className="text-xs text-fg-faint">Coverage</div>
                              <div className="text-sm font-medium text-fg">{coverage}%</div>
                            </div>
                          </div>

                          {/* Progress bar */}
                          <div className="w-full bg-surface-strong rounded-full h-2">
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

                          {job && (
                            <p className="mt-3 text-sm text-info-strong" role="status">{describeJob(job)}</p>
                          )}

                          {/* Status message */}
                          {status && (
                            <div
                              className={`mt-3 text-sm flex items-center gap-2 ${
                                status.success ? 'text-success' : 'text-danger'
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
                          onClick={() => reindex.start(collection, force)}
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

          <ReindexJobsTable jobs={reindex.recent} error={reindex.recentError} onRefresh={reindex.loadRecent} />
        </div>
      </div>
    </div>
  );
}
