import { useState, useEffect } from 'react';
import {
  Zap,
  Clock,
  Webhook,
  Plus,
  Trash2,
  Edit3,
  Play,
  RefreshCw,
  ToggleLeft,
  ToggleRight,
  ChevronDown,
} from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import AutomationLogsTab from './AutomationLogsTab';
import CronActionFields from './CronActionFields';
import { EMPTY_RULE, buildPayload, ruleToForm, describeCronAction } from '../lib/automation-form';

const TYPE_CONFIG = {
  webhook: { icon: Webhook, label: 'Webhook', badgeClass: 'bg-blue-100 text-blue-800' },
  trigger: { icon: Zap, label: 'Trigger', badgeClass: 'bg-amber-100 text-amber-800' },
  cron: { icon: Clock, label: 'Cron', badgeClass: 'bg-purple-100 text-purple-800' },
};

const EVENT_OPTIONS = [
  { value: 'insert', label: 'INSERT (new doc)' },
  { value: 'update', label: 'UPDATE (doc changed)' },
  { value: 'delete', label: 'DELETE (doc removed)' },
];

export default function AutomationPanel() {
  const { stats } = useStore();
  const config = useStore(s => s.config);
  const collections = stats?.collections || [];
  const [activeTab, setActiveTab] = useState('rules');
  const logsEnabled = config?.automationLogsEnabled !== false;

  // Data state
  const [rules, setRules] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [filterType, setFilterType] = useState('');

  // Separate state for all rules (not affected by tab filter) — used for dropdowns and tab counts
  const [allRules, setAllRules] = useState([]);
  const [allWebhooks, setAllWebhooks] = useState([]);

  // Add form state
  const [showAddForm, setShowAddForm] = useState(false);
  const [newRule, setNewRule] = useState({ ...EMPTY_RULE });
  const [adding, setAdding] = useState(false);

  // Edit state
  const [editingId, setEditingId] = useState(null);
  const [editRule, setEditRule] = useState({ ...EMPTY_RULE });
  const [saving, setSaving] = useState(false);

  // Delete / toggle state
  const [deleting, setDeleting] = useState(null);
  const [toggling, setToggling] = useState(null);

  // Test state
  const [testing, setTesting] = useState(null);
  const [testResults, setTestResults] = useState(null);

  useEffect(() => {
    loadRules();
  }, [filterType]);

  useEffect(() => {
    loadDropdownData();
  }, []);

  const loadRules = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await mddbClient.listAutomation(filterType || undefined);
      setRules(Array.isArray(data) ? data : data.rules || []);
    } catch (err) {
      setError(err.message);
      console.error('Failed to load automation rules:', err);
    } finally {
      setLoading(false);
    }
  };

  const loadDropdownData = async () => {
    try {
      const data = await mddbClient.listAutomation();
      const all = Array.isArray(data) ? data : data.rules || [];
      setAllRules(all);
      setAllWebhooks(all.filter((r) => r.type === 'webhook'));
    } catch (err) {
      console.error('Failed to load dropdown data:', err);
    }
  };

  // ---- Counts for filter tabs (always from full unfiltered list) ----
  const allCount = allRules.length;
  const webhookCount = allRules.filter((r) => r.type === 'webhook').length;
  const triggerCount = allRules.filter((r) => r.type === 'trigger').length;
  const cronCount = allRules.filter((r) => r.type === 'cron').length;

  // ---- CRUD handlers ----
  const handleCreate = async (e) => {
    e.preventDefault();
    if (!newRule.name.trim()) return;
    setAdding(true);
    setError(null);
    try {
      await mddbClient.createAutomation(buildPayload(newRule));
      setNewRule({ ...EMPTY_RULE });
      setShowAddForm(false);
      await loadRules();
      loadDropdownData();
    } catch (err) {
      setError(err.message);
    } finally {
      setAdding(false);
    }
  };

  const handleStartEdit = (rule) => {
    setEditingId(rule.id);
    setEditRule(ruleToForm(rule));
  };

  const handleCancelEdit = () => {
    setEditingId(null);
    setEditRule({ ...EMPTY_RULE });
  };

  const handleSaveEdit = async (id) => {
    setSaving(true);
    setError(null);
    try {
      await mddbClient.updateAutomation(id, buildPayload(editRule));
      setEditingId(null);
      setEditRule({ ...EMPTY_RULE });
      await loadRules();
      loadDropdownData();
    } catch (err) {
      setError(err.message);
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (id, name) => {
    if (!confirm(`Delete automation rule "${name}"?`)) return;
    setDeleting(id);
    setError(null);
    try {
      await mddbClient.deleteAutomation(id);
      await loadRules();
      loadDropdownData();
    } catch (err) {
      setError(err.message);
    } finally {
      setDeleting(null);
    }
  };

  const handleToggle = async (rule) => {
    setToggling(rule.id);
    setError(null);
    try {
      await mddbClient.updateAutomation(rule.id, { ...rule, enabled: !rule.enabled });
      await loadRules();
    } catch (err) {
      setError(err.message);
    } finally {
      setToggling(null);
    }
  };

  const handleTest = async (id) => {
    setTesting(id);
    setTestResults(null);
    setError(null);
    try {
      const result = await mddbClient.testAutomation(id);
      setTestResults({ id, result });
    } catch (err) {
      setError(err.message);
    } finally {
      setTesting(null);
    }
  };

  // ---- Render helpers ----
  const FilterTab = ({ type, label, count }) => (
    <button
      onClick={() => setFilterType(type)}
      className={`px-4 py-2 text-sm font-medium rounded-lg transition-colors ${
        filterType === type
          ? 'bg-blue-100 text-blue-700'
          : 'text-gray-600 hover:bg-gray-100'
      }`}
    >
      {label} ({count})
    </button>
  );

  const TypeBadge = ({ type }) => {
    const cfg = TYPE_CONFIG[type];
    if (!cfg) return null;
    return (
      <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${cfg.badgeClass}`}>
        {cfg.label}
      </span>
    );
  };

  const TypeIcon = ({ type }) => {
    const cfg = TYPE_CONFIG[type];
    if (!cfg) return null;
    const Icon = cfg.icon;
    return <Icon className="w-4 h-4 text-gray-500" />;
  };

  // ---- Dynamic type-specific fields ----
  const renderTypeFields = (form, setForm) => {
    if (form.type === 'webhook') {
      return (
        <>
          <div>
            <label className="block text-xs text-gray-500 mb-1">URL</label>
            <input
              type="url"
              value={form.url}
              onChange={(e) => setForm({ ...form, url: e.target.value })}
              placeholder="https://example.com/hook"
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">Method</label>
            <select
              value={form.method}
              onChange={(e) => setForm({ ...form, method: e.target.value })}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
            >
              <option value="POST">POST</option>
              <option value="GET">GET</option>
              <option value="PUT">PUT</option>
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">Headers (JSON)</label>
            <textarea
              value={form.headers}
              onChange={(e) => setForm({ ...form, headers: e.target.value })}
              placeholder={'{"Content-Type": "application/json"}'}
              rows={3}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm font-mono"
            />
          </div>
          <div className="md:col-span-2">
            <details className="text-xs text-gray-500">
              <summary className="cursor-pointer text-blue-600 hover:text-blue-800 font-medium select-none">
                Template Variables
              </summary>
              <div className="mt-2 bg-gray-50 border border-gray-200 rounded-lg p-3 space-y-2">
                <p>
                  Use <code className="bg-gray-200 px-1 rounded text-gray-700">{'{{variable}}'}</code> in URL or header values. Variables are replaced when the webhook fires.
                </p>
                <div className="grid grid-cols-2 gap-x-4 gap-y-0.5">
                  <div className="font-semibold mt-1 col-span-2 text-gray-700">Trigger Variables</div>
                  <code className="text-gray-600">{'{{doc.id}}'}</code><span>Document ID</span>
                  <code className="text-gray-600">{'{{doc.key}}'}</code><span>Document key</span>
                  <code className="text-gray-600">{'{{doc.lang}}'}</code><span>Document language</span>
                  <code className="text-gray-600">{'{{doc.addedAt}}'}</code><span>Added timestamp</span>
                  <code className="text-gray-600">{'{{doc.updatedAt}}'}</code><span>Updated timestamp</span>
                  <code className="text-gray-600">{'{{doc.meta.FIELD}}'}</code><span>Meta field value</span>
                  <code className="text-gray-600">{'{{collection}}'}</code><span>Collection name</span>
                  <code className="text-gray-600">{'{{trigger.id}}'}</code><span>Trigger ID</span>
                  <code className="text-gray-600">{'{{trigger.name}}'}</code><span>Trigger name</span>
                  <code className="text-gray-600">{'{{score}}'}</code><span>Search score</span>
                  <code className="text-gray-600">{'{{sentiment}}'}</code><span>Sentiment score</span>
                  <code className="text-gray-600">{'{{timestamp}}'}</code><span>Unix timestamp</span>
                  <code className="text-gray-600">{'{{webhook.id}}'}</code><span>Webhook ID</span>
                  <code className="text-gray-600">{'{{event}}'}</code><span>Event type</span>
                  <div className="font-semibold mt-1 col-span-2 text-gray-700">Cron Variables</div>
                  <code className="text-gray-600">{'{{cron.id}}'}</code><span>Cron ID</span>
                  <code className="text-gray-600">{'{{cron.name}}'}</code><span>Cron name</span>
                  <code className="text-gray-600">{'{{timestamp}}'}</code><span>Unix timestamp</span>
                  <code className="text-gray-600">{'{{webhook.id}}'}</code><span>Webhook ID</span>
                  <code className="text-gray-600">{'{{event}}'}</code><span>Event type</span>
                </div>
              </div>
            </details>
          </div>
        </>
      );
    }

    if (form.type === 'trigger') {
      return (
        <>
          {/* ── WHEN ── */}
          <div className="md:col-span-2 bg-gray-50 rounded-lg p-4 space-y-3">
            <div className="text-xs font-semibold text-gray-400 uppercase tracking-wider">When</div>
            <div className="flex flex-wrap items-center gap-4">
              <div className="flex flex-wrap gap-3">
                {EVENT_OPTIONS.map((ev) => (
                  <label key={ev.value} className="flex items-center gap-1.5 text-sm">
                    <input
                      type="checkbox"
                      checked={(form.events || []).includes(ev.value)}
                      onChange={() => {
                        const current = form.events || [];
                        const next = current.includes(ev.value)
                          ? current.filter((e) => e !== ev.value)
                          : [...current, ev.value];
                        setForm({ ...form, events: next });
                      }}
                      className="w-4 h-4 text-blue-600 rounded border-gray-300 focus:ring-blue-500"
                    />
                    {ev.label}
                  </label>
                ))}
              </div>
              <div className="flex items-center gap-2">
                <span className="text-sm text-gray-500">in</span>
                <select
                  value={form.collection}
                  onChange={(e) => setForm({ ...form, collection: e.target.value })}
                  className="px-3 py-1.5 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                >
                  <option value="">Select collection</option>
                  {collections.map((c) => (
                    <option key={c.name} value={c.name}>
                      {c.name}
                    </option>
                  ))}
                </select>
              </div>
            </div>
          </div>

          {/* ── IF (optional condition) ── */}
          <div className="md:col-span-2 bg-amber-50 rounded-lg p-4 space-y-3">
            <div className="text-xs font-semibold text-amber-500 uppercase tracking-wider">
              If matches (optional)
            </div>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
              <div>
                <label className="block text-xs text-gray-500 mb-1">Search method</label>
                <select
                  value={form.searchType}
                  onChange={(e) => setForm({ ...form, searchType: e.target.value })}
                  className="w-full px-3 py-1.5 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                >
                  <option value="fts">Full-Text Search</option>
                  <option value="vector">Vector Search</option>
                  <option value="hybrid">Hybrid Search</option>
                </select>
              </div>
              <div>
                <label className="block text-xs text-gray-500 mb-1">Query</label>
                <input
                  type="text"
                  value={form.query}
                  onChange={(e) => setForm({ ...form, query: e.target.value })}
                  placeholder="e.g. breaking news"
                  className="w-full px-3 py-1.5 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                />
              </div>
              <div>
                <label className="block text-xs text-gray-500 mb-1">
                  Score &ge; {form.threshold}%
                </label>
                <input
                  type="range"
                  min={0}
                  max={100}
                  value={form.threshold}
                  onChange={(e) => setForm({ ...form, threshold: parseInt(e.target.value, 10) })}
                  className="w-full mt-1"
                />
              </div>
            </div>
            {/* Sentiment condition */}
            <div className="border-t border-amber-200 pt-3 mt-3">
              <label className="flex items-center gap-2 cursor-pointer mb-2">
                <input
                  type="checkbox"
                  checked={form.sentimentEnabled}
                  onChange={(e) => setForm({ ...form, sentimentEnabled: e.target.checked })}
                  className="w-4 h-4 text-amber-600 rounded border-gray-300 focus:ring-amber-500"
                />
                <span className="text-sm text-gray-700 font-medium">Sentiment condition</span>
              </label>
              {form.sentimentEnabled && (
                <div className="grid grid-cols-1 md:grid-cols-2 gap-3 ml-6">
                  <div>
                    <label className="block text-xs text-gray-500 mb-1">Min score: {form.sentimentMin.toFixed(1)} <span className="text-gray-400">(Negative &minus;1.0)</span></label>
                    <input type="range" min={-100} max={100} value={Math.round(form.sentimentMin * 100)} onChange={(e) => setForm({ ...form, sentimentMin: parseInt(e.target.value, 10) / 100 })} className="w-full" />
                  </div>
                  <div>
                    <label className="block text-xs text-gray-500 mb-1">Max score: {form.sentimentMax.toFixed(1)} <span className="text-gray-400">(Positive 1.0)</span></label>
                    <input type="range" min={-100} max={100} value={Math.round(form.sentimentMax * 100)} onChange={(e) => setForm({ ...form, sentimentMax: parseInt(e.target.value, 10) / 100 })} className="w-full" />
                  </div>
                </div>
              )}
            </div>
            {form.query && form.sentimentEnabled && (
              <div className="border-t border-amber-200 pt-3 mt-3">
                <span className="text-xs text-gray-500 mr-3">Combine conditions:</span>
                <label className="inline-flex items-center gap-1.5 mr-4 text-sm">
                  <input type="radio" value="and" checked={form.conditionLogic === 'and'} onChange={() => setForm({ ...form, conditionLogic: 'and' })} className="w-4 h-4 text-amber-600" />
                  AND (both must match)
                </label>
                <label className="inline-flex items-center gap-1.5 text-sm">
                  <input type="radio" value="or" checked={form.conditionLogic === 'or'} onChange={() => setForm({ ...form, conditionLogic: 'or' })} className="w-4 h-4 text-amber-600" />
                  OR (either must match)
                </label>
              </div>
            )}
            <p className="text-xs text-gray-400">
              Leave query empty and sentiment unchecked to fire on every matching event.
            </p>
          </div>

          {/* ── THEN ── */}
          <div className="md:col-span-2 bg-blue-50 rounded-lg p-4 space-y-3">
            <div className="text-xs font-semibold text-blue-500 uppercase tracking-wider">Then call</div>
            <select
              value={form.webhookId}
              onChange={(e) => setForm({ ...form, webhookId: e.target.value })}
              className="w-full md:w-64 px-3 py-1.5 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
            >
              <option value="">Select webhook</option>
              {allWebhooks.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.name} ({w.method || 'POST'} {w.url})
                </option>
              ))}
            </select>
          </div>
        </>
      );
    }

    // cron: a webhook, a backup (OPS-023) or a sync (INT-022)
    return (
      <CronActionFields form={form} setForm={setForm} webhooks={allWebhooks} collections={collections} />
    );
  };

  return (
    <div className="h-full flex flex-col bg-gray-50">
      {/* Header */}
      <div className="bg-white border-b px-6 py-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-gray-900 mb-1">Automation</h1>
            <p className="text-gray-600">Manage webhooks, triggers, and cron schedules that call a webhook, take a backup or sync a source</p>
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => setShowAddForm(!showAddForm)}
              className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 flex items-center gap-2 text-sm"
            >
              {showAddForm ? (
                <ChevronDown className="w-4 h-4" />
              ) : (
                <Plus className="w-4 h-4" />
              )}
              Add Rule
            </button>
            <button
              onClick={loadRules}
              disabled={loading}
              className="px-4 py-2 bg-gray-100 text-gray-700 rounded hover:bg-gray-200 flex items-center gap-2 disabled:opacity-50"
            >
              <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
              Refresh
            </button>
          </div>
        </div>
      </div>

      {/* Tab bar */}
      <div className="bg-white border-b px-6 flex items-center gap-4">
        <button onClick={() => setActiveTab('rules')} className={`py-3 text-sm font-medium border-b-2 transition-colors ${activeTab === 'rules' ? 'border-blue-600 text-blue-600' : 'border-transparent text-gray-500 hover:text-gray-700'}`}>Rules</button>
        {logsEnabled && <button onClick={() => setActiveTab('logs')} className={`py-3 text-sm font-medium border-b-2 transition-colors ${activeTab === 'logs' ? 'border-blue-600 text-blue-600' : 'border-transparent text-gray-500 hover:text-gray-700'}`}>Logs</button>}
      </div>

      {/* Main content */}
      {activeTab === 'rules' && <div className="flex-1 overflow-y-auto p-6">
        <div className="max-w-6xl mx-auto space-y-6">
          {/* Type filter tabs */}
          <div className="flex items-center gap-2">
            <FilterTab type="" label="All" count={allCount} />
            <FilterTab type="webhook" label="Webhooks" count={webhookCount} />
            <FilterTab type="trigger" label="Triggers" count={triggerCount} />
            <FilterTab type="cron" label="Crons" count={cronCount} />
          </div>

          {/* Error alert */}
          {error && (
            <div className="bg-red-50 border border-red-200 rounded-lg p-4 text-red-700 text-sm">
              {error}
            </div>
          )}

          {/* Add form (expandable) */}
          {showAddForm && (
            <div className="bg-white rounded-lg shadow p-6">
              <h2 className="text-lg font-semibold text-gray-900 mb-4 flex items-center gap-2">
                <Plus className="w-5 h-5 text-blue-600" />
                Add Automation Rule
              </h2>
              <form onSubmit={handleCreate} className="space-y-4">
                <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                  <div>
                    <label className="block text-xs text-gray-500 mb-1">Type</label>
                    <select
                      value={newRule.type}
                      onChange={(e) =>
                        setNewRule({ ...EMPTY_RULE, type: e.target.value, name: newRule.name, enabled: newRule.enabled })
                      }
                      className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                    >
                      <option value="webhook">Webhook</option>
                      <option value="trigger">Trigger</option>
                      <option value="cron">Cron</option>
                    </select>
                  </div>
                  <div>
                    <label className="block text-xs text-gray-500 mb-1">Name</label>
                    <input
                      type="text"
                      value={newRule.name}
                      onChange={(e) => setNewRule({ ...newRule, name: e.target.value })}
                      placeholder="My automation rule"
                      className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                    />
                  </div>
                  <div className="flex items-end">
                    <label className="flex items-center gap-2 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={newRule.enabled}
                        onChange={(e) => setNewRule({ ...newRule, enabled: e.target.checked })}
                        className="w-4 h-4 text-blue-600 rounded border-gray-300 focus:ring-blue-500"
                      />
                      <span className="text-sm text-gray-700">Enabled</span>
                    </label>
                  </div>
                </div>

                {/* Dynamic fields based on type */}
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  {renderTypeFields(newRule, setNewRule)}
                </div>

                <div className="flex items-center gap-2 pt-2">
                  <button
                    type="submit"
                    disabled={adding || !newRule.name.trim()}
                    className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2 text-sm"
                  >
                    {adding ? (
                      <div className="animate-spin rounded-full h-4 w-4 border-2 border-white border-t-transparent"></div>
                    ) : (
                      <Plus className="w-4 h-4" />
                    )}
                    Create
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      setShowAddForm(false);
                      setNewRule({ ...EMPTY_RULE });
                    }}
                    className="px-4 py-2 bg-gray-100 text-gray-700 rounded-lg hover:bg-gray-200 text-sm"
                  >
                    Cancel
                  </button>
                </div>
              </form>
            </div>
          )}

          {/* Rules table */}
          <div className="bg-white rounded-lg shadow">
            <div className="px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold text-gray-900">
                Rules ({rules.length})
              </h2>
            </div>

            {loading ? (
              <div className="flex items-center justify-center py-12">
                <div className="text-center">
                  <div className="animate-spin rounded-full h-10 w-10 border-b-2 border-blue-600 mx-auto mb-3"></div>
                  <p className="text-gray-500 text-sm">Loading automation rules...</p>
                </div>
              </div>
            ) : rules.length === 0 ? (
              <div className="p-12 text-center text-gray-500">
                <p>No automation rules found.</p>
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full">
                  <thead>
                    <tr className="bg-gray-50 border-b border-gray-200">
                      <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase tracking-wider w-10">
                        {/* Icon */}
                      </th>
                      <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase tracking-wider">
                        Name
                      </th>
                      <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase tracking-wider">
                        Type
                      </th>
                      <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase tracking-wider">
                        Status
                      </th>
                      <th className="text-right px-6 py-3 text-xs font-medium text-gray-500 uppercase tracking-wider w-48">
                        Actions
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-200">
                    {rules.map((rule) => (
                      <tr key={rule.id} className="hover:bg-gray-50">
                        {editingId === rule.id ? (
                          /* ---- Inline edit row ---- */
                          <td colSpan={5} className="px-6 py-4">
                            <div className="space-y-4">
                              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                                <div>
                                  <label className="block text-xs text-gray-500 mb-1">Type</label>
                                  <select
                                    value={editRule.type}
                                    onChange={(e) =>
                                      setEditRule({
                                        ...EMPTY_RULE,
                                        type: e.target.value,
                                        name: editRule.name,
                                        enabled: editRule.enabled,
                                      })
                                    }
                                    className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                                  >
                                    <option value="webhook">Webhook</option>
                                    <option value="trigger">Trigger</option>
                                    <option value="cron">Cron</option>
                                  </select>
                                </div>
                                <div>
                                  <label className="block text-xs text-gray-500 mb-1">Name</label>
                                  <input
                                    type="text"
                                    value={editRule.name}
                                    onChange={(e) => setEditRule({ ...editRule, name: e.target.value })}
                                    className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                                  />
                                </div>
                                <div className="flex items-end">
                                  <label className="flex items-center gap-2 cursor-pointer">
                                    <input
                                      type="checkbox"
                                      checked={editRule.enabled}
                                      onChange={(e) => setEditRule({ ...editRule, enabled: e.target.checked })}
                                      className="w-4 h-4 text-blue-600 rounded border-gray-300 focus:ring-blue-500"
                                    />
                                    <span className="text-sm text-gray-700">Enabled</span>
                                  </label>
                                </div>
                              </div>
                              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                                {renderTypeFields(editRule, setEditRule)}
                              </div>
                              <div className="flex items-center gap-2">
                                <button
                                  onClick={() => handleSaveEdit(rule.id)}
                                  disabled={saving}
                                  className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 flex items-center gap-2 text-sm"
                                >
                                  {saving ? (
                                    <div className="animate-spin rounded-full h-4 w-4 border-2 border-white border-t-transparent"></div>
                                  ) : (
                                    <Edit3 className="w-4 h-4" />
                                  )}
                                  Save
                                </button>
                                <button
                                  onClick={handleCancelEdit}
                                  className="px-4 py-2 bg-gray-100 text-gray-700 rounded-lg hover:bg-gray-200 text-sm"
                                >
                                  Cancel
                                </button>
                              </div>
                            </div>
                          </td>
                        ) : (
                          /* ---- Normal display row ---- */
                          <>
                            <td className="px-6 py-4">
                              <TypeIcon type={rule.type} />
                            </td>
                            <td className="px-6 py-4">
                              <div className="text-sm font-medium text-gray-900">{rule.name}</div>
                              {rule.type === 'webhook' && rule.url && (
                                <div className="text-xs text-gray-500 font-mono mt-0.5 truncate max-w-xs">
                                  {rule.method || 'POST'} {rule.url}
                                </div>
                              )}
                              {rule.type === 'trigger' && (
                                <div className="text-xs text-gray-500 mt-0.5">
                                  {rule.collection && <span>{rule.collection}</span>}
                                  {rule.query && <span className="ml-1 font-mono">"{rule.query}"</span>}
                                  {rule.sentimentEnabled && (
                                    <span className="ml-1.5 text-amber-600">
                                      sentiment [{rule.sentimentMin?.toFixed(1)}, {rule.sentimentMax?.toFixed(1)}]
                                    </span>
                                  )}
                                  {rule.query && rule.sentimentEnabled && (
                                    <span className="ml-1 text-gray-400 uppercase text-[10px]">{rule.conditionLogic || 'and'}</span>
                                  )}
                                  {rule.events && rule.events.length > 0 && (
                                    <span className="ml-1.5">
                                      ON {rule.events.map((e) => e.toUpperCase()).join(', ')}
                                    </span>
                                  )}
                                </div>
                              )}
                              {rule.type === 'cron' && (
                                <div className="text-xs text-gray-600 mt-0.5">
                                  <span className="font-mono">{rule.schedule}</span>
                                  <span className="ml-1.5">{describeCronAction(rule)}</span>
                                </div>
                              )}
                            </td>
                            <td className="px-6 py-4">
                              <TypeBadge type={rule.type} />
                            </td>
                            <td className="px-6 py-4">
                              <button
                                onClick={() => handleToggle(rule)}
                                disabled={toggling === rule.id}
                                className="flex items-center gap-1.5 text-sm transition-colors"
                                title={rule.enabled ? 'Click to disable' : 'Click to enable'}
                              >
                                {toggling === rule.id ? (
                                  <div className="animate-spin rounded-full h-4 w-4 border-2 border-gray-400 border-t-transparent"></div>
                                ) : rule.enabled ? (
                                  <ToggleRight className="w-5 h-5 text-green-600" />
                                ) : (
                                  <ToggleLeft className="w-5 h-5 text-gray-400" />
                                )}
                                <span className={rule.enabled ? 'text-green-700' : 'text-gray-500'}>
                                  {rule.enabled ? 'Enabled' : 'Disabled'}
                                </span>
                              </button>
                            </td>
                            <td className="px-6 py-4 text-right whitespace-nowrap">
                              <div className="flex items-center justify-end gap-2">
                                {rule.type === 'trigger' && (
                                  <button
                                    onClick={() => handleTest(rule.id)}
                                    disabled={testing === rule.id}
                                    className="p-1.5 text-green-600 hover:bg-green-50 rounded transition-colors"
                                    title="Test trigger"
                                  >
                                    {testing === rule.id ? (
                                      <div className="animate-spin rounded-full h-4 w-4 border-2 border-green-600 border-t-transparent"></div>
                                    ) : (
                                      <Play className="w-4 h-4" />
                                    )}
                                  </button>
                                )}
                                <button
                                  onClick={() => handleStartEdit(rule)}
                                  className="p-1.5 text-blue-600 hover:bg-blue-50 rounded transition-colors"
                                  title="Edit"
                                >
                                  <Edit3 className="w-4 h-4" />
                                </button>
                                <button
                                  onClick={() => handleDelete(rule.id, rule.name)}
                                  disabled={deleting === rule.id}
                                  className="p-1.5 text-red-500 hover:bg-red-50 rounded transition-colors"
                                  title="Delete"
                                >
                                  {deleting === rule.id ? (
                                    <div className="animate-spin rounded-full h-4 w-4 border-2 border-red-500 border-t-transparent"></div>
                                  ) : (
                                    <Trash2 className="w-4 h-4" />
                                  )}
                                </button>
                              </div>
                            </td>
                          </>
                        )}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>

          {/* Test results */}
          {testResults && (
            <div className="bg-white rounded-lg shadow p-6">
              <div className="flex items-center justify-between mb-4">
                <h2 className="text-lg font-semibold text-gray-900 flex items-center gap-2">
                  <Play className="w-5 h-5 text-green-600" />
                  Test Results
                </h2>
                <button
                  onClick={() => setTestResults(null)}
                  className="text-sm text-gray-500 hover:text-gray-700"
                >
                  Dismiss
                </button>
              </div>
              <pre className="bg-gray-50 border border-gray-200 rounded-lg p-4 text-sm text-gray-800 overflow-x-auto font-mono">
                {JSON.stringify(testResults.result, null, 2)}
              </pre>
            </div>
          )}
        </div>
      </div>}
      {activeTab === 'logs' && logsEnabled && <AutomationLogsTab />}
    </div>
  );
}
