import { useState, useEffect } from 'react';
import { X, Save, AlertCircle, Check } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import MarkdownEditor from './MarkdownEditor';

export default function DocumentEditor({ document, onClose, onSave }) {
  const [contentMd, setContentMd] = useState(document?.contentMd || '');
  const [metadata, setMetadata] = useState(document?.meta || {});
  const [newMetaKey, setNewMetaKey] = useState('');
  const [newMetaValue, setNewMetaValue] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);
  const [success, setSuccess] = useState(false);

  useEffect(() => {
    if (document) {
      setContentMd(document.contentMd || '');
      setMetadata(document.meta || {});
    }
  }, [document]);

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

  const handleUpdateMetadata = (key, value) => {
    setMetadata({
      ...metadata,
      [key]: [value],
    });
  };

  const handleSave = async () => {
    setSaving(true);
    setError(null);
    setSuccess(false);

    try {
      await mddbClient.addDocument({
        collection: document.collection,
        key: document.key,
        lang: document.lang,
        meta: metadata,
        contentMd: contentMd,
      });

      setSuccess(true);
      setTimeout(() => {
        setSuccess(false);
        if (onSave) onSave();
      }, 1500);
    } catch (err) {
      setError(err.message);
    } finally {
      setSaving(false);
    }
  };

  if (!document) return null;

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-4xl max-h-[90vh] flex flex-col">
        {/* Header */}
        <div className="border-b border-gray-200 p-4">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-xl font-semibold text-gray-900">
                Edit Document
              </h2>
              <p className="text-sm text-gray-500 mt-1">
                {document.collection} / {document.key} ({document.lang})
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
                <h4 className="text-sm font-medium text-red-900">Error saving document</h4>
                <p className="text-sm text-red-700 mt-1">{error}</p>
              </div>
            </div>
          )}

          {/* Success Message */}
          {success && (
            <div className="bg-green-50 border border-green-200 rounded-lg p-4 flex items-center space-x-3">
              <Check className="w-5 h-5 text-green-600" />
              <p className="text-sm font-medium text-green-900">Document saved successfully!</p>
            </div>
          )}

          {/* Metadata Section */}
          <div>
            <h3 className="text-sm font-semibold text-gray-700 mb-3">Metadata</h3>
            
            {/* Existing Metadata */}
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
                    onChange={(e) => handleUpdateMetadata(key, e.target.value)}
                    className="flex-1 px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
                  />
                  <button
                    onClick={() => handleRemoveMetadata(key)}
                    className="px-3 py-2 text-red-600 hover:bg-red-50 rounded-lg transition-colors"
                  >
                    Remove
                  </button>
                </div>
              ))}
            </div>

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
                className="px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors text-sm font-medium"
              >
                Add
              </button>
            </div>
          </div>

          {/* Content Section */}
          <div>
            <h3 className="text-sm font-semibold text-gray-700 mb-3">Content (Markdown)</h3>
            <div className="border border-gray-300 rounded-lg overflow-hidden">
              <MarkdownEditor
                value={contentMd}
                onChange={setContentMd}
                placeholder="Enter markdown content..."
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
            onClick={handleSave}
            disabled={saving}
            className="flex items-center space-x-2 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {saving ? (
              <>
                <div className="animate-spin rounded-full h-4 w-4 border-b-2 border-white"></div>
                <span>Saving...</span>
              </>
            ) : (
              <>
                <Save className="w-4 h-4" />
                <span>Save Changes</span>
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}
