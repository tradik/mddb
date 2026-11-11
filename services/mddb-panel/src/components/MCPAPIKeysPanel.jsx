import { useState, useEffect, useCallback } from 'react';
import { Key, Plus, Trash2, Ban, AlertCircle } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import OneTimeSecret from './OneTimeSecret';

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
        <Key className="w-5 h-5 text-gray-500" />
        <h3 className="text-lg font-semibold">MCP API Keys</h3>
      </div>

      <p className="text-sm text-gray-500">
        Manage API keys for MCP endpoint access. Keys are stored in the database and persist across restarts.
      </p>

      {error && (
        <div className="flex items-center space-x-2 p-3 bg-red-50 text-red-700 rounded-lg text-sm">
          <AlertCircle className="w-4 h-4 flex-shrink-0" />
          <span>{error}</span>
          <button onClick={() => setError(null)} className="ml-auto text-red-500 hover:text-red-700">&times;</button>
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
          className="flex-1 px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-transparent"
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
      {loading ? (
        <p className="text-sm text-gray-400">Loading keys...</p>
      ) : keys.length === 0 ? (
        <p className="text-sm text-gray-400">No API keys configured. Create one above.</p>
      ) : (
        <div className="border rounded-lg divide-y">
          {keys.map((k, i) => (
            <div key={i} className="flex items-center justify-between px-4 py-3">
              <div className="flex-1">
                <span className="font-medium text-sm">{k.name}</span>
                <span className="ml-2 text-xs text-gray-400 font-mono">{k.keyPrefix}</span>
                {k.disabled && (
                  <span className="ml-2 px-1.5 py-0.5 text-[10px] font-bold rounded bg-red-100 text-red-600">DISABLED</span>
                )}
                {k.expiresAt > 0 && (
                  <span className="ml-2 text-xs text-gray-400">
                    expires {new Date(k.expiresAt * 1000).toLocaleDateString()}
                  </span>
                )}
              </div>
              <div className="flex items-center space-x-1">
                {!k.disabled && (
                  <button
                    onClick={() => handleDisable(k.keyPrefix.replace('...', ''))}
                    title="Disable key"
                    className="p-1.5 text-gray-400 hover:text-yellow-600 rounded"
                  >
                    <Ban className="w-4 h-4" />
                  </button>
                )}
                <button
                  onClick={() => handleDelete(k.keyPrefix.replace('...', ''))}
                  title="Delete key"
                  className="p-1.5 text-gray-400 hover:text-red-600 rounded"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      <p className="text-xs text-gray-400">
        Use keys via <code className="bg-gray-100 px-1 rounded">X-API-Key</code> header
        or <code className="bg-gray-100 px-1 rounded">Authorization: Bearer</code>.
        Enable with <code className="bg-gray-100 px-1 rounded">MDDB_MCP_API_KEY_ENABLED=true</code>.
      </p>
    </div>
  );
}
