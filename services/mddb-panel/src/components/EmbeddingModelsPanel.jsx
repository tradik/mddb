import { useEffect, useState } from 'react';
import { Brain, Plus, Edit, Trash2, Star, X } from 'lucide-react';
import mddbClient from '../lib/mddb-client';

export default function EmbeddingModelsPanel() {
  const [configs, setConfigs] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [showEditModal, setShowEditModal] = useState(false);
  const [selectedConfig, setSelectedConfig] = useState(null);
  const [vectorStats, setVectorStats] = useState(null);

  useEffect(() => {
    loadConfigs();
    loadVectorStats();
  }, []);

  const loadVectorStats = async () => {
    try {
      const data = await mddbClient.vectorStats();
      setVectorStats(data);
    } catch {
      // Vector stats unavailable - not critical
    }
  };

  const loadConfigs = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await mddbClient.listEmbeddingConfigs();
      setConfigs(data.configs || []);
    } catch (err) {
      setError(err.message);
      console.error('Failed to load embedding configs:', err);
    } finally {
      setLoading(false);
    }
  };

  const handleSetDefault = async (id) => {
    try {
      await mddbClient.setDefaultEmbeddingConfig(id);
      await loadConfigs();
    } catch (err) {
      alert(`Failed to set default: ${err.message}`);
    }
  };

  const handleDelete = async (config) => {
    if (config.isDefault) {
      alert('Cannot delete the default configuration');
      return;
    }

    if (!confirm(`Delete embedding config "${config.name}"?`)) {
      return;
    }

    try {
      await mddbClient.deleteEmbeddingConfig(config.id);
      await loadConfigs();
    } catch (err) {
      alert(`Failed to delete: ${err.message}`);
    }
  };

  const handleImportCurrentConfig = async () => {
    if (!vectorStats || !vectorStats.enabled) {
      alert('No active embedding configuration to import');
      return;
    }

    try {
      const config = {
        id: `${vectorStats.provider}-default`,
        name: `${vectorStats.provider} - ${vectorStats.model}`,
        provider: vectorStats.provider,
        model: vectorStats.model,
        dimensions: vectorStats.dimensions,
        apiKey: '',
        apiUrl: '',
        isDefault: true,
      };

      await mddbClient.createEmbeddingConfig(config);
      await loadConfigs();
      await loadVectorStats();
      alert('Successfully imported current configuration!');
    } catch (err) {
      alert(`Failed to import configuration: ${err.message}`);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-600"></div>
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col bg-gray-50">
      <div className="bg-white border-b px-6 py-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-gray-900 mb-1">Embedding Models</h1>
            <p className="text-gray-600">Configure embedding providers for vector search</p>
          </div>
          <button
            onClick={() => setShowCreateModal(true)}
            className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 flex items-center gap-2"
          >
            <Plus className="w-4 h-4" />
            Add Model
          </button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        {error && (
          <div className="mb-4 p-4 bg-red-50 border border-red-200 rounded-lg">
            <p className="text-red-800">{error}</p>
          </div>
        )}

        {configs.length === 0 ? (
          <div className="bg-white rounded-lg shadow p-12 text-center">
            <Brain className="w-16 h-16 mx-auto mb-4 text-gray-400" />
            {vectorStats && vectorStats.enabled ? (
              <>
                <h3 className="text-lg font-medium text-gray-900 mb-2">Import Current Configuration</h3>
                <p className="text-gray-600 mb-2">
                  You're currently using environment variables for embedding configuration:
                </p>
                <div className="inline-block bg-blue-50 border border-blue-200 rounded-lg px-4 py-2 mb-6">
                  <div className="text-sm text-gray-700">
                    <span className="font-medium capitalize">{vectorStats.provider}</span> - {vectorStats.model}
                  </div>
                  <div className="text-xs text-gray-500">
                    {vectorStats.dimensions} dimensions
                  </div>
                </div>
                <div className="space-x-3">
                  <button
                    onClick={handleImportCurrentConfig}
                    className="px-4 py-2 bg-green-600 text-white rounded hover:bg-green-700"
                  >
                    Import Current Config
                  </button>
                  <button
                    onClick={() => setShowCreateModal(true)}
                    className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700"
                  >
                    Add New Model
                  </button>
                </div>
              </>
            ) : (
              <>
                <h3 className="text-lg font-medium text-gray-900 mb-2">No embedding models configured</h3>
                <p className="text-gray-600 mb-6">
                  Add an embedding provider to enable vector search features
                </p>
                <button
                  onClick={() => setShowCreateModal(true)}
                  className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700"
                >
                  Add Your First Model
                </button>
              </>
            )}
          </div>
        ) : (
          <div className="grid gap-4">
            {configs.map((config) => (
              <div key={config.id} className="bg-white rounded-lg shadow p-6">
                <div className="flex items-start justify-between">
                  <div className="flex-1">
                    <div className="flex items-center gap-2 mb-2">
                      <h3 className="text-lg font-semibold text-gray-900">{config.name}</h3>
                      {config.isDefault && (
                        <span className="px-2 py-0.5 bg-green-100 text-green-800 text-xs font-medium rounded flex items-center gap-1">
                          <Star className="w-3 h-3" />
                          Default
                        </span>
                      )}
                    </div>

                    <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mt-3">
                      <div>
                        <div className="text-xs text-gray-500">Provider</div>
                        <div className="text-sm font-medium text-gray-900 capitalize">{config.provider}</div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-500">Model</div>
                        <div className="text-sm font-medium text-gray-900">{config.model}</div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-500">Dimensions</div>
                        <div className="text-sm font-medium text-gray-900">{config.dimensions}</div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-500">API URL</div>
                        <div className="text-sm font-medium text-gray-900 truncate" title={config.apiUrl}>
                          {config.apiUrl || 'Default'}
                        </div>
                      </div>
                    </div>
                  </div>

                  <div className="flex items-center gap-2 ml-4">
                    {!config.isDefault && (
                      <button
                        onClick={() => handleSetDefault(config.id)}
                        className="p-2 text-gray-600 hover:text-green-600 hover:bg-green-50 rounded"
                        title="Set as default"
                      >
                        <Star className="w-4 h-4" />
                      </button>
                    )}
                    <button
                      onClick={() => {
                        setSelectedConfig(config);
                        setShowEditModal(true);
                      }}
                      className="p-2 text-gray-600 hover:text-blue-600 hover:bg-blue-50 rounded"
                      title="Edit"
                    >
                      <Edit className="w-4 h-4" />
                    </button>
                    <button
                      onClick={() => handleDelete(config)}
                      disabled={config.isDefault}
                      className="p-2 text-gray-600 hover:text-red-600 hover:bg-red-50 rounded disabled:opacity-50 disabled:cursor-not-allowed"
                      title="Delete"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Create Modal */}
      {showCreateModal && (
        <EmbeddingConfigModal
          onClose={() => setShowCreateModal(false)}
          onSave={async (config) => {
            await mddbClient.createEmbeddingConfig(config);
            setShowCreateModal(false);
            await loadConfigs();
          }}
        />
      )}

      {/* Edit Modal */}
      {showEditModal && selectedConfig && (
        <EmbeddingConfigModal
          config={selectedConfig}
          onClose={() => {
            setShowEditModal(false);
            setSelectedConfig(null);
          }}
          onSave={async (config) => {
            await mddbClient.updateEmbeddingConfig(selectedConfig.id, config);
            setShowEditModal(false);
            setSelectedConfig(null);
            await loadConfigs();
          }}
        />
      )}
    </div>
  );
}

// Modal for creating/editing embedding config
function EmbeddingConfigModal({ config, onClose, onSave }) {
  const isEdit = !!config;
  const [id, setId] = useState(config?.id || '');
  const [name, setName] = useState(config?.name || '');
  const [provider, setProvider] = useState(config?.provider || 'openai');
  const [model, setModel] = useState(config?.model || '');
  const [dimensions, setDimensions] = useState(config?.dimensions || '');
  const [apiKey, setApiKey] = useState(config?.apiKey || '');
  const [apiUrl, setApiUrl] = useState(config?.apiUrl || '');
  const [isDefault, setIsDefault] = useState(config?.isDefault || false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  // Set default values based on provider
  useEffect(() => {
    if (!isEdit) {
      if (provider === 'openai') {
        if (!model) setModel('text-embedding-3-small');
        if (!dimensions) setDimensions('1536');
      } else if (provider === 'ollama') {
        if (!model) setModel('nomic-embed-text');
        if (!dimensions) setDimensions('768');
        if (!apiUrl) setApiUrl('http://localhost:11434');
      } else if (provider === 'cohere') {
        if (!model) setModel('embed-english-v3.0');
        if (!dimensions) setDimensions('1024');
        if (!apiUrl) setApiUrl('https://api.cohere.ai/v1');
      } else if (provider === 'voyage') {
        if (!model) setModel('voyage-3');
        if (!dimensions) setDimensions('1024');
        if (!apiUrl) setApiUrl('https://api.voyageai.com/v1');
      } else if (provider === 'lexical') {
        // No model and no network: hashed words and n-grams (SRCH-014).
        if (!model) setModel('lexical-ngram-v1');
        if (!dimensions) setDimensions('512');
      }
    }
  }, [provider, isEdit]);

  const handleSubmit = async (e) => {
    e.preventDefault();
    setError(null);
    setLoading(true);

    try {
      const needsApiKey = ['openai', 'cohere', 'voyage'].includes(provider);
      const configData = {
        id: isEdit ? undefined : id,
        name,
        provider,
        model,
        dimensions: parseInt(dimensions, 10),
        apiKey: needsApiKey ? apiKey : '',
        apiUrl: apiUrl || '',
        isDefault,
      };

      await onSave(configData);
    } catch (err) {
      setError(err.message);
      setLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50 p-4">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-2xl max-h-[90vh] overflow-y-auto">
        <div className="p-6">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-xl font-bold text-gray-900">
              {isEdit ? 'Edit' : 'Add'} Embedding Model
            </h2>
            <button onClick={onClose} className="text-gray-400 hover:text-gray-600" disabled={loading}>
              <X className="w-5 h-5" />
            </button>
          </div>

          <form onSubmit={handleSubmit}>
            {!isEdit && (
              <div className="mb-4">
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  ID <span className="text-red-600">*</span>
                </label>
                <input
                  type="text"
                  value={id}
                  onChange={(e) => setId(e.target.value)}
                  placeholder="e.g., openai-small, ollama-nomic"
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
                  required
                  disabled={loading}
                />
                <p className="text-xs text-gray-500 mt-1">
                  Unique identifier (lowercase, hyphens allowed)
                </p>
              </div>
            )}

            <div className="mb-4">
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Name <span className="text-red-600">*</span>
              </label>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g., OpenAI Small, Ollama Nomic"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
                required
                disabled={loading}
              />
            </div>

            <div className="mb-4">
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Provider <span className="text-red-600">*</span>
              </label>
              <select
                value={provider}
                onChange={(e) => setProvider(e.target.value)}
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
                required
                disabled={loading || isEdit}
              >
                <option value="openai">OpenAI</option>
                <option value="ollama">Ollama (Local)</option>
                <option value="cohere">Cohere</option>
                <option value="voyage">Voyage AI</option>
                <option value="lexical">Lexical (no model, spelling only)</option>
              </select>
              {provider === 'lexical' && (
                <p className="text-xs text-gray-700 mt-1" role="note">
                  Lexical vectors match shared words and word fragments, not meaning. Use it for CI or
                  offline installs; configure a model for search by meaning. Dimensions: 256–2048.
                </p>
              )}
            </div>

            <div className="grid grid-cols-2 gap-4 mb-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  Model <span className="text-red-600">*</span>
                </label>
                <input
                  type="text"
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                  placeholder={
                    provider === 'openai' ? 'text-embedding-3-small' :
                    provider === 'cohere' ? 'embed-english-v3.0' :
                    provider === 'voyage' ? 'voyage-3' :
                    'nomic-embed-text'
                  }
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
                  required
                  disabled={loading}
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  Dimensions <span className="text-red-600">*</span>
                </label>
                <input
                  type="number"
                  value={dimensions}
                  onChange={(e) => setDimensions(e.target.value)}
                  placeholder={
                    provider === 'openai' ? '1536' :
                    provider === 'cohere' ? '1024' :
                    provider === 'voyage' ? '1024' :
                    '768'
                  }
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
                  required
                  min="1"
                  disabled={loading}
                />
              </div>
            </div>

            {/* API Key for providers that need it */}
            {(provider === 'openai' || provider === 'cohere' || provider === 'voyage') && (
              <div className="mb-4">
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  API Key <span className="text-red-600">*</span>
                </label>
                <input
                  type="password"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  placeholder={
                    provider === 'openai' ? 'sk-...' :
                    provider === 'cohere' ? 'cohere_api_key...' :
                    'pa-...'
                  }
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
                  required
                  disabled={loading}
                />
              </div>
            )}

            {/* API URL - required for Ollama, optional for others, none for lexical */}
            {provider !== 'lexical' && (
            <div className="mb-4">
              <label className="block text-sm font-medium text-gray-700 mb-1">
                API URL {provider === 'ollama' && <span className="text-red-600">*</span>}
              </label>
              <input
                type="url"
                value={apiUrl}
                onChange={(e) => setApiUrl(e.target.value)}
                placeholder={
                  provider === 'openai' ? 'https://api.openai.com/v1' :
                  provider === 'cohere' ? 'https://api.cohere.ai/v1' :
                  provider === 'voyage' ? 'https://api.voyageai.com/v1' :
                  'http://localhost:11434'
                }
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
                required={provider === 'ollama'}
                disabled={loading}
              />
              <p className="text-xs text-gray-500 mt-1">
                {provider === 'ollama' ? 'Ollama server URL' : 'Optional: Leave empty to use default'}
              </p>
            </div>
            )}

            <div className="mb-4 flex items-center">
              <input
                type="checkbox"
                id="isDefault"
                checked={isDefault}
                onChange={(e) => setIsDefault(e.target.checked)}
                className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                disabled={loading}
              />
              <label htmlFor="isDefault" className="ml-2 block text-sm text-gray-700">
                Set as default embedding model
              </label>
            </div>

            {error && (
              <div className="mb-4 p-3 bg-red-50 border border-red-200 rounded-lg">
                <p className="text-sm text-red-800">{error}</p>
              </div>
            )}

            <div className="flex justify-end gap-3">
              <button
                type="button"
                onClick={onClose}
                className="px-4 py-2 text-gray-700 bg-gray-100 hover:bg-gray-200 rounded-lg transition-colors"
                disabled={loading}
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={loading}
                className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {loading ? 'Saving...' : isEdit ? 'Update' : 'Create'}
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>
  );
}
