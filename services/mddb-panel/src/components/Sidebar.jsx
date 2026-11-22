import { useState, useEffect } from 'react';
import { Folder, Database, Trash2, Brain, Upload, FolderPlus, PanelLeftClose, Globe, Image, Music, FileText, Settings2 } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import UploadModal from './UploadModal';
import CreateCollectionModal from './CreateCollectionModal';
import CollectionConfigModal from './CollectionConfigModal';
// 2.19.0: the menu, grouped and searchable.
import NavMenu from './NavMenu';
import { navConditions } from '../lib/nav-groups';
// The panel's own version, read from package.json at build time, so a release
// bump (scripts/bump-version.sh) shows up here with no further edit.
import { version as panelVersion } from '../../package.json';

export default function Sidebar({ stats, statsError, onStatsRefresh, onCollapse }) {
  const { currentCollection, setCurrentCollection, vectorStats, setVectorStats, config, setViewMode, setStats, collectionConfigs, setCollectionConfigs } = useStore();
  const [deletingCollection, setDeletingCollection] = useState(null);
  const [showUploadModal, setShowUploadModal] = useState(false);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [configCollection, setConfigCollection] = useState(null);

  useEffect(() => {
    loadVectorStats();
    loadCollectionConfigs();
    const interval = setInterval(() => {
      loadVectorStats();
    }, 5000);
    return () => clearInterval(interval);
  }, []);

  const loadCollectionConfigs = async () => {
    try {
      const data = await mddbClient.listCollectionConfigs();
      const configMap = {};
      (data.configs || []).forEach((item) => {
        configMap[item.collection] = item.config;
      });
      setCollectionConfigs(configMap);
    } catch {
      // Collection configs unavailable - not critical
    }
  };

  const getCollectionIcon = (collectionName) => {
    const cfg = collectionConfigs[collectionName];
    if (cfg?.icon) {
      return <span className="text-sm flex-shrink-0">{cfg.icon}</span>;
    }
    const type = cfg?.type || 'default';
    switch (type) {
      case 'website': return <Globe className="w-4 h-4 flex-shrink-0" />;
      case 'images': return <Image className="w-4 h-4 flex-shrink-0" />;
      case 'audio': return <Music className="w-4 h-4 flex-shrink-0" />;
      case 'documents': return <FileText className="w-4 h-4 flex-shrink-0" />;
      default: return <Folder className="w-4 h-4 flex-shrink-0" />;
    }
  };

  const loadVectorStats = async () => {
    try {
      const data = await mddbClient.vectorStats();
      setVectorStats(data);
    } catch {
      // Vector stats unavailable - not critical
    }
  };

  const collections = stats?.collections || [];

  const handleDeleteCollection = async (collectionName, e) => {
    e.stopPropagation();

    const message = `WARNING: This will PERMANENTLY delete ALL documents in "${collectionName}"!\n\nThis action cannot be undone.\n\nAre you absolutely sure?`;
    if (!confirm(message)) {
      return;
    }

    setDeletingCollection(collectionName);
    try {
      const result = await mddbClient.deleteCollection({
        collection: collectionName
      });

      alert(`Collection "${collectionName}" has been deleted successfully!\n\nDeleted ${result.deletedCount} documents.`);
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
    <div className="h-full bg-surface border-r border-line overflow-y-auto flex flex-col">
      {/* Collapse button */}
      <div className="p-2 border-b border-line flex justify-end">
        <button
          onClick={onCollapse}
          className="p-1 text-fg-faint hover:text-fg-subtle hover:bg-surface-subtle rounded transition-colors"
          title="Hide sidebar"
        >
          <PanelLeftClose className="w-4 h-4" />
        </button>
      </div>

      {/* Stats Summary */}
      <div className="p-4 border-b border-line">
        <h3 className="text-xs font-semibold text-fg-faint uppercase tracking-wider mb-3">
          Server Stats
        </h3>
        <div className="space-y-2">
          <div className="flex items-center justify-between text-sm">
            <span className="text-fg-subtle">Documents</span>
            <span className="font-medium text-fg">
              {stats?.totalDocuments?.toLocaleString() || 0}
            </span>
          </div>
          <div className="flex items-center justify-between text-sm">
            <span className="text-fg-subtle">Revisions</span>
            <span className="font-medium text-fg">
              {stats?.totalRevisions?.toLocaleString() || 0}
            </span>
          </div>
          <div className="flex items-center justify-between text-sm">
            <span className="text-fg-subtle">DB Size</span>
            <span className="font-medium text-fg">
              {formatBytes(stats?.databaseSize || 0)}
            </span>
          </div>
        </div>
      </div>

      {/* Embeddings */}
      {vectorStats && (
        <div className="p-4 border-b border-line">
          <h3 className="text-xs font-semibold text-fg-faint uppercase tracking-wider mb-3">
            <span className="flex items-center space-x-2">
              <Brain className="w-3 h-3" />
              <span>Embeddings</span>
              {vectorStats.enabled ? (
                <span className="w-2 h-2 bg-green-500 rounded-full animate-pulse" title="Connected"></span>
              ) : (
                <span className="w-2 h-2 bg-red-500 rounded-full" title="Disconnected"></span>
              )}
            </span>
          </h3>
          {vectorStats.enabled ? (
            <div className="space-y-2">
              <div className="flex items-center justify-between text-sm">
                <span className="text-fg-subtle">Provider</span>
                <span className="font-medium text-fg text-xs capitalize">
                  {vectorStats.provider || 'Unknown'}
                </span>
              </div>
              <div className="flex items-center justify-between text-sm">
                <span className="text-fg-subtle">Model</span>
                <span className="font-medium text-fg text-xs truncate max-w-[120px]" title={vectorStats.model}>
                  {vectorStats.model}
                </span>
              </div>
              <div className="flex items-center justify-between text-sm">
                <span className="text-fg-subtle">Dimensions</span>
                <span className="font-medium text-fg">{vectorStats.dimensions}</span>
              </div>
            </div>
          ) : (
            <p className="text-xs text-fg-faint">Disabled</p>
          )}
        </div>
      )}

      {/* Collections List */}
      <div className="p-4 flex-1">
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-xs font-semibold text-fg-faint uppercase tracking-wider">
            Collections ({collections.length})
          </h3>
          {currentCollection ? (
            <button
              onClick={() => setShowUploadModal(true)}
              className="p-1.5 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
              title="Upload documents to collection"
            >
              <Upload className="w-3.5 h-3.5" />
            </button>
          ) : (
            <button
              onClick={() => setShowCreateModal(true)}
              className="p-1.5 bg-green-600 text-white rounded-lg hover:bg-green-700 transition-colors"
              title="Create new collection"
            >
              <FolderPlus className="w-3.5 h-3.5" />
            </button>
          )}
        </div>
        <div className="space-y-1">
          {statsError ? (
            <div className="text-sm text-danger text-center py-8 px-4">
              <div className="flex items-center justify-center mb-2">
                <Database className="w-8 h-8 text-danger" />
              </div>
              <div className="font-medium">Connection Error</div>
              <div className="text-xs mt-1">{statsError}</div>
              <div className="text-xs mt-2 text-fg-faint">
                Make sure MDDB server is running and accessible
              </div>
            </div>
          ) : collections.length === 0 ? (
            <div className="text-sm text-fg-faint text-center py-8">
              No collections found
            </div>
          ) : (
            collections.map((collection) => (
              <div
                key={collection.name}
                className={`w-full flex items-center justify-between px-3 py-2 rounded-lg transition-colors group ${currentCollection === collection.name
                  ? 'bg-info-muted text-info'
                  : 'text-fg-muted hover:bg-surface-subtle'
                  }`}
              >
                <button
                  onClick={() => {
                    setCurrentCollection(collection.name);
                    setViewMode('documents');
                  }}
                  className="flex-1 min-w-0 flex items-center gap-2 text-left"
                >
                  {getCollectionIcon(collection.name)}
                  {/* A long name ends in an ellipsis (full name in the title)
                      instead of running into the document count. */}
                  <span className="text-sm font-medium truncate" title={collection.name}>
                    {collection.name}
                  </span>
                </button>
                <div className="flex-shrink-0 ml-2 flex items-center space-x-2">
                  <span className="text-xs text-fg-subtle">
                    {collection.documentCount}
                  </span>
                  <button
                    onClick={(e) => {
                      e.stopPropagation();
                      setConfigCollection(collection.name);
                    }}
                    className="opacity-0 group-hover:opacity-100 focus:opacity-100 p-1 text-fg-faint hover:text-fg-subtle hover:bg-surface-subtle rounded transition-opacity"
                    title="Collection settings"
                  >
                    <Settings2 className="w-3 h-3" />
                  </button>
                  <button
                    onClick={(e) => handleDeleteCollection(collection.name, e)}
                    disabled={deletingCollection === collection.name}
                    className="opacity-0 group-hover:opacity-100 focus:opacity-100 p-1 text-danger hover:bg-danger-soft rounded transition-opacity"
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

      {/* Menu: grouped, searchable, built from nav-groups.js */}
      <div className="p-4 border-t border-line">
        <NavMenu {...navConditions(config)} />
      </div>

      {/* Version Footer */}
      <div className="px-4 py-2 border-t border-line text-center">
        <span className="text-[10px] text-fg-subtle">
          Server v{config?.version || '...'} · Panel v{panelVersion}
        </span>
      </div>

      {/* Create Collection Modal */}
      {showCreateModal && (
        <CreateCollectionModal
          onClose={() => setShowCreateModal(false)}
          onCreate={(collectionName) => {
            setShowCreateModal(false);
            setCurrentCollection(collectionName);
            setShowUploadModal(true);
          }}
        />
      )}

      {/* Upload Modal */}
      {showUploadModal && (
        <UploadModal
          collection={currentCollection}
          onClose={() => setShowUploadModal(false)}
          onSuccess={async (uploadedCollection) => {
            setShowUploadModal(false);
            try {
              const newStats = await mddbClient.getStats();
              setStats(newStats);
              if (!currentCollection && uploadedCollection) {
                setCurrentCollection(uploadedCollection);
              }
              if (onStatsRefresh) {
                onStatsRefresh();
              }
            } catch (error) {
              console.error('Failed to refresh stats:', error);
            }
          }}
        />
      )}

      {/* Collection Config Modal */}
      {configCollection && (
        <CollectionConfigModal
          collection={configCollection}
          onClose={() => setConfigCollection(null)}
          onSave={() => loadCollectionConfigs()}
        />
      )}
    </div>
  );
}
