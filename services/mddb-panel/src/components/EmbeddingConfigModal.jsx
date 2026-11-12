/**
 * The form that creates or edits one embedding configuration.
 *
 * Changing the provider of a new configuration resets the model, dimensions
 * and API URL to that provider's defaults unless the user typed them (#415,
 * lib/embedding-defaults.js). A saved configuration keeps its provider.
 */
import { useState } from 'react';
import { X } from 'lucide-react';
import { defaultsFor, initialFields, needsApiKey, switchProvider } from '../lib/embedding-defaults';

const INPUT = 'w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500';
const LABEL = 'block text-sm font-medium text-gray-700 mb-1';
const Required = () => <span className="text-red-600">*</span>;

/** The fields of a saved configuration, or a new one's defaults. */
function startFields(config) {
  if (!config) return { id: '', name: '', apiKey: '', isDefault: false, ...initialFields('openai') };
  return {
    id: config.id || '', name: config.name || '', model: config.model || '', dimensions: config.dimensions || '',
    apiKey: config.apiKey || '', apiUrl: config.apiUrl || '', isDefault: Boolean(config.isDefault),
  };
}

export default function EmbeddingConfigModal({ config, onClose, onSave }) {
  const isEdit = Boolean(config);
  const [provider, setProvider] = useState(config?.provider || 'openai');
  const [fields, setFields] = useState(() => startFields(config));
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const hints = defaultsFor(provider);
  const set = (key) => (e) => setFields((f) => ({ ...f, [key]: e.target.type === 'checkbox' ? e.target.checked : e.target.value }));

  const changeProvider = (e) => {
    const next = e.target.value;
    setFields((f) => switchProvider(f, provider, next));
    setProvider(next);
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      await onSave({
        id: isEdit ? undefined : fields.id,
        name: fields.name,
        provider,
        model: fields.model,
        dimensions: Number.parseInt(fields.dimensions, 10),
        apiKey: needsApiKey(provider) ? fields.apiKey : '',
        apiUrl: fields.apiUrl || '',
        isDefault: fields.isDefault,
      });
    } catch (err) {
      setError(err.message);
      setLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50 p-4">
      <div role="dialog" aria-modal="true" aria-labelledby="embedding-config-title"
        className="bg-white rounded-lg shadow-xl w-full max-w-2xl max-h-[90vh] overflow-y-auto">
        <div className="p-6">
          <div className="flex items-center justify-between mb-4">
            <h2 id="embedding-config-title" className="text-xl font-bold text-gray-900">
              {isEdit ? 'Edit' : 'Add'} Embedding Model
            </h2>
            <button type="button" onClick={onClose} aria-label="Close" className="text-gray-500 hover:text-gray-700" disabled={loading}>
              <X className="w-5 h-5" />
            </button>
          </div>

          <form onSubmit={handleSubmit}>
            {!isEdit && (
              <div className="mb-4">
                <label htmlFor="emb-id" className={LABEL}>ID <Required /></label>
                <input id="emb-id" type="text" value={fields.id} onChange={set('id')} placeholder="e.g., openai-small, ollama-nomic"
                  className={INPUT} required disabled={loading} />
                <p className="text-xs text-gray-600 mt-1">Unique identifier (lowercase, hyphens allowed)</p>
              </div>
            )}

            <div className="mb-4">
              <label htmlFor="emb-name" className={LABEL}>Name <Required /></label>
              <input id="emb-name" type="text" value={fields.name} onChange={set('name')} placeholder="e.g., OpenAI Small, Ollama Nomic"
                className={INPUT} required disabled={loading} />
            </div>

            <div className="mb-4">
              <label htmlFor="emb-provider" className={LABEL}>Provider <Required /></label>
              <select id="emb-provider" value={provider} onChange={changeProvider} className={INPUT} required disabled={loading || isEdit}>
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
                <label htmlFor="emb-model" className={LABEL}>Model <Required /></label>
                <input id="emb-model" type="text" value={fields.model} onChange={set('model')} placeholder={hints.model}
                  className={INPUT} required disabled={loading} />
              </div>
              <div>
                <label htmlFor="emb-dimensions" className={LABEL}>Dimensions <Required /></label>
                <input id="emb-dimensions" type="number" value={fields.dimensions} onChange={set('dimensions')} placeholder={hints.dimensions}
                  className={INPUT} required min="1" disabled={loading} />
              </div>
            </div>

            {needsApiKey(provider) && (
              <div className="mb-4">
                <label htmlFor="emb-key" className={LABEL}>API Key <Required /></label>
                <input id="emb-key" type="password" value={fields.apiKey} onChange={set('apiKey')} placeholder={hints.keyHint}
                  className={INPUT} required disabled={loading} />
              </div>
            )}

            {provider !== 'lexical' && (
              <div className="mb-4">
                <label htmlFor="emb-url" className={LABEL}>API URL {provider === 'ollama' && <Required />}</label>
                <input id="emb-url" type="url" value={fields.apiUrl} onChange={set('apiUrl')} placeholder={hints.urlHint}
                  className={INPUT} required={provider === 'ollama'} disabled={loading} />
                <p className="text-xs text-gray-600 mt-1">
                  {provider === 'ollama' ? 'Ollama server URL, as mddbd reaches it (not localhost when mddbd runs in a container)' : 'Optional: Leave empty to use default'}
                </p>
              </div>
            )}

            <div className="mb-4 flex items-center">
              <input type="checkbox" id="isDefault" checked={fields.isDefault} onChange={set('isDefault')}
                className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded" disabled={loading} />
              <label htmlFor="isDefault" className="ml-2 block text-sm text-gray-700">Set as default embedding model</label>
            </div>

            {error && (
              <div role="alert" className="mb-4 p-3 bg-red-50 border border-red-200 rounded-lg">
                <p className="text-sm text-red-800">{error}</p>
              </div>
            )}

            <div className="flex justify-end gap-3">
              <button type="button" onClick={onClose} disabled={loading}
                className="px-4 py-2 text-gray-700 bg-gray-100 hover:bg-gray-200 rounded-lg transition-colors">Cancel</button>
              <button type="submit" disabled={loading}
                className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors">
                {loading ? 'Saving...' : isEdit ? 'Update' : 'Create'}
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>
  );
}
