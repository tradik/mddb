import { useEffect } from 'react';
import { Settings, Database, Network, ToggleLeft, ToggleRight, Brain } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import ServerEdgeCard from './ServerEdgeCard';

/** One on/off feature row. */
function FeatureToggle({ label, enabled }) {
  return (
    <div className="flex items-center justify-between py-2">
      <span className="text-sm text-fg-muted">{label}</span>
      <div className="flex items-center">
        {enabled ? (
          <>
            <ToggleRight className="w-5 h-5 text-success" />
            <span className="ml-2 text-xs font-medium text-success">Enabled</span>
          </>
        ) : (
          <>
            <ToggleLeft className="w-5 h-5 text-fg-faint" />
            <span className="ml-2 text-xs font-medium text-fg-faint">Disabled</span>
          </>
        )}
      </div>
    </div>
  );
}

export default function ConfigPanel() {
  const {
    config,
    configLoading,
    configError,
    setConfig,
    setConfigLoading,
    setConfigError,
  } = useStore();

  useEffect(() => {
    loadConfig();
  }, []);

  const loadConfig = async () => {
    setConfigLoading(true);
    setConfigError(null);
    try {
      const data = await mddbClient.getConfig();
      setConfig(data);
    } catch (error) {
      setConfigError(error.message);
      console.error('Failed to load config:', error);
    } finally {
      setConfigLoading(false);
    }
  };

  if (configLoading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-primary-600"></div>
      </div>
    );
  }

  if (configError) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <div className="text-danger font-medium mb-2">Failed to load configuration</div>
          <div className="text-sm text-fg-faint">{configError}</div>
          <button
            onClick={loadConfig}
            className="mt-4 px-4 py-2 bg-primary-600 text-white rounded hover:bg-primary-700"
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  if (!config) return null;

  return (
    <div className="h-full overflow-y-auto bg-surface-muted p-6">
      <div className="max-w-4xl mx-auto">
        <div className="mb-6">
          <h1 className="text-2xl font-bold text-fg mb-2">Server Configuration</h1>
          <p className="text-fg-subtle">Current server settings and features</p>
        </div>

        {/* Database Settings */}
        <div className="bg-surface rounded-lg shadow mb-6 p-6">
          <div className="flex items-center mb-4">
            <Database className="w-5 h-5 text-accent mr-2" />
            <h2 className="text-lg font-semibold text-fg">Database</h2>
          </div>
          <div className="space-y-3">
            <div className="flex justify-between py-2 border-b">
              <span className="text-sm text-fg-subtle">Path</span>
              <span className="text-sm font-medium text-fg font-mono">{config.databasePath}</span>
            </div>
            <div className="flex justify-between py-2">
              <span className="text-sm text-fg-subtle">Mode</span>
              <span className="text-sm font-medium text-fg uppercase">
                {config.mode === 'wr' ? 'Read/Write' : config.mode === 'read' ? 'Read Only' : 'Write Only'}
              </span>
            </div>
          </div>
        </div>

        {/* Network Settings */}
        <div className="bg-surface rounded-lg shadow mb-6 p-6">
          <div className="flex items-center mb-4">
            <Network className="w-5 h-5 text-accent mr-2" />
            <h2 className="text-lg font-semibold text-fg">Network</h2>
          </div>
          <div className="space-y-3">
            <div className="flex justify-between py-2 border-b">
              <span className="text-sm text-fg-subtle">HTTP Address</span>
              <span className="text-sm font-medium text-fg font-mono">{config.protocols?.http?.addr}</span>
            </div>
            <div className="flex justify-between py-2 border-b">
              <span className="text-sm text-fg-subtle">gRPC Address</span>
              <span className="text-sm font-medium text-fg font-mono">{config.protocols?.grpc?.addr}</span>
            </div>
            {config.protocols?.http3?.enabled && (
              <div className="flex justify-between py-2">
                <span className="text-sm text-fg-subtle">HTTP/3 Address</span>
                <span className="text-sm font-medium text-fg font-mono">{config.protocols.http3.addr}</span>
              </div>
            )}
          </div>
        </div>

        {/* Features */}
        <div className="bg-surface rounded-lg shadow mb-6 p-6">
          <div className="flex items-center mb-4">
            <Settings className="w-5 h-5 text-accent mr-2" />
            <h2 className="text-lg font-semibold text-fg">Features</h2>
          </div>
          <div className="space-y-1 divide-y">
            <FeatureToggle label="Authentication & RBAC" enabled={config.authEnabled} />
            <FeatureToggle label="Prometheus Metrics" enabled={config.metricsEnabled} />
            <FeatureToggle label="Search Statistics" enabled={config.searchStatsEnabled} />
            <FeatureToggle label="Vector Search" enabled={config.vectorConfig?.enabled} />
          </div>
        </div>

        {/* Search surface, tracing and proxies (2.18.10) */}
        <ServerEdgeCard config={config} />

        {/* Vector Configuration */}
        {config.vectorConfig && config.vectorConfig.enabled && (
          <div className="bg-surface rounded-lg shadow mb-6 p-6">
            <div className="flex items-center mb-4">
              <Brain className="w-5 h-5 text-accent mr-2" />
              <h2 className="text-lg font-semibold text-fg">Vector Search Configuration</h2>
            </div>
            <div className="space-y-3">
              <div className="flex justify-between py-2 border-b">
                <span className="text-sm text-fg-subtle">Provider</span>
                <span className="text-sm font-medium text-fg capitalize">{config.vectorConfig.provider}</span>
              </div>
              <div className="flex justify-between py-2 border-b">
                <span className="text-sm text-fg-subtle">Model</span>
                <span className="text-sm font-medium text-fg">{config.vectorConfig.model}</span>
              </div>
              {config.vectorConfig.semantic !== undefined && (
                <div className="flex justify-between py-2 border-b">
                  <span className="text-sm text-fg-subtle">Matches by meaning</span>
                  <span className="text-sm font-medium text-fg">
                    {config.vectorConfig.semantic ? 'Yes' : 'No — shared spelling only (semantic: false)'}
                  </span>
                </div>
              )}
              <div className="flex justify-between py-2 border-b">
                <span className="text-sm text-fg-subtle">Dimensions</span>
                <span className="text-sm font-medium text-fg">{config.vectorConfig.dimensions}</span>
              </div>
              <div className="flex justify-between py-2">
                <span className="text-sm text-fg-subtle">API URL</span>
                <span className="text-sm font-medium text-fg font-mono text-right break-all">
                  {config.vectorConfig.apiUrl}
                </span>
              </div>
            </div>
          </div>
        )}

        {/* Refresh Note */}
        <div className="bg-info-soft border border-info-line rounded-lg p-4">
          <p className="text-sm text-info-strong">
            <strong>Note:</strong> Configuration changes require server restart to take effect.
            These settings are read from environment variables at startup.
          </p>
        </div>
      </div>
    </div>
  );
}
