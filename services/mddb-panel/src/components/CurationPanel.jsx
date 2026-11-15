import { useState, useEffect } from 'react';
import { Pin, Trash2, Plus, Save, X, AlertCircle } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';

// CurationPanel (v2.9.14+) — Typesense-style curation rules. For a given
// search query, editors can pin specific documents to fixed positions and
// hide others. Rules are stored server-side and applied in FTS + Hybrid
// responses.
export default function CurationPanel() {
  const { currentCollection } = useStore();
  const [rules, setRules] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [editing, setEditing] = useState(null); // null | rule object
  const [showForm, setShowForm] = useState(false);

  const load = async () => {
    if (!currentCollection) return;
    setLoading(true);
    setError(null);
    try {
      const data = await mddbClient.listCurationRules(currentCollection);
      setRules(data.rules || []);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentCollection]);

  const handleDelete = async (id) => {
    if (!window.confirm('Delete this curation rule?')) return;
    try {
      await mddbClient.deleteCurationRule(id);
      await load();
    } catch (err) {
      setError(err.message);
    }
  };

  const handleEdit = (rule) => {
    setEditing(rule);
    setShowForm(true);
  };

  const handleCreate = () => {
    setEditing(null);
    setShowForm(true);
  };

  if (!currentCollection) {
    return (
      <div className="p-6 text-gray-500 text-sm">
        Select a collection to manage its curation rules.
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      <div className="px-4 py-3 border-b border-gray-200 flex items-center justify-between">
        <div>
          <h3 className="text-lg font-semibold text-gray-900 flex items-center gap-2">
            <Pin className="w-5 h-5 text-amber-600" />
            Curation Rules
          </h3>
          <p className="text-xs text-gray-500 mt-0.5">
            Pin or hide documents for specific queries in <span className="font-medium">{currentCollection}</span>.
          </p>
        </div>
        <button
          onClick={handleCreate}
          className="flex items-center gap-1 px-3 py-1.5 bg-primary-600 text-white rounded-lg hover:bg-primary-700 text-sm"
        >
          <Plus className="w-4 h-4" />
          New Rule
        </button>
      </div>

      {error && (
        <div className="mx-4 mt-3 p-3 bg-red-50 border border-red-200 rounded-lg flex items-start gap-2">
          <AlertCircle className="w-4 h-4 text-red-600 flex-shrink-0 mt-0.5" />
          <p className="text-sm text-red-800">{error}</p>
        </div>
      )}

      <div className="flex-1 overflow-y-auto p-4">
        {loading ? (
          <p className="text-sm text-gray-500">Loading…</p>
        ) : rules.length === 0 ? (
          <p className="text-sm text-gray-400 italic">No curation rules yet. Click &ldquo;New Rule&rdquo; to create one.</p>
        ) : (
          <div className="space-y-3">
            {rules.map((rule) => (
              <CurationRuleRow
                key={rule.id}
                rule={rule}
                onEdit={() => handleEdit(rule)}
                onDelete={() => handleDelete(rule.id)}
              />
            ))}
          </div>
        )}
      </div>

      {showForm && (
        <CurationRuleForm
          rule={editing}
          collection={currentCollection}
          onClose={() => {
            setShowForm(false);
            setEditing(null);
          }}
          onSaved={async () => {
            setShowForm(false);
            setEditing(null);
            await load();
          }}
        />
      )}
    </div>
  );
}

function CurationRuleRow({ rule, onEdit, onDelete }) {
  return (
    <div className="border border-gray-200 rounded-lg p-3 bg-white hover:border-gray-300 transition">
      <div className="flex items-center justify-between mb-1">
        <div className="flex items-center gap-2">
          <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-semibold ${rule.enabled ? 'bg-green-100 text-green-800' : 'bg-gray-200 text-gray-600'}`}>
            {rule.enabled ? 'ENABLED' : 'DISABLED'}
          </span>
          <span className="font-mono text-sm text-gray-900">{rule.query}</span>
          <span className="text-xs text-gray-500">({rule.matchMode || 'exact'})</span>
        </div>
        <div className="flex items-center gap-1">
          <button
            onClick={onEdit}
            className="p-1 text-gray-400 hover:text-primary-600"
            title="Edit"
          >
            <Save className="w-3.5 h-3.5" />
          </button>
          <button
            onClick={onDelete}
            className="p-1 text-gray-400 hover:text-red-600"
            title="Delete"
          >
            <Trash2 className="w-3.5 h-3.5" />
          </button>
        </div>
      </div>
      {rule.pins && rule.pins.length > 0 && (
        <div className="mt-1 text-xs text-gray-600">
          <span className="font-medium">Pins:</span>{' '}
          {rule.pins.map((p) => `#${p.position} ${p.key}`).join(', ')}
        </div>
      )}
      {rule.hides && rule.hides.length > 0 && (
        <div className="mt-1 text-xs text-gray-600">
          <span className="font-medium">Hides:</span> {rule.hides.join(', ')}
        </div>
      )}
    </div>
  );
}

function CurationRuleForm({ rule, collection, onClose, onSaved }) {
  const [query, setQuery] = useState(rule?.query || '');
  const [matchMode, setMatchMode] = useState(rule?.matchMode || 'exact');
  const [enabled, setEnabled] = useState(rule?.enabled !== false);
  const [pins, setPins] = useState(rule?.pins ? [...rule.pins] : []);
  const [hides, setHides] = useState(rule?.hides ? [...rule.hides] : []);
  const [hideInput, setHideInput] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);

  const addPin = () => setPins([...pins, { key: '', lang: '', position: pins.length + 1 }]);
  const updatePin = (idx, field, value) => {
    const next = [...pins];
    next[idx] = { ...next[idx], [field]: field === 'position' ? Number(value) : value };
    setPins(next);
  };
  const removePin = (idx) => setPins(pins.filter((_, i) => i !== idx));

  const addHide = () => {
    const v = hideInput.trim();
    if (v && !hides.includes(v)) {
      setHides([...hides, v]);
      setHideInput('');
    }
  };
  const removeHide = (key) => setHides(hides.filter((h) => h !== key));

  const handleSave = async () => {
    if (!query.trim()) {
      setError('query is required');
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const payload = {
        ...(rule || {}),
        collection,
        query: query.trim(),
        matchMode,
        enabled,
        pins: pins.filter((p) => p.key.trim()),
        hides,
      };
      if (rule?.id) {
        await mddbClient.updateCurationRule(payload);
      } else {
        await mddbClient.createCurationRule(payload);
      }
      onSaved();
    } catch (err) {
      setError(err.message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-xl max-h-[90vh] overflow-y-auto">
        <div className="p-6">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
              <Pin className="w-5 h-5 text-amber-600" />
              {rule?.id ? 'Edit Curation Rule' : 'New Curation Rule'}
            </h2>
            <button onClick={onClose} className="text-gray-400 hover:text-gray-600">
              <X className="w-5 h-5" />
            </button>
          </div>

          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Trigger Query</label>
              <input
                type="text"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="e.g. rust tutorial"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-primary-500 text-sm font-mono"
              />
            </div>

            <div className="flex items-center gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Match Mode</label>
                <select
                  value={matchMode}
                  onChange={(e) => setMatchMode(e.target.value)}
                  className="px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-primary-500 text-sm"
                >
                  <option value="exact">exact</option>
                  <option value="contains">contains</option>
                </select>
              </div>
              <label className="flex items-center gap-2 text-sm text-gray-700 mt-6">
                <input
                  type="checkbox"
                  checked={enabled}
                  onChange={(e) => setEnabled(e.target.checked)}
                  className="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                />
                Enabled
              </label>
            </div>

            {/* Pins */}
            <div>
              <div className="flex items-center justify-between mb-1">
                <label className="block text-sm font-medium text-gray-700">Pinned Documents</label>
                <button
                  onClick={addPin}
                  type="button"
                  className="flex items-center gap-1 text-xs text-primary-600 hover:text-primary-700"
                >
                  <Plus className="w-3 h-3" /> Add
                </button>
              </div>
              {pins.length === 0 ? (
                <p className="text-xs text-gray-400 italic">No pins</p>
              ) : (
                <div className="space-y-2">
                  {pins.map((p, idx) => (
                    <div key={idx} className="flex items-center gap-2">
                      <input
                        type="number"
                        min="0"
                        value={p.position}
                        onChange={(e) => updatePin(idx, 'position', e.target.value)}
                        className="w-16 px-2 py-1.5 border border-gray-300 rounded text-sm"
                        placeholder="#"
                      />
                      <input
                        type="text"
                        value={p.key}
                        onChange={(e) => updatePin(idx, 'key', e.target.value)}
                        placeholder="document key"
                        className="flex-1 px-2 py-1.5 border border-gray-300 rounded text-sm font-mono"
                      />
                      <input
                        type="text"
                        value={p.lang || ''}
                        onChange={(e) => updatePin(idx, 'lang', e.target.value)}
                        placeholder="lang (optional)"
                        className="w-28 px-2 py-1.5 border border-gray-300 rounded text-sm"
                      />
                      <button
                        type="button"
                        onClick={() => removePin(idx)}
                        className="p-1 text-red-400 hover:text-red-600"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>

            {/* Hides */}
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Hidden Documents</label>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={hideInput}
                  onChange={(e) => setHideInput(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && (e.preventDefault(), addHide())}
                  placeholder="document key"
                  className="flex-1 px-2 py-1.5 border border-gray-300 rounded text-sm font-mono"
                />
                <button
                  type="button"
                  onClick={addHide}
                  className="px-3 py-1.5 bg-gray-100 text-gray-700 rounded hover:bg-gray-200 text-sm"
                >
                  Add
                </button>
              </div>
              {hides.length > 0 && (
                <div className="flex flex-wrap gap-1 mt-2">
                  {hides.map((h) => (
                    <span key={h} className="inline-flex items-center gap-1 px-2 py-0.5 bg-red-50 text-red-800 rounded text-xs font-mono">
                      {h}
                      <button type="button" onClick={() => removeHide(h)} className="text-red-600 hover:text-red-800">
                        <X className="w-3 h-3" />
                      </button>
                    </span>
                  ))}
                </div>
              )}
            </div>

            {error && (
              <div className="p-3 bg-red-50 border border-red-200 rounded-lg">
                <p className="text-sm text-red-800">{error}</p>
              </div>
            )}

            <div className="flex gap-3 pt-2">
              <button
                onClick={onClose}
                className="flex-1 px-4 py-2 bg-gray-100 text-gray-700 rounded-lg hover:bg-gray-200"
              >
                Cancel
              </button>
              <button
                onClick={handleSave}
                disabled={saving}
                className="flex-1 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 disabled:opacity-50 flex items-center justify-center gap-2"
              >
                <Save className="w-4 h-4" />
                {saving ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
