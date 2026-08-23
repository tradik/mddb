import { useState, useEffect } from 'react';
import { Folder, Database, Trash2, Brain, Server, Settings, Network, Users, UsersIcon, Upload, FolderPlus, Sliders, GitBranch, PanelLeftClose, BookOpen, Ban, Zap, Globe, Image, Music, FileText, Settings2, Shuffle, Clock, Type, Pin, FileSearch, Webhook, Shield, Lock, ScatterChart, Compass } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import UploadModal from './UploadModal';
import CreateCollectionModal from './CreateCollectionModal';
import CollectionConfigModal from './CollectionConfigModal';

export default function Sidebar({ stats, statsError, onStatsRefresh, onCollapse }) {
  const { currentCollection, setCurrentCollection, vectorStats, setVectorStats, config, viewMode, setViewMode, setStats, collectionConfigs, setCollectionConfigs } = useStore();
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
  const authEnabled = config?.authEnabled ?? false;
  const clusterEnabled = !!config?.replicationRole;

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

  const NavButton = ({ mode, icon: Icon, label }) => (
    <button
      onClick={() => setViewMode(mode)}
      className={`w-full flex items-center space-x-2 px-3 py-2 rounded-lg transition-colors ${viewMode === mode
        ? 'bg-blue-100 text-blue-700'
        : 'text-gray-700 hover:bg-gray-100'
        }`}
    >
      <Icon className="w-4 h-4" />
      <span className="text-sm font-medium">{label}</span>
    </button>
  );

  return (
    <div className="h-full bg-white border-r border-gray-200 overflow-y-auto flex flex-col">
      {/* Collapse button */}
      <div className="p-2 border-b border-gray-200 flex justify-end">
        <button
          onClick={onCollapse}
          className="p-1 text-gray-400 hover:text-gray-600 hover:bg-gray-100 rounded transition-colors"
          title="Hide sidebar"
        >
          <PanelLeftClose className="w-4 h-4" />
        </button>
      </div>

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

      {/* Embeddings */}
      {vectorStats && (
        <div className="p-4 border-b border-gray-200">
          <h3 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-3">
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
                <span className="text-gray-600">Provider</span>
                <span className="font-medium text-gray-900 text-xs capitalize">
                  {vectorStats.provider || 'Unknown'}
                </span>
              </div>
              <div className="flex items-center justify-between text-sm">
                <span className="text-gray-600">Model</span>
                <span className="font-medium text-gray-900 text-xs truncate max-w-[120px]" title={vectorStats.model}>
                  {vectorStats.model}
                </span>
              </div>
              <div className="flex items-center justify-between text-sm">
                <span className="text-gray-600">Dimensions</span>
                <span className="font-medium text-gray-900">{vectorStats.dimensions}</span>
              </div>
            </div>
          ) : (
            <p className="text-xs text-gray-400">Disabled</p>
          )}
        </div>
      )}

      {/* Collections List */}
      <div className="p-4 flex-1">
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-xs font-semibold text-gray-500 uppercase tracking-wider">
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
                className={`w-full flex items-center justify-between px-3 py-2 rounded-lg transition-colors group ${currentCollection === collection.name
                  ? 'bg-blue-100 text-blue-700'
                  : 'text-gray-700 hover:bg-gray-100'
                  }`}
              >
                <button
                  onClick={() => {
                    setCurrentCollection(collection.name);
                    setViewMode('documents');
                  }}
                  className="flex-1 flex items-center space-x-2 text-left"
                >
                  {getCollectionIcon(collection.name)}
                  <span className="text-sm font-medium truncate">
                    {collection.name}
                  </span>
                </button>
                <div className="flex items-center space-x-2">
                  <span className="text-xs text-gray-500">
                    {collection.documentCount}
                  </span>
                  <button
                    onClick={(e) => {
                      e.stopPropagation();
                      setConfigCollection(collection.name);
                    }}
                    className="opacity-0 group-hover:opacity-100 p-1 text-gray-400 hover:text-gray-600 hover:bg-gray-100 rounded transition-opacity"
                    title="Collection settings"
                  >
                    <Settings2 className="w-3 h-3" />
                  </button>
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

      {/* Administration Section */}
      <div className="p-4 border-t border-gray-200">
        <h3 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-3">
          Administration
        </h3>
        <div className="space-y-1">
          <NavButton mode="system" icon={Server} label="System Info" />
          {clusterEnabled && (
            <NavButton mode="cluster" icon={GitBranch} label="Cluster" />
          )}
          <NavButton mode="config" icon={Settings} label="Configuration" />
          <NavButton mode="mcp" icon={Brain} label="LLM Connections" />
          <NavButton mode="endpoints" icon={Network} label="API Endpoints" />
          {authEnabled && (
            <>
              <NavButton mode="users" icon={Users} label="Users" />
              <NavButton mode="groups" icon={UsersIcon} label="Groups" />
            </>
          )}
          <NavButton mode="crossSearch" icon={Shuffle} label="Cross Search" />
          <NavButton mode="geo" icon={Globe} label="Geo Search" />
          <NavButton mode="vectors" icon={Database} label="Vector Search" />
          <NavButton mode="vectorSpace" icon={ScatterChart} label="Vector Space" />
          <NavButton mode="searchAdvisor" icon={Compass} label="Search Advisor" />
          <NavButton mode="embeddings" icon={Brain} label="Embedding Models" />
          <NavButton mode="synonyms" icon={BookOpen} label="Synonyms" />
          <NavButton mode="stopwords" icon={Ban} label="Stop Words" />
          <NavButton mode="curation" icon={Pin} label="Curation Rules" />
          <NavButton mode="auditLog" icon={FileSearch} label="Audit Log" />
          <NavButton mode="webhooks" icon={Webhook} label="Webhooks" />
          <NavButton mode="security" icon={Shield} label="Security" />
          <NavButton mode="encryption" icon={Lock} label="Encryption" />
          <NavButton mode="temporal" icon={Clock} label="Temporal Analytics" />
          <NavButton mode="spellcheck" icon={Type} label="Spell Checker" />
          {config?.automationsEnabled !== false && (
            <NavButton mode="automation" icon={Zap} label="Automation" />
          )}
          <NavButton mode="settings" icon={Sliders} label="Client Settings" />
        </div>
      </div>

      {/* Version Footer */}
      <div className="px-4 py-2 border-t border-gray-200 text-center">
        <span className="text-[10px] text-gray-400">
          Server v{config?.version || '...'} · Panel v2.11.4
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
