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
      <div className="bg-white rounded-lg shadow-xl w-full max-w-md">
        <div className="p-6">
          <div className="flex items-center justify-between mb-4">
            <div className="flex items-center gap-2">
              <FolderPlus className="w-6 h-6 text-green-600" />
              <h2 className="text-xl font-bold text-gray-900">Create Collection</h2>
            </div>
            <button onClick={onClose} className="text-gray-400 hover:text-gray-600">
              <X className="w-5 h-5" />
            </button>
          </div>

          <form onSubmit={handleSubmit}>
            <div className="mb-4">
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Collection Name <span className="text-red-600">*</span>
              </label>
              <input
                type="text"
                value={collectionName}
                onChange={(e) => setCollectionName(e.target.value)}
                placeholder="e.g., blog, docs, notes"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-green-500"
                autoFocus
              />
              <p className="text-xs text-gray-500 mt-1">
                Use only letters, numbers, underscores, and hyphens
              </p>
            </div>

            {error && (
              <div className="mb-4 p-3 bg-red-50 border border-red-200 rounded-lg">
                <p className="text-sm text-red-800">{error}</p>
              </div>
            )}

            <div className="bg-blue-50 border border-blue-200 rounded-lg p-3 mb-4">
              <p className="text-sm text-blue-800">
                <strong>Note:</strong> The collection will appear in the sidebar once you upload your first document to it.
              </p>
            </div>

            <div className="flex gap-3">
              <button
                type="button"
                onClick={onClose}
                className="flex-1 px-4 py-2 text-gray-700 bg-gray-100 hover:bg-gray-200 rounded-lg transition-colors"
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
