import { useState } from 'react';
import { X, Plus, AlertCircle } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import MarkdownEditor from './MarkdownEditor';
import TemplatePicker from './TemplatePicker';

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

  // Create is disabled until a key and a language are set, so both are here.
  const handleCreate = async () => {
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
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-surface rounded-lg shadow-xl w-full max-w-2xl max-h-[90vh] flex flex-col">
        {/* Header */}
        <div className="border-b border-line p-4">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-xl font-semibold text-fg">
                New Document
              </h2>
              <p className="text-sm text-fg-faint mt-1">
                Create a new document in {collection}
              </p>
            </div>
            <button
              onClick={onClose}
              className="p-2 text-fg-faint hover:text-fg-subtle rounded-lg hover:bg-surface-subtle"
            >
              <X className="w-5 h-5" />
            </button>
          </div>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto p-6 space-y-6">
          {/* Error Message */}
          {error && (
            <div className="bg-danger-soft border border-danger-line rounded-lg p-4 flex items-start space-x-3">
              <AlertCircle className="w-5 h-5 text-danger flex-shrink-0 mt-0.5" />
              <div>
                <h4 className="text-sm font-medium text-danger-strong">Error creating document</h4>
                <p className="text-sm text-danger mt-1">{error}</p>
              </div>
            </div>
          )}

          {/* Basic Info */}
          <div className="space-y-4">
            <div>
              <label htmlFor="new-document-key" className="block text-sm font-medium text-fg-muted mb-2">
                Document Key *
              </label>
              <input
                id="new-document-key"
                type="text"
                value={key}
                onChange={(e) => setKey(e.target.value)}
                placeholder="e.g., hello-world"
                className="w-full px-3 py-2 border border-line-strong rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-focus"
              />
              <p className="text-xs text-fg-faint mt-1">
                Unique identifier for this document
              </p>
            </div>

            <div>
              <label htmlFor="new-document-lang" className="block text-sm font-medium text-fg-muted mb-2">
                Language *
              </label>
              <select
                id="new-document-lang"
                value={lang}
                onChange={(e) => setLang(e.target.value)}
                className="w-full px-3 py-2 border border-line-strong rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-focus"
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

          {/* FE-018: built-in templates and the team's own from _templates */}
          <TemplatePicker
            onPick={(template) => {
              setContentMd(template.contentMd);
              setMetadata(template.meta);
            }}
          />

          {/* Metadata Section */}
          <div>
            <h3 className="text-sm font-semibold text-fg-muted mb-3">Metadata (Optional)</h3>
            
            {/* Existing Metadata */}
            {Object.keys(metadata).length > 0 && (
              <div className="space-y-2 mb-4">
                {Object.entries(metadata).map(([key, values]) => (
                  <div key={key} className="flex items-center space-x-2">
                    <input
                      type="text"
                      value={key}
                      disabled
                      className="flex-1 px-3 py-2 border border-line-strong rounded-lg text-sm bg-surface-muted"
                    />
                    <input
                      type="text"
                      value={values.join(', ')}
                      disabled
                      className="flex-1 px-3 py-2 border border-line-strong rounded-lg text-sm bg-surface-muted"
                    />
                    <button
                      onClick={() => handleRemoveMetadata(key)}
                      className="px-3 py-2 text-danger hover:bg-danger-soft rounded-lg transition-colors text-sm"
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
                className="flex-1 px-3 py-2 border border-line-strong rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-focus"
              />
              <input
                type="text"
                placeholder="Value"
                value={newMetaValue}
                onChange={(e) => setNewMetaValue(e.target.value)}
                onKeyPress={(e) => e.key === 'Enter' && handleAddMetadata()}
                className="flex-1 px-3 py-2 border border-line-strong rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-focus"
              />
              <button
                onClick={handleAddMetadata}
                className="px-4 py-2 bg-surface-subtle text-fg-muted rounded-lg hover:bg-surface-strong transition-colors text-sm font-medium"
              >
                Add
              </button>
            </div>
          </div>

          {/* Content Section */}
          <div>
            <h3 className="block text-sm font-medium text-fg-muted mb-2">
              Content (Markdown)
            </h3>
            <div className="border border-line-strong rounded-lg overflow-hidden">
              <MarkdownEditor
                value={contentMd}
                onChange={setContentMd}
                onSave={() => { if (!creating && key && lang) void handleCreate(); }}
                placeholder="# New Document&#10;&#10;Start writing your markdown content here..."
              />
            </div>
          </div>
        </div>

        {/* Footer */}
        <div className="border-t border-line p-4 flex items-center justify-end space-x-3">
          <button
            onClick={onClose}
            className="px-4 py-2 text-fg-muted bg-surface-subtle rounded-lg hover:bg-surface-strong transition-colors"
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
