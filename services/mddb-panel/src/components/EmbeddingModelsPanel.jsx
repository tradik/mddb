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
    <div className="h-full flex flex-col bg-surface-muted">
      <div className="bg-surface border-b px-6 py-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-fg mb-1">Embedding Models</h1>
            <p className="text-fg-subtle">Configure embedding providers for vector search</p>
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
          <div className="mb-4 p-4 bg-danger-soft border border-danger-line rounded-lg">
            <p className="text-danger-strong">{error}</p>
          </div>
        )}

        {configs.length === 0 ? (
          <div className="bg-surface rounded-lg shadow p-12 text-center">
            <Brain className="w-16 h-16 mx-auto mb-4 text-fg-faint" />
            {vectorStats && vectorStats.enabled ? (
              <>
                <h3 className="text-lg font-medium text-fg mb-2">Import Current Configuration</h3>
                <p className="text-fg-subtle mb-2">
                  You're currently using environment variables for embedding configuration:
                </p>
                <div className="inline-block bg-info-soft border border-info-line rounded-lg px-4 py-2 mb-6">
                  <div className="text-sm text-fg-muted">
                    <span className="font-medium capitalize">{vectorStats.provider}</span> - {vectorStats.model}
                  </div>
                  <div className="text-xs text-fg-faint">
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
                <h3 className="text-lg font-medium text-fg mb-2">No embedding models configured</h3>
                <p className="text-fg-subtle mb-6">
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
              <div key={config.id} className="bg-surface rounded-lg shadow p-6">
                <div className="flex items-start justify-between">
                  <div className="flex-1">
                    <div className="flex items-center gap-2 mb-2">
                      <h3 className="text-lg font-semibold text-fg">{config.name}</h3>
                      {config.isDefault && (
                        <span className="px-2 py-0.5 bg-success-muted text-success-strong text-xs font-medium rounded flex items-center gap-1">
                          <Star className="w-3 h-3" />
                          Default
                        </span>
                      )}
                    </div>

                    <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mt-3">
                      <div>
                        <div className="text-xs text-fg-faint">Provider</div>
                        <div className="text-sm font-medium text-fg capitalize">{config.provider}</div>
                      </div>
                      <div>
                        <div className="text-xs text-fg-faint">Model</div>
                        <div className="text-sm font-medium text-fg">{config.model}</div>
                      </div>
                      <div>
                        <div className="text-xs text-fg-faint">Dimensions</div>
                        <div className="text-sm font-medium text-fg">{config.dimensions}</div>
                      </div>
                      <div>
                        <div className="text-xs text-fg-faint">API URL</div>
                        <div className="text-sm font-medium text-fg truncate" title={config.apiUrl}>
                          {config.apiUrl || 'Default'}
                        </div>
                      </div>
                    </div>
                  </div>

                  <div className="flex items-center gap-2 ml-4">
                    {!config.isDefault && (
                      <button
                        onClick={() => handleSetDefault(config.id)}
                        className="p-2 text-fg-subtle hover:text-success hover:bg-success-soft rounded"
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
                      className="p-2 text-fg-subtle hover:text-info hover:bg-info-soft rounded"
                      title="Edit"
                    >
                      <Edit className="w-4 h-4" />
                    </button>
                    <button
                      onClick={() => handleDelete(config)}
                      disabled={config.isDefault}
                      className="p-2 text-fg-subtle hover:text-danger hover:bg-danger-soft rounded disabled:opacity-50 disabled:cursor-not-allowed"
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
