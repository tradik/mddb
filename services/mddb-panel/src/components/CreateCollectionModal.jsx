import { useState } from 'react';
import { X, FolderPlus } from 'lucide-react';

export default function CreateCollectionModal({ onClose, onCreate }) {
  const [collectionName, setCollectionName] = useState('');
  const [error, setError] = useState(null);

  const handleSubmit = (e) => {
    e.preventDefault();
    setError(null);

    const trimmedName = collectionName.trim();

    // Validate collection name
    if (!trimmedName) {
      setError('Collection name is required');
      return;
    }

    // Check for invalid characters
    if (!/^[a-zA-Z0-9_-]+$/.test(trimmedName)) {
      setError('Collection name can only contain letters, numbers, underscores, and hyphens');
      return;
    }

    // Collection name is valid, call onCreate
    onCreate(trimmedName);
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
      <div className="bg-surface rounded-lg shadow-xl w-full max-w-md">
        <div className="p-6">
          <div className="flex items-center justify-between mb-4">
            <div className="flex items-center gap-2">
              <FolderPlus className="w-6 h-6 text-success" />
              <h2 className="text-xl font-bold text-fg">Create Collection</h2>
            </div>
            <button onClick={onClose} className="text-fg-faint hover:text-fg-subtle">
              <X className="w-5 h-5" />
            </button>
          </div>

          <form onSubmit={handleSubmit}>
            <div className="mb-4">
              <label className="block text-sm font-medium text-fg-muted mb-1">
                Collection Name <span className="text-danger">*</span>
              </label>
              <input
                type="text"
                value={collectionName}
                onChange={(e) => setCollectionName(e.target.value)}
                placeholder="e.g., blog, docs, notes"
                className="w-full px-3 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-green-500"
                autoFocus
              />
              <p className="text-xs text-fg-faint mt-1">
                Use only letters, numbers, underscores, and hyphens
              </p>
            </div>

            {error && (
              <div className="mb-4 p-3 bg-danger-soft border border-danger-line rounded-lg">
                <p className="text-sm text-danger-strong">{error}</p>
              </div>
            )}

            <div className="bg-info-soft border border-info-line rounded-lg p-3 mb-4">
              <p className="text-sm text-info-strong">
                <strong>Note:</strong> The collection will appear in the sidebar once you upload your first document to it.
              </p>
            </div>

            <div className="flex gap-3">
              <button
                type="button"
                onClick={onClose}
                className="flex-1 px-4 py-2 text-fg-muted bg-surface-subtle hover:bg-surface-strong rounded-lg transition-colors"
              >
                Cancel
              </button>
              <button
                type="submit"
                className="flex-1 px-4 py-2 bg-green-600 text-white rounded-lg hover:bg-green-700 transition-colors flex items-center justify-center gap-2"
              >
                <FolderPlus className="w-4 h-4" />
                Create Collection
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>
  );
}
