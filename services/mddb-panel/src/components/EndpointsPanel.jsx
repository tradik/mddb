import { useEffect, useState } from 'react';
import { Network, Lock, Unlock, ExternalLink } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';

export default function EndpointsPanel() {
  const {
    endpoints,
    endpointsLoading,
    endpointsError,
    setEndpoints,
    setEndpointsLoading,
    setEndpointsError,
    config,
  } = useStore();

  const [activeTab, setActiveTab] = useState('http');

  useEffect(() => {
    loadEndpoints();
  }, []);

  const loadEndpoints = async () => {
    setEndpointsLoading(true);
    setEndpointsError(null);
    try {
      const data = await mddbClient.getEndpoints();
      setEndpoints(data);
    } catch (error) {
      setEndpointsError(error.message);
      console.error('Failed to load endpoints:', error);
    } finally {
      setEndpointsLoading(false);
    }
  };

  const Tab = ({ id, label, count }) => (
    <button
      onClick={() => setActiveTab(id)}
      className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
        activeTab === id
          ? 'border-primary-600 text-accent'
          : 'border-transparent text-fg-subtle hover:text-fg hover:border-line-strong'
      }`}
    >
      {label} <span className="text-xs text-fg-faint">({count})</span>
    </button>
  );

  if (endpointsLoading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-primary-600"></div>
      </div>
    );
  }

  if (endpointsError) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <div className="text-danger font-medium mb-2">Failed to load endpoints</div>
          <div className="text-sm text-fg-faint">{endpointsError}</div>
          <button
            onClick={loadEndpoints}
            className="mt-4 px-4 py-2 bg-primary-600 text-white rounded hover:bg-primary-700"
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  if (!endpoints) return null;

  return (
    <div className="h-full flex flex-col bg-surface-muted">
      <div className="bg-surface border-b px-6 py-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-fg mb-1">API Endpoints</h1>
            <p className="text-fg-subtle">Documentation for all available endpoints</p>
          </div>
          <a
            href={`https://github.com/tradik/mddb/blob/${config?.version ? `v${config.version}` : 'main'}/docs/openapi.yaml`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium text-accent bg-accent-soft border border-accent-line rounded-lg hover:bg-accent-muted transition-colors"
          >
            <ExternalLink className="w-4 h-4" />
            OpenAPI Spec {config?.version && <span className="text-xs text-accent">v{config.version}</span>}
          </a>
        </div>
      </div>

      {/* Tabs */}
      <div className="bg-surface border-b px-6">
        <div className="flex space-x-6">
          <Tab id="http" label="HTTP" count={endpoints.http?.length || 0} />
          <Tab id="grpc" label="gRPC" count={endpoints.grpc?.length || 0} />
          <Tab id="mcp" label="MCP" count={endpoints.mcp?.length || 0} />
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-6">
        <div className="max-w-6xl mx-auto">
          {activeTab === 'http' && (
            <div className="bg-surface rounded-lg shadow overflow-hidden">
              <table className="min-w-full divide-y divide-line">
                <thead className="bg-surface-muted">
                  <tr>
                    <th className="px-6 py-3 text-left text-xs font-medium text-fg-faint uppercase tracking-wider">
                      Method
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-medium text-fg-faint uppercase tracking-wider">
                      Path
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-medium text-fg-faint uppercase tracking-wider">
                      Description
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-medium text-fg-faint uppercase tracking-wider">
                      Auth
                    </th>
                  </tr>
                </thead>
                <tbody className="bg-surface divide-y divide-line">
                  {endpoints.http?.map((endpoint, idx) => (
                    <tr key={idx} className="hover:bg-surface-muted">
                      <td className="px-6 py-4 whitespace-nowrap">
                        <span className="px-2 inline-flex text-xs leading-5 font-semibold rounded-full bg-info-muted text-info-strong">
                          {endpoint.method}
                        </span>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-sm font-mono text-fg">
                        {endpoint.path}
                      </td>
                      <td className="px-6 py-4 text-sm text-fg-muted">
                        {endpoint.description}
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        {endpoint.requiresAuth ? (
                          <Lock className="w-4 h-4 text-warning" title="Requires authentication" />
                        ) : (
                          <Unlock className="w-4 h-4 text-success" title="Public" />
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {activeTab === 'grpc' && (
            <div className="grid gap-4">
              {endpoints.grpc?.map((method, idx) => (
                <div key={idx} className="bg-surface rounded-lg shadow p-4">
                  <div className="flex items-start">
                    <Network className="w-5 h-5 text-accent mr-3 mt-0.5" />
                    <div className="flex-1">
                      <h3 className="font-mono text-sm font-semibold text-fg mb-1">
                        {method.name}
                      </h3>
                      <p className="text-sm text-fg-subtle">{method.description}</p>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}

          {activeTab === 'mcp' && (
            <div className="grid gap-4">
              {endpoints.mcp?.map((tool, idx) => (
                <div key={idx} className="bg-surface rounded-lg shadow p-4">
                  <div className="flex items-start">
                    <div className="bg-note-muted rounded p-2 mr-3">
                      <Network className="w-4 h-4 text-note" />
                    </div>
                    <div className="flex-1">
                      <h3 className="font-mono text-sm font-semibold text-fg mb-1">
                        {tool.name}
                      </h3>
                      <p className="text-sm text-fg-subtle">{tool.description}</p>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
