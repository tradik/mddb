import { useEffect, useState, useCallback } from 'react';
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
import AliasesPanel from './components/AliasesPanel';
import EvalPanel from './components/EvalPanel';
import CodeSymbolPanel from './components/CodeSymbolPanel';
import ChatScenariosPanel from './components/ChatScenariosPanel';
import FeaturesPanel from './components/FeaturesPanel';
import FeatureDisabledNotice from './components/FeatureDisabledNotice';
import { useFeatures, disabledFeatureForView } from './lib/features';
import ComplianceBanner from './components/ComplianceBanner';
import GlobalShortcuts from './components/GlobalShortcuts';
import SSEToast from './components/SSEToast';
import { useSSE } from './lib/useSSE';
// 2.19.0: memory, schemas, duplicates, import and account pages.
import AdminToolViews from './components/AdminToolViews';

function App() {
  const {
    stats,
    statsError,
    setStats,
    setStatsLoading,
    setStatsError,
    currentDocument,
    searchMode,
    viewMode: requestedView,
    sidebarWidth,
    sidebarCollapsed,
    setSidebarWidth,
    setSidebarCollapsed,
    setConfig,
  } = useStore();
  // GO-049: a page whose server module is off shows a notice instead.
  const { features, reload: reloadFeatures } = useFeatures();
  const disabledFeature = disabledFeatureForView(requestedView, features);
  const viewMode = disabledFeature ? 'featureDisabled' : requestedView;

  const [isAuthenticated, setIsAuthenticated] = useState(authManager.isAuthenticated());
  const [needsAuth, setNeedsAuth] = useState(false);
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
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';

    const handleMouseMove = (e) => {
      const newWidth = Math.min(Math.max(e.clientX, 180), 480);
      setSidebarWidth(newWidth);
    };

    const handleMouseUp = () => {
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
      reloadFeatures();
      useStore.getState().setViewMode('system');
      loadStats();
    }} />;
  }

  const effectiveWidth = sidebarCollapsed ? 0 : sidebarWidth;

  return (
    <div className="min-h-screen bg-surface-muted">
      <ComplianceBanner />
      <GlobalShortcuts />
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
            className="w-1 flex-shrink-0 cursor-col-resize bg-surface-strong hover:bg-primary-400 active:bg-primary-500 transition-colors"
          />
        )}

        {/* Collapse toggle (shown when collapsed) */}
        {sidebarCollapsed && (
          <button
            onClick={() => setSidebarCollapsed(false)}
            className="flex-shrink-0 w-6 bg-surface-subtle hover:bg-surface-strong border-r border-line flex items-center justify-center transition-colors"
            title="Show sidebar"
          >
            <svg className="w-3 h-3 text-fg-faint" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" /></svg>
          </button>
        )}

        <div className="flex-1 min-w-0 flex overflow-hidden">
          {viewMode === 'cluster' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <ClusterPanel />
            </div>
          )}
          {viewMode === 'system' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <SystemInfo />
            </div>
          )}
          {viewMode === 'config' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <ConfigPanel />
            </div>
          )}
            {viewMode === 'features' && (
              <div className="flex-1 min-w-0 border-l border-line">
                <FeaturesPanel />
              </div>
            )}
            {viewMode === 'featureDisabled' && (
              <div className="flex-1 min-w-0 border-l border-line">
                <FeatureDisabledNotice feature={disabledFeature} />
              </div>
            )}
          {viewMode === 'mcp' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <MCPConfigPanel />
            </div>
          )}
          {viewMode === 'endpoints' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <EndpointsPanel />
            </div>
          )}
          {viewMode === 'chat' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <ChatScenariosPanel />
            </div>
          )}
          {viewMode === 'users' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <UsersPanel />
            </div>
          )}
          {viewMode === 'groups' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <GroupsPanel />
            </div>
          )}
          {viewMode === 'vectors' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <VectorPanel />
            </div>
          )}
          {viewMode === 'vectorSpace' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <VectorSpacePanel />
            </div>
          )}
          {viewMode === 'searchAdvisor' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <SearchAdvisorPanel />
            </div>
          )}
          {viewMode === 'embeddings' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <EmbeddingModelsPanel />
            </div>
          )}
          {viewMode === 'synonyms' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <SynonymsPanel />
            </div>
          )}
          {viewMode === 'stopwords' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <StopWordsPanel />
            </div>
          )}
          {viewMode === 'curation' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <CurationPanel />
            </div>
          )}
          {viewMode === 'aliases' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <AliasesPanel />
            </div>
          )}
          {viewMode === 'eval' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <EvalPanel />
            </div>
          )}
          {viewMode === 'codeSymbols' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <CodeSymbolPanel />
            </div>
          )}
          {viewMode === 'automation' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <AutomationPanel />
            </div>
          )}
          {viewMode === 'crossSearch' && (
            <>
              <div className="flex-1 min-w-0 border-l border-line">
                <CrossSearchPanel />
              </div>
              {currentDocument && (
                <div className="flex-1 min-w-0 border-l border-line">
                  <DocumentViewer />
                </div>
              )}
            </>
          )}
          {viewMode === 'geo' && (
            <>
              <div className="flex-1 min-w-0 border-l border-line">
                <GeoPanel />
              </div>
              {currentDocument && (
                <div className="flex-1 min-w-0 border-l border-line">
                  <DocumentViewer />
                </div>
              )}
            </>
          )}
          {viewMode === 'settings' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <SettingsPanel />
            </div>
          )}
          {viewMode === 'temporal' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <TemporalPanel />
            </div>
          )}
          {viewMode === 'spellcheck' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <SpellCheckPanel />
            </div>
          )}
          {viewMode === 'auditLog' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <AuditLogPanel />
            </div>
          )}
          {viewMode === 'webhooks' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <WebhooksPanel />
            </div>
          )}
          {viewMode === 'security' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <SecurityDashboard />
            </div>
          )}
          {viewMode === 'encryption' && (
            <div className="flex-1 min-w-0 border-l border-line">
              <EncryptionPanel />
            </div>
          )}
          {viewMode === 'documents' && (
            <>
              {searchMode === 'vector' ? (
                <>
                  <div className="flex-1 min-w-0 border-l border-line">
                    <VectorSearchPanel />
                  </div>
                  {currentDocument && (
                    <div className="flex-1 min-w-0 border-l border-line">
                      <DocumentViewer />
                    </div>
                  )}
                </>
              ) : searchMode === 'hybrid' ? (
                <>
                  <div className="flex-1 min-w-0 border-l border-line">
                    <HybridSearchPanel />
                  </div>
                  {currentDocument && (
                    <div className="flex-1 min-w-0 border-l border-line">
                      <DocumentViewer />
                    </div>
                  )}
                </>
              ) : searchMode === 'fulltext' ? (
                <>
                  <div className="flex-1 min-w-0 border-l border-line">
                    <FTSSearchPanel />
                  </div>
                  {currentDocument && (
                    <div className="flex-1 min-w-0 border-l border-line">
                      <DocumentViewer />
                    </div>
                  )}
                </>
              ) : (
                <>
                  <div className="flex-1 min-w-0 border-l border-line">
                    <DocumentList sseRefreshKey={sseRefreshKey} />
                  </div>
                  {currentDocument && (
                    <div className="flex-1 min-w-0 border-l border-line">
                      <DocumentViewer />
                    </div>
                  )}
                </>
              )}
            </>
          )}
          <AdminToolViews viewMode={viewMode} currentDocument={currentDocument} />
        </div>
      </div>
      <SSEToast connected={sseConnected} lastEvent={sseLastEvent} />
    </div>
  );
}

export default App;
