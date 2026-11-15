import { useEffect, useState } from 'react';
import { Webhook, Plus, Trash2, X, Save, AlertCircle } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';

const DOC_EVENTS = ['doc.added', 'doc.updated', 'doc.deleted'];
const INCIDENT_EVENTS = [
  'security.auth_failure_burst',
  'security.rate_limit_exceeded',
  'ops.replication_lag_high',
  'ops.panic_recovered',
  'ops.disk_usage_high',
];

// WebhooksPanel — CRUD UI for outbound webhooks, including ISO 27001 /
// SOC 2 incident notifications (auth bursts, rate-limit breaches, panic
// recovery, disk usage).
export default function WebhooksPanel() {
  const { collectionConfigs } = useStore();
  const [webhooks, setWebhooks] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [showForm, setShowForm] = useState(false);

  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await mddbClient.listWebhooks();
      setWebhooks(Array.isArray(data) ? data : data.webhooks || []);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, []);

  const handleDelete = async (id) => {
    if (!window.confirm('Delete this webhook?')) return;
    try {
      await mddbClient.deleteWebhook(id);
      await load();
    } catch (err) {
      setError(err.message);
    }
  };

  const collectionNames = Object.keys(collectionConfigs || {}).sort((a, b) => a.localeCompare(b));

  return (
    <div className="flex flex-col h-full">
      <div className="px-4 py-3 border-b border-line flex items-center justify-between">
        <div>
          <h3 className="text-lg font-semibold text-fg flex items-center gap-2">
            <Webhook className="w-5 h-5 text-accent" />
            Webhooks
          </h3>
          <p className="text-xs text-fg-faint mt-0.5">
            Outbound HTTP notifications for document and security/ops incident events.
          </p>
        </div>
        <button
          onClick={() => setShowForm(true)}
          className="flex items-center gap-1 px-3 py-1.5 bg-primary-600 text-white rounded-lg hover:bg-primary-700 text-sm"
        >
          <Plus className="w-4 h-4" />
          New Webhook
        </button>
      </div>

      {error && (
        <div className="mx-4 mt-3 p-3 bg-danger-muted border border-danger-line rounded-lg flex items-start gap-2">
          <AlertCircle className="w-4 h-4 text-danger-strong flex-shrink-0 mt-0.5" />
          <p className="text-sm text-danger-strong">{error}</p>
        </div>
      )}

      <div className="flex-1 overflow-y-auto p-4">
        {loading ? (
          <p className="text-sm text-fg-faint">Loading…</p>
        ) : webhooks.length === 0 ? (
          <p className="text-sm text-fg-faint italic">No webhooks registered yet.</p>
        ) : (
          <div className="space-y-3">
            {webhooks.map((w) => (
              <WebhookRow key={w.id} webhook={w} onDelete={() => handleDelete(w.id)} />
            ))}
          </div>
        )}
      </div>

      {showForm && (
        <WebhookForm
          collectionNames={collectionNames}
          onClose={() => setShowForm(false)}
          onSaved={async () => {
            setShowForm(false);
            await load();
          }}
        />
      )}
    </div>
  );
}

function WebhookRow({ webhook, onDelete }) {
  return (
    <div className="border border-line rounded-lg p-3 bg-surface hover:border-line-strong transition">
      <div className="flex items-start justify-between gap-2">
        <div className="flex-1 min-w-0">
          <p className="font-mono text-sm text-fg truncate" title={webhook.url}>
            {webhook.url}
          </p>
          <div className="flex flex-wrap gap-1 mt-1">
            {(webhook.events || []).map((ev) => (
              <span key={ev} className="inline-flex px-1.5 py-0.5 bg-accent-muted text-accent-strong rounded text-[10px] font-mono">
                {ev}
              </span>
            ))}
          </div>
          <p className="text-xs text-fg-faint mt-1">
            Collection: <span className="font-mono">{webhook.collection || 'all'}</span>
            {webhook.createdAt && (
              <> · Created {new Date(webhook.createdAt).toLocaleString()}</>
            )}
          </p>
        </div>
        <button
          onClick={onDelete}
          className="p-1 text-fg-faint hover:text-danger"
          title="Delete"
        >
          <Trash2 className="w-4 h-4" />
        </button>
      </div>
    </div>
  );
}

function WebhookForm({ collectionNames, onClose, onSaved }) {
  const [url, setUrl] = useState('');
  const [events, setEvents] = useState([]);
  const [collection, setCollection] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);

  const toggleEvent = (ev) => {
    setEvents((prev) => (prev.includes(ev) ? prev.filter((e) => e !== ev) : [...prev, ev]));
  };

  const handleSave = async () => {
    if (!url.trim()) {
      setError('URL is required');
      return;
    }
    if (events.length === 0) {
      setError('Select at least one event');
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await mddbClient.registerWebhook({
        url: url.trim(),
        events,
        collection: collection || undefined,
      });
      onSaved();
    } catch (err) {
      setError(err.message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
      <div className="bg-surface rounded-lg shadow-xl w-full max-w-lg max-h-[90vh] overflow-y-auto">
        <div className="p-6">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-xl font-bold text-fg flex items-center gap-2">
              <Webhook className="w-5 h-5 text-accent" />
              New Webhook
            </h2>
            <button onClick={onClose} className="text-fg-faint hover:text-fg-subtle">
              <X className="w-5 h-5" />
            </button>
          </div>

          <div className="space-y-4">
            <div>
              <label htmlFor="webhook-url" className="block text-sm font-medium text-fg-muted mb-1">URL</label>
              <input
                id="webhook-url"
                type="url"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://example.com/hook"
                className="w-full px-3 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus text-sm font-mono"
              />
            </div>

            <EventPickerSection
              title="Document events"
              options={DOC_EVENTS}
              selected={events}
              onToggle={toggleEvent}
            />

            <EventPickerSection
              title="Incident events (ISO 27001 / SOC 2)"
              options={INCIDENT_EVENTS}
              selected={events}
              onToggle={toggleEvent}
            />

            <div>
              <label htmlFor="webhook-collection" className="block text-sm font-medium text-fg-muted mb-1">Collection filter</label>
              <select
                id="webhook-collection"
                value={collection}
                onChange={(e) => setCollection(e.target.value)}
                className="w-full px-3 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus text-sm"
              >
                <option value="">All collections</option>
                {collectionNames.map((c) => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </select>
            </div>

            {error && (
              <div className="p-3 bg-danger-muted border border-danger-line rounded-lg">
                <p className="text-sm text-danger-strong">{error}</p>
              </div>
            )}

            <div className="flex gap-3 pt-2">
              <button
                onClick={onClose}
                className="flex-1 px-4 py-2 bg-surface-subtle text-fg-muted rounded-lg hover:bg-surface-strong"
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

function EventPickerSection({ title, options, selected, onToggle }) {
  return (
    <div>
      <p className="text-sm font-medium text-fg-muted mb-1">{title}</p>
      <div className="space-y-1 border border-line rounded-lg p-2 bg-surface-muted">
        {options.map((ev) => (
          <label key={ev} className="flex items-center gap-2 text-sm text-fg-muted cursor-pointer">
            <input
              type="checkbox"
              checked={selected.includes(ev)}
              onChange={() => onToggle(ev)}
              className="rounded border-line-strong text-accent focus:ring-focus"
            />
            <span className="font-mono text-xs">{ev}</span>
          </label>
        ))}
      </div>
    </div>
  );
}
