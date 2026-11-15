import { useState, useEffect, useCallback } from 'react';
import { Key, Plus, Trash2, Ban, AlertCircle } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import OneTimeSecret from './OneTimeSecret';

/**
 * KeyId tells keys apart without showing key material: the scheme marker and
 * the fingerprint (four bytes of SHA-256 of the key, the identifier the server
 * log uses too). A server without fingerprints (before SEC-012) lists its
 * prefix only.
 */
function KeyId({ apiKey }) {
  if (!apiKey.fingerprint) {
    return <span className="ml-2 text-xs text-fg-subtle font-mono">{apiKey.keyPrefix}</span>;
  }
  return (
    <span
      className="ml-2 text-xs text-fg-subtle font-mono"
      title="Fingerprint: four bytes of SHA-256 of the key, as the server log shows it. Not part of the key."
    >
      {apiKey.keyPrefix}… · {apiKey.fingerprint}
    </span>
  );
}

/** One listed key: its name, identifier, state, and the disable and delete buttons. */
function KeyRow({ apiKey, onDisable, onDelete }) {
  return (
    <div className="flex items-center justify-between px-4 py-3">
      <div className="flex-1 min-w-0">
        <span className="font-medium text-sm">{apiKey.name}</span>
        <KeyId apiKey={apiKey} />
        {apiKey.disabled && (
          <span className="ml-2 px-1.5 py-0.5 text-[10px] font-bold rounded bg-danger-muted text-danger">DISABLED</span>
        )}
        {apiKey.expiresAt > 0 && (
          <span className="ml-2 text-xs text-fg-subtle">
            expires {new Date(apiKey.expiresAt * 1000).toLocaleDateString()}
          </span>
        )}
      </div>
      <div className="flex items-center space-x-1">
        {!apiKey.disabled && (
          <button
            onClick={() => { void onDisable({ fingerprint: apiKey.fingerprint }); }}
            title="Disable key"
            aria-label={`Disable key ${apiKey.name}`}
            className="p-1.5 text-fg-subtle hover:text-warning rounded"
          >
            <Ban className="w-4 h-4" aria-hidden="true" />
          </button>
        )}
        <button
          onClick={() => { void onDelete({ fingerprint: apiKey.fingerprint }); }}
          title="Delete key"
          aria-label={`Delete key ${apiKey.name}`}
          className="p-1.5 text-fg-subtle hover:text-danger rounded"
        >
          <Trash2 className="w-4 h-4" aria-hidden="true" />
        </button>
      </div>
    </div>
  );
}

export default function MCPAPIKeysPanel() {
  const [keys, setKeys] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [newKeyName, setNewKeyName] = useState('');
  const [createdKey, setCreatedKey] = useState(null);
  const [creating, setCreating] = useState(false);

  // A promise chain: its state updates land in callbacks, after the request.
  const loadKeys = useCallback(() => mddbClient.listMCPAPIKeys()
    .then((data) => {
      setKeys(data.keys || []);
      setError(null);
    })
    .catch((err) => {
      setError(err.message || 'Failed to load API keys');
      setKeys([]);
    })
    .finally(() => setLoading(false)), []);

  useEffect(() => { loadKeys(); }, [loadKeys]);

  const handleCreate = async () => {
    if (!newKeyName.trim()) return;
    setCreating(true);
    try {
      const data = await mddbClient.createMCPAPIKey(newKeyName.trim());
      setCreatedKey(data.key);
      setNewKeyName('');
      loadKeys();
    } catch (err) {
      setError(err.message);
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (key) => {
    if (!confirm('Delete this API key? This cannot be undone.')) return;
    try {
      await mddbClient.deleteMCPAPIKey(key);
      loadKeys();
    } catch (err) {
      setError(err.message);
    }
  };

  const handleDisable = async (key) => {
    try {
      await mddbClient.disableMCPAPIKey(key);
      loadKeys();
    } catch (err) {
      setError(err.message);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center space-x-2">
        <Key className="w-5 h-5 text-fg-faint" />
        <h3 className="text-lg font-semibold">MCP API Keys</h3>
      </div>

      <p className="text-sm text-fg-faint">
        Manage API keys for MCP endpoint access. Keys are stored in the database and persist across restarts.
      </p>

      {error && (
        <div className="flex items-center space-x-2 p-3 bg-danger-soft text-danger rounded-lg text-sm">
          <AlertCircle className="w-4 h-4 flex-shrink-0" />
          <span>{error}</span>
          <button onClick={() => setError(null)} className="ml-auto text-danger hover:text-danger">&times;</button>
        </div>
      )}

      {/* Create new key */}
      <div className="flex items-center space-x-2">
        <input
          type="text"
          value={newKeyName}
          onChange={(e) => setNewKeyName(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && handleCreate()}
          placeholder="Key name (e.g. claude-prod)"
          className="flex-1 px-3 py-2 border border-line-strong rounded-lg text-sm focus:ring-2 focus:ring-focus focus:border-transparent"
        />
        <button
          onClick={handleCreate}
          disabled={creating || !newKeyName.trim()}
          className="flex items-center space-x-1 px-4 py-2 bg-primary-600 text-white rounded-lg text-sm hover:bg-primary-700 disabled:opacity-50"
        >
          <Plus className="w-4 h-4" />
          <span>{creating ? 'Creating...' : 'Create Key'}</span>
        </button>
      </div>

      {/* Show newly created key (one-time) */}
      {createdKey && <OneTimeSecret secret={createdKey} onDismiss={() => setCreatedKey(null)} />}

      {/* Key list */}
      {loading && <p className="text-sm text-fg-subtle">Loading keys...</p>}
      {!loading && keys.length === 0 && (
        <p className="text-sm text-fg-subtle">No API keys configured. Create one above.</p>
      )}
      {!loading && keys.length > 0 && (
        <div className="border rounded-lg divide-y">
          {keys.map((k, i) => (
            <KeyRow key={k.fingerprint || i} apiKey={k} onDisable={handleDisable} onDelete={handleDelete} />
          ))}
        </div>
      )}

      <p className="text-xs text-fg-subtle">
        Use keys via <code className="bg-surface-subtle px-1 rounded">X-API-Key</code> header
        or <code className="bg-surface-subtle px-1 rounded">Authorization: Bearer</code>.
        Enable with <code className="bg-surface-subtle px-1 rounded">MDDB_MCP_API_KEY_ENABLED=true</code>.
      </p>
    </div>
  );
}
