import { useState } from 'react';
import { X, Plus, AlertCircle, Check } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import MarkdownEditor from './MarkdownEditor';

export default function NewDocumentModal({ collection, onClose, onSuccess }) {
  const [key, setKey] = useState('');
  const [lang, setLang] = useState('en_US');
  const [contentMd, setContentMd] = useState('');
  const [metadata, setMetadata] = useState({});
  const [newMetaKey, setNewMetaKey] = useState('');
  const [newMetaValue, setNewMetaValue] = useState('');
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState(null);

  const handleAddMetadata = () => {
    if (newMetaKey && newMetaValue) {
      setMetadata({
        ...metadata,
        [newMetaKey]: [newMetaValue],
      });
      setNewMetaKey('');
      setNewMetaValue('');
    }
  };

  const handleRemoveMetadata = (key) => {
    const updated = { ...metadata };
    delete updated[key];
    setMetadata(updated);
  };

  const handleCreate = async () => {
    if (!key || !lang) {
      setError('Key and language are required');
      return;
    }

    setCreating(true);
    setError(null);

    try {
      await mddbClient.addDocument({
        collection,
        key,
        lang,
        meta: metadata,
        contentMd: contentMd || '# New Document\n\nStart writing here...',
      });

      if (onSuccess) onSuccess();
      onClose();
    } catch (err) {
      setError(err.message);
    } finally {
      setCreating(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-2xl max-h-[90vh] flex flex-col">
        {/* Header */}
        <div className="border-b border-gray-200 p-4">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-xl font-semibold text-gray-900">
                New Document
              </h2>
              <p className="text-sm text-gray-500 mt-1">
                Create a new document in {collection}
              </p>
            </div>
            <button
              onClick={onClose}
              className="p-2 text-gray-400 hover:text-gray-600 rounded-lg hover:bg-gray-100"
            >
              <X className="w-5 h-5" />
            </button>
          </div>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto p-6 space-y-6">
          {/* Error Message */}
          {error && (
            <div className="bg-red-50 border border-red-200 rounded-lg p-4 flex items-start space-x-3">
              <AlertCircle className="w-5 h-5 text-red-600 flex-shrink-0 mt-0.5" />
              <div>
                <h4 className="text-sm font-medium text-red-900">Error creating document</h4>
                <p className="text-sm text-red-700 mt-1">{error}</p>
              </div>
            </div>
          )}

          {/* Basic Info */}
          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">
                Document Key *
              </label>
              <input
                type="text"
                value={key}
                onChange={(e) => setKey(e.target.value)}
                placeholder="e.g., hello-world"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
              />
              <p className="text-xs text-gray-500 mt-1">
                Unique identifier for this document
              </p>
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">
                Language *
              </label>
              <select
                value={lang}
                onChange={(e) => setLang(e.target.value)}
                className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
              >
                <option value="en_US">English (US)</option>
                <option value="en_GB">English (GB)</option>
                <option value="pl_PL">Polish</option>
                <option value="de_DE">German</option>
                <option value="fr_FR">French</option>
                <option value="es_ES">Spanish</option>
                <option value="it_IT">Italian</option>
              </select>
            </div>
          </div>

          {/* Metadata Section */}
          <div>
            <h3 className="text-sm font-semibold text-gray-700 mb-3">Metadata (Optional)</h3>
            
            {/* Existing Metadata */}
            {Object.keys(metadata).length > 0 && (
              <div className="space-y-2 mb-4">
                {Object.entries(metadata).map(([key, values]) => (
                  <div key={key} className="flex items-center space-x-2">
                    <input
                      type="text"
                      value={key}
                      disabled
                      className="flex-1 px-3 py-2 border border-gray-300 rounded-lg text-sm bg-gray-50"
                    />
                    <input
                      type="text"
                      value={Array.isArray(values) ? values.join(', ') : values}
                      disabled
                      className="flex-1 px-3 py-2 border border-gray-300 rounded-lg text-sm bg-gray-50"
                    />
                    <button
                      onClick={() => handleRemoveMetadata(key)}
                      className="px-3 py-2 text-red-600 hover:bg-red-50 rounded-lg transition-colors text-sm"
                    >
                      Remove
                    </button>
                  </div>
                ))}
              </div>
            )}

            {/* Add New Metadata */}
            <div className="flex items-center space-x-2">
              <input
                type="text"
                placeholder="Key (e.g., author)"
                value={newMetaKey}
                onChange={(e) => setNewMetaKey(e.target.value)}
                className="flex-1 px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
              />
              <input
                type="text"
                placeholder="Value"
                value={newMetaValue}
                onChange={(e) => setNewMetaValue(e.target.value)}
                onKeyPress={(e) => e.key === 'Enter' && handleAddMetadata()}
                className="flex-1 px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
              />
              <button
                onClick={handleAddMetadata}
                className="px-4 py-2 bg-gray-100 text-gray-700 rounded-lg hover:bg-gray-200 transition-colors text-sm font-medium"
              >
                Add
              </button>
            </div>
          </div>

          {/* Content Section */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-2">
              Content (Markdown)
            </label>
            <div className="border border-gray-300 rounded-lg overflow-hidden">
              <MarkdownEditor
                value={contentMd}
                onChange={setContentMd}
                placeholder="# New Document&#10;&#10;Start writing your markdown content here..."
              />
            </div>
          </div>
        </div>

        {/* Footer */}
        <div className="border-t border-gray-200 p-4 flex items-center justify-end space-x-3">
          <button
            onClick={onClose}
            className="px-4 py-2 text-gray-700 bg-gray-100 rounded-lg hover:bg-gray-200 transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleCreate}
            disabled={creating || !key || !lang}
            className="flex items-center space-x-2 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {creating ? (
              <>
                <div className="animate-spin rounded-full h-4 w-4 border-b-2 border-white"></div>
                <span>Creating...</span>
              </>
            ) : (
              <>
                <Plus className="w-4 h-4" />
                <span>Create Document</span>
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}
