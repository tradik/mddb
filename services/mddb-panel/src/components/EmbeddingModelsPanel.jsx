import { useEffect, useState } from 'react';
import { Brain, Plus, Edit, Trash2, Star } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import EmbeddingConfigModal from './EmbeddingConfigModal';

/** Embedding Models: the stored embedding configurations and the default. */
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

  // The default configuration's delete button is disabled.
  const handleDelete = async (config) => {
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

  // Shown only while vectorStats.enabled. Since #415 vectorStats.provider is
  // the provider's name (it was the model's, which the server refused).
  const handleImportCurrentConfig = async () => {
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
