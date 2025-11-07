import { useState } from 'react';
import { Folder, Database, HardDrive, FileText, Trash2 } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';

export default function Sidebar({ stats, statsError }) {
  const { currentCollection, setCurrentCollection } = useStore();
  const [deletingCollection, setDeletingCollection] = useState(null);

  const collections = stats?.collections || [];

  const handleDeleteCollection = async (collectionName, e) => {
    e.stopPropagation();
    
    const message = `⚠️ WARNING: This will PERMANENTLY delete ALL documents in "${collectionName}"!\n\nThis action cannot be undone.\n\nAre you absolutely sure?`;
    if (!confirm(message)) {
      return;
    }

    setDeletingCollection(collectionName);
    try {
      const result = await mddbClient.deleteCollection({ 
        collection: collectionName 
      });

      alert(`✅ Collection "${collectionName}" has been deleted successfully!\n\nDeleted ${result.deletedCount} documents.`);
      
      // Reload page to refresh stats
      window.location.reload();
    } catch (error) {
      alert(`Failed to delete collection: ${error.message}`);
      console.error('Delete collection error:', error);
    } finally {
      setDeletingCollection(null);
    }
  };

  const formatBytes = (bytes) => {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i];
  };

  return (
    <div className="w-64 bg-white border-r border-gray-200 overflow-y-auto">
      {/* Stats Summary */}
      <div className="p-4 border-b border-gray-200">
        <h3 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-3">
          Server Stats
        </h3>
        <div className="space-y-2">
          <div className="flex items-center justify-between text-sm">
            <span className="text-gray-600">Documents</span>
            <span className="font-medium text-gray-900">
              {stats?.totalDocuments?.toLocaleString() || 0}
            </span>
          </div>
          <div className="flex items-center justify-between text-sm">
            <span className="text-gray-600">Revisions</span>
            <span className="font-medium text-gray-900">
              {stats?.totalRevisions?.toLocaleString() || 0}
            </span>
          </div>
          <div className="flex items-center justify-between text-sm">
            <span className="text-gray-600">DB Size</span>
            <span className="font-medium text-gray-900">
              {formatBytes(stats?.databaseSize || 0)}
            </span>
          </div>
        </div>
      </div>

      {/* Collections List */}
      <div className="p-4">
        <h3 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-3">
          Collections ({collections.length})
        </h3>
        <div className="space-y-1">
          {statsError ? (
            <div className="text-sm text-red-600 text-center py-8 px-4">
              <div className="flex items-center justify-center mb-2">
                <Database className="w-8 h-8 text-red-400" />
              </div>
              <div className="font-medium">Connection Error</div>
              <div className="text-xs mt-1">{statsError}</div>
              <div className="text-xs mt-2 text-gray-500">
                Make sure MDDB server is running and accessible
              </div>
            </div>
          ) : collections.length === 0 ? (
            <div className="text-sm text-gray-500 text-center py-8">
              No collections found
            </div>
          ) : (
            collections.map((collection) => (
              <div
                key={collection.name}
                className={`w-full flex items-center justify-between px-3 py-2 rounded-lg transition-colors group ${
                  currentCollection === collection.name
                    ? 'bg-primary-100 text-primary-700'
                    : 'text-gray-700 hover:bg-gray-100'
                }`}
              >
                <button
                  onClick={() => setCurrentCollection(collection.name)}
                  className="flex-1 flex items-center space-x-2 text-left"
                >
                  <Folder className="w-4 h-4" />
                  <span className="text-sm font-medium truncate">
                    {collection.name}
                  </span>
                </button>
                <div className="flex items-center space-x-2">
                  <span className="text-xs text-gray-500">
                    {collection.documentCount}
                  </span>
                  <button
                    onClick={(e) => handleDeleteCollection(collection.name, e)}
                    disabled={deletingCollection === collection.name}
                    className="opacity-0 group-hover:opacity-100 p-1 text-red-500 hover:bg-red-50 rounded transition-opacity"
                    title="Delete collection"
                  >
                    {deletingCollection === collection.name ? (
                      <div className="animate-spin rounded-full h-3 w-3 border-b-2 border-red-500"></div>
                    ) : (
                      <Trash2 className="w-3 h-3" />
                    )}
                  </button>
                </div>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
