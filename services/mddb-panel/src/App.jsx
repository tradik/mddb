import { useEffect, useState, useCallback, useRef } from 'react';
import { useStore } from './lib/store';
import mddbClient from './lib/mddb-client';
import { authManager } from './lib/auth';
import Header from './components/Header';
import Sidebar from './components/Sidebar';
import DocumentList from './components/DocumentList';
import DocumentViewer from './components/DocumentViewer';
import VectorSearchPanel from './components/VectorSearchPanel';
import HybridSearchPanel from './components/HybridSearchPanel';
import FTSSearchPanel from './components/FTSSearchPanel';
import LoginForm from './components/LoginForm';
import SystemInfo from './components/SystemInfo';
import ConfigPanel from './components/ConfigPanel';
import MCPConfigPanel from './components/MCPConfigPanel';
import EndpointsPanel from './components/EndpointsPanel';
import UsersPanel from './components/UsersPanel';
import GroupsPanel from './components/GroupsPanel';
import VectorPanel from './components/VectorPanel';
import VectorSpacePanel from './components/VectorSpacePanel';
import SearchAdvisorPanel from './components/SearchAdvisorPanel';
import EmbeddingModelsPanel from './components/EmbeddingModelsPanel';
import SettingsPanel from './components/SettingsPanel';
import ClusterPanel from './components/ClusterPanel';
import SynonymsPanel from './components/SynonymsPanel';
import CurationPanel from './components/CurationPanel';
import StopWordsPanel from './components/StopWordsPanel';
import AutomationPanel from './components/AutomationPanel';
import CrossSearchPanel from './components/CrossSearchPanel';
import TemporalPanel from './components/TemporalPanel';
import SpellCheckPanel from './components/SpellCheckPanel';
import GeoPanel from './components/GeoPanel';
import AuditLogPanel from './components/AuditLogPanel';
import WebhooksPanel from './components/WebhooksPanel';
import SecurityDashboard from './components/SecurityDashboard';
import EncryptionPanel from './components/EncryptionPanel';
import ComplianceBanner from './components/ComplianceBanner';
import SSEToast from './components/SSEToast';
import { useSSE } from './lib/useSSE';

function App() {
  const {
    stats,
    statsError,
    setStats,
    setStatsLoading,
    setStatsError,
    currentDocument,
    searchMode,
    viewMode,
    sidebarWidth,
    sidebarCollapsed,
    setSidebarWidth,
    setSidebarCollapsed,
    setConfig,
  } = useStore();

  const [isAuthenticated, setIsAuthenticated] = useState(authManager.isAuthenticated());
  const [needsAuth, setNeedsAuth] = useState(false);
  const isResizing = useRef(false);
  const [sseRefreshKey, setSSERefreshKey] = useState(0);

  // SSE: real-time document change notifications
  const { connected: sseConnected, lastEvent: sseLastEvent } = useSSE({
    enabled: isAuthenticated || !needsAuth,
    onEvent: () => {
      // Trigger auto-refresh of document list
      setSSERefreshKey((k) => k + 1);
    },
  });

  useEffect(() => {
    checkAuthAndLoadStats();
  }, []);

  const checkAuthAndLoadStats = async () => {
    setStatsLoading(true);
    setStatsError(null);
    try {
      const data = await mddbClient.getStats();
      setStats(data);
      setIsAuthenticated(true);
      setNeedsAuth(false);
    } catch (error) {
      // If we get Unauthorized error, auth is enabled
      if (error.message.includes('401') || error.message.includes('Unauthorized')) {
        setNeedsAuth(true);
        setIsAuthenticated(false);
      } else {
        // Other errors - auth might be disabled
        setStatsError(error.message);
        setIsAuthenticated(true); // Assume auth is disabled
        setNeedsAuth(false);
      }
      console.error('Failed to load stats:', error);
    } finally {
      setStatsLoading(false);
    }

    // Load config for auth status detection
    try {
      const cfg = await mddbClient.getConfig();
      setConfig(cfg);
    } catch {
      // Config unavailable - not critical
    }
  };

  const loadStats = async () => {
    setStatsLoading(true);
    setStatsError(null);
    try {
      const data = await mddbClient.getStats();
      setStats(data);
    } catch (error) {
      setStatsError(error.message);
      console.error('Failed to load stats:', error);
    } finally {
      setStatsLoading(false);
    }
  };

  // Sidebar resize handlers
  const handleMouseDown = useCallback((e) => {
    e.preventDefault();
    isResizing.current = true;
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';

    const handleMouseMove = (e) => {
      if (!isResizing.current) return;
      const newWidth = Math.min(Math.max(e.clientX, 180), 480);
      setSidebarWidth(newWidth);
    };

    const handleMouseUp = () => {
      isResizing.current = false;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };

    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);
  }, [setSidebarWidth]);

  // Show login form if authentication is required and user is not authenticated
  if (needsAuth && !isAuthenticated) {
    return <LoginForm onSuccess={() => {
      setIsAuthenticated(true);
      setNeedsAuth(false);
      useStore.getState().setViewMode('system');
      loadStats();
    }} />;
  }

  const effectiveWidth = sidebarCollapsed ? 0 : sidebarWidth;

  return (
    <div className="min-h-screen bg-gray-50">
      <ComplianceBanner />
      <Header onRefresh={loadStats} />

      <div className="flex" style={{ height: 'calc(100vh - 64px)' }}>
        {/* Sidebar */}
        <div
          className="relative flex-shrink-0 transition-[width] duration-150"
          style={{ width: effectiveWidth }}
        >
          {!sidebarCollapsed && (
            <Sidebar stats={stats} statsError={statsError} onCollapse={() => setSidebarCollapsed(true)} />
          )}
        </div>

        {/* Resize handle */}
        {!sidebarCollapsed && (
          <div
            onMouseDown={handleMouseDown}
            className="w-1 flex-shrink-0 cursor-col-resize bg-gray-200 hover:bg-primary-400 active:bg-primary-500 transition-colors"
          />
        )}

        {/* Collapse toggle (shown when collapsed) */}
        {sidebarCollapsed && (
          <button
            onClick={() => setSidebarCollapsed(false)}
            className="flex-shrink-0 w-6 bg-gray-100 hover:bg-gray-200 border-r border-gray-200 flex items-center justify-center transition-colors"
            title="Show sidebar"
          >
            <svg className="w-3 h-3 text-gray-500" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" /></svg>
          </button>
        )}

        <div className="flex-1 flex overflow-hidden">
          {viewMode === 'cluster' && (
            <div className="flex-1 border-l border-gray-200">
              <ClusterPanel />
            </div>
          )}
          {viewMode === 'system' && (
            <div className="flex-1 border-l border-gray-200">
              <SystemInfo />
            </div>
          )}
          {viewMode === 'config' && (
            <div className="flex-1 border-l border-gray-200">
              <ConfigPanel />
            </div>
          )}
          {viewMode === 'mcp' && (
            <div className="flex-1 border-l border-gray-200">
              <MCPConfigPanel />
            </div>
          )}
          {viewMode === 'endpoints' && (
            <div className="flex-1 border-l border-gray-200">
              <EndpointsPanel />
            </div>
          )}
          {viewMode === 'users' && (
            <div className="flex-1 border-l border-gray-200">
              <UsersPanel />
            </div>
          )}
          {viewMode === 'groups' && (
            <div className="flex-1 border-l border-gray-200">
              <GroupsPanel />
            </div>
          )}
          {viewMode === 'vectors' && (
            <div className="flex-1 border-l border-gray-200">
              <VectorPanel />
            </div>
          )}
          {viewMode === 'vectorSpace' && (
            <div className="flex-1 border-l border-gray-200">
              <VectorSpacePanel />
            </div>
          )}
          {viewMode === 'searchAdvisor' && (
            <div className="flex-1 border-l border-gray-200">
              <SearchAdvisorPanel />
            </div>
          )}
          {viewMode === 'embeddings' && (
            <div className="flex-1 border-l border-gray-200">
              <EmbeddingModelsPanel />
            </div>
          )}
          {viewMode === 'synonyms' && (
            <div className="flex-1 border-l border-gray-200">
              <SynonymsPanel />
            </div>
          )}
          {viewMode === 'stopwords' && (
            <div className="flex-1 border-l border-gray-200">
              <StopWordsPanel />
            </div>
          )}
          {viewMode === 'curation' && (
            <div className="flex-1 border-l border-gray-200">
              <CurationPanel />
            </div>
          )}
          {viewMode === 'automation' && (
            <div className="flex-1 border-l border-gray-200">
              <AutomationPanel />
            </div>
          )}
          {viewMode === 'crossSearch' && (
            <>
              <div className="flex-1 border-l border-gray-200">
                <CrossSearchPanel />
              </div>
              {currentDocument && (
                <div className="flex-1 border-l border-gray-200">
                  <DocumentViewer />
                </div>
              )}
            </>
          )}
          {viewMode === 'geo' && (
            <>
              <div className="flex-1 border-l border-gray-200">
                <GeoPanel />
              </div>
              {currentDocument && (
                <div className="flex-1 border-l border-gray-200">
                  <DocumentViewer />
                </div>
              )}
            </>
          )}
          {viewMode === 'settings' && (
            <div className="flex-1 border-l border-gray-200">
              <SettingsPanel />
            </div>
          )}
          {viewMode === 'temporal' && (
            <div className="flex-1 border-l border-gray-200">
              <TemporalPanel />
            </div>
          )}
          {viewMode === 'spellcheck' && (
            <div className="flex-1 border-l border-gray-200">
              <SpellCheckPanel />
            </div>
          )}
          {viewMode === 'auditLog' && (
            <div className="flex-1 border-l border-gray-200">
              <AuditLogPanel />
            </div>
          )}
          {viewMode === 'webhooks' && (
            <div className="flex-1 border-l border-gray-200">
              <WebhooksPanel />
            </div>
          )}
          {viewMode === 'security' && (
            <div className="flex-1 border-l border-gray-200">
              <SecurityDashboard />
            </div>
          )}
          {viewMode === 'encryption' && (
            <div className="flex-1 border-l border-gray-200">
              <EncryptionPanel />
            </div>
          )}
          {viewMode === 'documents' && (
            <>
              {searchMode === 'vector' ? (
                <>
                  <div className="flex-1 border-l border-gray-200">
                    <VectorSearchPanel />
                  </div>
                  {currentDocument && (
                    <div className="flex-1 border-l border-gray-200">
                      <DocumentViewer />
                    </div>
                  )}
                </>
              ) : searchMode === 'hybrid' ? (
                <>
                  <div className="flex-1 border-l border-gray-200">
                    <HybridSearchPanel />
                  </div>
                  {currentDocument && (
                    <div className="flex-1 border-l border-gray-200">
                      <DocumentViewer />
                    </div>
                  )}
                </>
              ) : searchMode === 'fulltext' ? (
                <>
                  <div className="flex-1 border-l border-gray-200">
                    <FTSSearchPanel />
                  </div>
                  {currentDocument && (
                    <div className="flex-1 border-l border-gray-200">
                      <DocumentViewer />
                    </div>
                  )}
                </>
              ) : (
                <>
                  <div className="flex-1 border-l border-gray-200">
                    <DocumentList sseRefreshKey={sseRefreshKey} />
                  </div>
                  {currentDocument && (
                    <div className="flex-1 border-l border-gray-200">
                      <DocumentViewer />
                    </div>
                  )}
                </>
              )}
            </>
          )}
        </div>
      </div>
      <SSEToast connected={sseConnected} lastEvent={sseLastEvent} />
    </div>
  );
}

export default App;
