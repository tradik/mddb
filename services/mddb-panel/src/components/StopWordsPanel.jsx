import { useState, useEffect } from 'react';
import { Plus, RefreshCw, Trash2, ChevronDown, ChevronRight } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';

export default function StopWordsPanel() {
  const { stats, currentCollection } = useStore();

  const collections = stats?.collections || [];
  const [collection, setCollection] = useState(currentCollection || collections[0]?.name || '');
  const [entries, setEntries] = useState([]);
  const [defaultCount, setDefaultCount] = useState(0);
  const [customCount, setCustomCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  // Language support
  const [lang, setLang] = useState('');
  const [availableLangs, setAvailableLangs] = useState([]);
  const [resolvedLang, setResolvedLang] = useState('en');

  // Add form
  const [newWords, setNewWords] = useState('');
  const [adding, setAdding] = useState(false);

  // Delete state
  const [deleting, setDeleting] = useState(null);

  // Collapsible defaults section
  const [showDefaults, setShowDefaults] = useState(true);

  useEffect(() => {
    mddbClient.ftsLanguages().then((data) => {
      setAvailableLangs(data.languages || []);
    }).catch(() => {});
  }, []);

  useEffect(() => {
    if (collection) {
      loadStopWords();
    }
  }, [collection, lang]);

  const loadStopWords = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await mddbClient.listStopWords(collection, lang || undefined);
      setEntries(data.entries || []);
      setDefaultCount(data.defaults || 0);
      setCustomCount(data.custom || 0);
      setResolvedLang(data.lang || 'en');
    } catch (err) {
      setError(err.message);
      console.error('Failed to load stop words:', err);
    } finally {
      setLoading(false);
    }
  };

  const handleAdd = async (e) => {
    e.preventDefault();
    if (!newWords.trim()) return;

    setAdding(true);
    setError(null);
    try {
      const words = newWords.split(',').map((w) => w.trim()).filter(Boolean);
      await mddbClient.addStopWords({ collection, words });
      setNewWords('');
      await loadStopWords();
    } catch (err) {
      setError(err.message);
    } finally {
      setAdding(false);
    }
  };

  const handleDelete = async (word) => {
    setDeleting(word);
    setError(null);
    try {
      await mddbClient.deleteStopWord({ collection, words: [word] });
      await loadStopWords();
    } catch (err) {
      setError(err.message);
    } finally {
      setDeleting(null);
    }
  };

  const customWords = entries.filter((e) => !e.isDefault);
  const defaultWords = entries.filter((e) => e.isDefault);

  return (
    <div className="h-full flex flex-col bg-surface-muted">
      <div className="bg-surface border-b px-6 py-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-fg mb-1">Stop Words</h1>
            <p className="text-fg-subtle">Manage FTS stop words per collection</p>
          </div>
          <button
            onClick={loadStopWords}
            disabled={!collection || loading}
            className="px-4 py-2 bg-surface-subtle text-fg-muted rounded hover:bg-surface-strong flex items-center gap-2 disabled:opacity-50"
          >
            <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
            Refresh
          </button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        <div className="max-w-6xl mx-auto space-y-6">
          {/* Collection & Language Selector */}
          <div className="bg-surface rounded-lg shadow p-6">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-fg-muted mb-2">Collection</label>
                <select
                  value={collection}
                  onChange={(e) => setCollection(e.target.value)}
                  className="w-full px-3 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus"
                >
                  {collections.length === 0 && (
                    <option value="">No collections available</option>
                  )}
                  {collections.map((c) => (
                    <option key={c.name} value={c.name}>
                      {c.name}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-fg-muted mb-2">Language (defaults)</label>
                <select
                  value={lang}
                  onChange={(e) => setLang(e.target.value)}
                  className="w-full px-3 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus"
                >
                  <option value="">Default</option>
                  {availableLangs.map((l) => (
                    <option key={l.code} value={l.code}>
                      {l.name} ({l.code}) — {l.stopWords} words
                    </option>
                  ))}
                </select>
              </div>
            </div>
          </div>

          {/* Error */}
          {error && (
            <div className="bg-danger-soft border border-danger-line rounded-lg p-4 text-danger text-sm">
              {error}
            </div>
          )}

          {/* Summary Bar */}
          {!loading && collection && (
            <div className="bg-surface rounded-lg shadow p-6">
              <div className="flex items-center gap-6">
                <div className="text-sm text-fg-muted">
                  <span className="font-semibold text-fg">{resolvedLang}</span> language
                </div>
                <div className="text-sm text-fg-muted">
                  <span className="font-semibold text-fg">{defaultCount}</span> defaults
                </div>
                <div className="text-sm text-fg-muted">
                  <span className="font-semibold text-fg">{customCount}</span> custom
                </div>
              </div>
            </div>
          )}

          {/* Add Form */}
          <div className="bg-surface rounded-lg shadow p-6">
            <h2 className="text-lg font-semibold text-fg mb-4 flex items-center gap-2">
              <Plus className="w-5 h-5 text-info" />
              Add Stop Words
            </h2>
            <form onSubmit={handleAdd} className="flex flex-col md:flex-row gap-3">
              <div className="flex-1">
                <label className="block text-xs text-fg-faint mb-1">Words (comma-separated)</label>
                <input
                  type="text"
                  value={newWords}
                  onChange={(e) => setNewWords(e.target.value)}
                  placeholder="e.g. the, a, an, is"
                  className="w-full px-3 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus text-sm"
                />
              </div>
              <div className="flex items-end">
                <button
                  type="submit"
                  disabled={adding || !newWords.trim()}
                  className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2 text-sm"
                >
                  {adding ? (
                    <div className="animate-spin rounded-full h-4 w-4 border-2 border-white border-t-transparent"></div>
                  ) : (
                    <Plus className="w-4 h-4" />
                  )}
                  Add
                </button>
              </div>
            </form>
          </div>

          {loading ? (
            <div className="flex items-center justify-center py-12">
              <div className="text-center">
                <div className="animate-spin rounded-full h-10 w-10 border-b-2 border-blue-600 mx-auto mb-3"></div>
                <p className="text-fg-faint text-sm">Loading stop words...</p>
              </div>
            </div>
          ) : (
            <>
              {/* Custom Stop Words */}
              <div className="bg-surface rounded-lg shadow">
                <div className="px-6 py-4 border-b border-line">
                  <h2 className="text-lg font-semibold text-fg">
                    Custom Stop Words ({customWords.length})
                  </h2>
                </div>
                <div className="p-6">
                  {customWords.length === 0 ? (
                    <p className="text-fg-faint text-sm text-center py-4">
                      No custom stop words for this collection.
                    </p>
                  ) : (
                    <div className="flex flex-wrap gap-2">
                      {customWords.map((entry) => (
                        <span
                          key={entry.word}
                          className="inline-flex items-center gap-1 px-3 py-1 bg-info-muted text-info-strong rounded-full text-sm font-medium"
                        >
                          {entry.word}
                          <button
                            onClick={() => handleDelete(entry.word)}
                            disabled={deleting === entry.word}
                            className="ml-1 p-0.5 text-info hover:text-info-strong hover:bg-info-muted rounded-full transition-colors"
                            title={`Remove "${entry.word}"`}
                          >
                            {deleting === entry.word ? (
                              <div className="animate-spin rounded-full h-3 w-3 border border-blue-600 border-t-transparent"></div>
                            ) : (
                              <Trash2 className="w-3 h-3" />
                            )}
                          </button>
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              </div>

              {/* Default Stop Words (collapsible) */}
              <div className="bg-surface rounded-lg shadow">
                <button
                  onClick={() => setShowDefaults(!showDefaults)}
                  className="w-full px-6 py-4 border-b border-line flex items-center justify-between hover:bg-surface-muted transition-colors"
                >
                  <h2 className="text-lg font-semibold text-fg">
                    Default Stop Words ({defaultWords.length})
                  </h2>
                  {showDefaults ? (
                    <ChevronDown className="w-5 h-5 text-fg-faint" />
                  ) : (
                    <ChevronRight className="w-5 h-5 text-fg-faint" />
                  )}
                </button>
                {showDefaults && (
                  <div className="p-6">
                    {defaultWords.length === 0 ? (
                      <p className="text-fg-faint text-sm text-center py-4">
                        No default stop words.
                      </p>
                    ) : (
                      <div className="flex flex-wrap gap-2">
                        {defaultWords.map((entry) => (
                          <span
                            key={entry.word}
                            className="inline-flex items-center px-3 py-1 bg-surface-subtle text-fg-subtle rounded-full text-sm"
                          >
                            {entry.word}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
