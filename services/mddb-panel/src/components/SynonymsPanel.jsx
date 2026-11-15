import { useState, useEffect } from 'react';
import { Pencil, Trash2, Plus, RefreshCw, Check, X } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';

export default function SynonymsPanel() {
  const { stats, currentCollection } = useStore();

  const collections = stats?.collections || [];
  const [collection, setCollection] = useState(currentCollection || collections[0]?.name || '');
  const [entries, setEntries] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  // Add form
  const [newTerm, setNewTerm] = useState('');
  const [newSynonyms, setNewSynonyms] = useState('');
  const [adding, setAdding] = useState(false);

  // Edit state
  const [editingTerm, setEditingTerm] = useState(null);
  const [editSynonyms, setEditSynonyms] = useState('');
  const [saving, setSaving] = useState(false);

  // Delete state
  const [deleting, setDeleting] = useState(null);

  useEffect(() => {
    if (collection) {
      loadSynonyms();
    }
  }, [collection]);

  const loadSynonyms = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await mddbClient.listSynonyms(collection);
      setEntries(data.entries || []);
    } catch (err) {
      setError(err.message);
      console.error('Failed to load synonyms:', err);
    } finally {
      setLoading(false);
    }
  };

  const handleAdd = async (e) => {
    e.preventDefault();
    if (!newTerm.trim() || !newSynonyms.trim()) return;

    setAdding(true);
    setError(null);
    try {
      const synonyms = newSynonyms.split(',').map((s) => s.trim()).filter(Boolean);
      await mddbClient.setSynonym({ collection, term: newTerm.trim(), synonyms });
      setNewTerm('');
      setNewSynonyms('');
      await loadSynonyms();
    } catch (err) {
      setError(err.message);
    } finally {
      setAdding(false);
    }
  };

  const handleEdit = (entry) => {
    setEditingTerm(entry.term);
    setEditSynonyms(entry.synonyms.join(', '));
  };

  const handleCancelEdit = () => {
    setEditingTerm(null);
    setEditSynonyms('');
  };

  const handleSaveEdit = async (term) => {
    setSaving(true);
    setError(null);
    try {
      const synonyms = editSynonyms.split(',').map((s) => s.trim()).filter(Boolean);
      await mddbClient.setSynonym({ collection, term, synonyms });
      setEditingTerm(null);
      setEditSynonyms('');
      await loadSynonyms();
    } catch (err) {
      setError(err.message);
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (term) => {
    if (!confirm(`Delete synonym group for "${term}"?`)) return;

    setDeleting(term);
    setError(null);
    try {
      await mddbClient.deleteSynonym({ collection, term });
      await loadSynonyms();
    } catch (err) {
      setError(err.message);
    } finally {
      setDeleting(null);
    }
  };

  return (
    <div className="h-full flex flex-col bg-surface-muted">
      <div className="bg-surface border-b px-6 py-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-fg mb-1">Synonyms</h1>
            <p className="text-fg-subtle">Manage FTS synonym groups per collection</p>
          </div>
          <button
            onClick={loadSynonyms}
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
          {/* Collection Selector */}
          <div className="bg-surface rounded-lg shadow p-6">
            <label className="block text-sm font-medium text-fg-muted mb-2">Collection</label>
            <select
              value={collection}
              onChange={(e) => setCollection(e.target.value)}
              className="w-full md:w-64 px-3 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus"
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

          {/* Error */}
          {error && (
            <div className="bg-danger-soft border border-danger-line rounded-lg p-4 text-danger text-sm">
              {error}
            </div>
          )}

          {/* Add Form */}
          <div className="bg-surface rounded-lg shadow p-6">
            <h2 className="text-lg font-semibold text-fg mb-4 flex items-center gap-2">
              <Plus className="w-5 h-5 text-info" />
              Add Synonym Group
            </h2>
            <form onSubmit={handleAdd} className="flex flex-col md:flex-row gap-3">
              <div className="flex-1">
                <label className="block text-xs text-fg-faint mb-1">Term</label>
                <input
                  type="text"
                  value={newTerm}
                  onChange={(e) => setNewTerm(e.target.value)}
                  placeholder="e.g. car"
                  className="w-full px-3 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus text-sm"
                />
              </div>
              <div className="flex-[2]">
                <label className="block text-xs text-fg-faint mb-1">Synonyms (comma-separated)</label>
                <input
                  type="text"
                  value={newSynonyms}
                  onChange={(e) => setNewSynonyms(e.target.value)}
                  placeholder="e.g. automobile, vehicle, auto"
                  className="w-full px-3 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus text-sm"
                />
              </div>
              <div className="flex items-end">
                <button
                  type="submit"
                  disabled={adding || !newTerm.trim() || !newSynonyms.trim()}
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

          {/* Synonyms Table */}
          <div className="bg-surface rounded-lg shadow">
            <div className="px-6 py-4 border-b border-line">
              <h2 className="text-lg font-semibold text-fg">
                Synonym Groups ({entries.length})
              </h2>
            </div>

            {loading ? (
              <div className="flex items-center justify-center py-12">
                <div className="text-center">
                  <div className="animate-spin rounded-full h-10 w-10 border-b-2 border-blue-600 mx-auto mb-3"></div>
                  <p className="text-fg-faint text-sm">Loading synonyms...</p>
                </div>
              </div>
            ) : entries.length === 0 ? (
              <div className="p-12 text-center text-fg-faint">
                <p>No synonym groups found for this collection.</p>
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full">
                  <thead>
                    <tr className="bg-surface-muted border-b border-line">
                      <th className="text-left px-6 py-3 text-xs font-medium text-fg-faint uppercase tracking-wider">
                        Term
                      </th>
                      <th className="text-left px-6 py-3 text-xs font-medium text-fg-faint uppercase tracking-wider">
                        Synonyms
                      </th>
                      <th className="text-right px-6 py-3 text-xs font-medium text-fg-faint uppercase tracking-wider w-32">
                        Actions
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line">
                    {entries.map((entry) => (
                      <tr key={entry.term} className="hover:bg-surface-muted">
                        <td className="px-6 py-4 text-sm font-medium text-fg whitespace-nowrap">
                          {entry.term}
                        </td>
                        <td className="px-6 py-4 text-sm text-fg-muted">
                          {editingTerm === entry.term ? (
                            <input
                              type="text"
                              value={editSynonyms}
                              onChange={(e) => setEditSynonyms(e.target.value)}
                              className="w-full px-2 py-1 border border-info-line rounded focus:outline-none focus:ring-2 focus:ring-focus text-sm"
                              autoFocus
                            />
                          ) : (
                            entry.synonyms.join(', ')
                          )}
                        </td>
                        <td className="px-6 py-4 text-right whitespace-nowrap">
                          {editingTerm === entry.term ? (
                            <div className="flex items-center justify-end gap-2">
                              <button
                                onClick={() => handleSaveEdit(entry.term)}
                                disabled={saving}
                                className="p-1.5 text-success hover:bg-success-soft rounded transition-colors"
                                title="Save"
                              >
                                {saving ? (
                                  <div className="animate-spin rounded-full h-4 w-4 border-2 border-green-600 border-t-transparent"></div>
                                ) : (
                                  <Check className="w-4 h-4" />
                                )}
                              </button>
                              <button
                                onClick={handleCancelEdit}
                                className="p-1.5 text-fg-faint hover:bg-surface-subtle rounded transition-colors"
                                title="Cancel"
                              >
                                <X className="w-4 h-4" />
                              </button>
                            </div>
                          ) : (
                            <div className="flex items-center justify-end gap-2">
                              <button
                                onClick={() => handleEdit(entry)}
                                className="p-1.5 text-info hover:bg-info-soft rounded transition-colors"
                                title="Edit"
                              >
                                <Pencil className="w-4 h-4" />
                              </button>
                              <button
                                onClick={() => handleDelete(entry.term)}
                                disabled={deleting === entry.term}
                                className="p-1.5 text-danger hover:bg-danger-soft rounded transition-colors"
                                title="Delete"
                              >
                                {deleting === entry.term ? (
                                  <div className="animate-spin rounded-full h-4 w-4 border-2 border-red-500 border-t-transparent"></div>
                                ) : (
                                  <Trash2 className="w-4 h-4" />
                                )}
                              </button>
                            </div>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
