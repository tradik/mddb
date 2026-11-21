import { useEffect, useState } from 'react';
import { Code, Copy, Download, Check, Brain } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import MCPAPIKeysPanel from './MCPAPIKeysPanel';
import { withServerDomain, urlOnHost, mcpBaseUrl, mcpEndpoint } from '../lib/server-address';
import { connectionTemplate } from '../lib/llm-connection-templates';
import { claudeVariants, MCP_DOCS } from '../lib/claude-connection';

const TABS = [
  { id: 'mcp', label: 'MCP Config', desc: 'Custom MCP tools configuration (YAML)' },
  { id: 'api-keys', label: 'API Keys', desc: 'Manage MCP API keys' },
  { id: 'claude', label: 'Claude', desc: 'Claude Code, claude.ai and Claude Desktop' },
  { id: 'chatgpt', label: 'ChatGPT', desc: 'OpenAI ChatGPT with MCP bridge' },
  { id: 'ollama', label: 'Ollama (Python)', desc: 'Local Ollama with Python MCP client' },
  { id: 'deepseek', label: 'DeepSeek', desc: 'DeepSeek AI agent connection' },
  { id: 'manus', label: 'Manus', desc: 'Manus AI agent configuration' },
  { id: 'bielik', label: 'Bielik.ai', desc: 'Bielik.ai Polish LLM agent' },
  { id: 'openwebui', label: 'Open WebUI', desc: 'Open WebUI RAG pipeline model' },
];

const smallButton = 'flex items-center px-3 py-1.5 text-sm rounded transition-colors focus:outline-none focus:ring-2 focus:ring-focus';
const variantButton = (active) => `px-2 py-1 text-xs rounded focus:outline-none focus:ring-2 focus:ring-focus ${active ? 'bg-primary-700 text-white' : 'text-fg-soft hover:bg-surface-subtle'}`;

/**
 * ClaudeKeyField is the MCP API key the Claude examples carry. It lives in
 * the page's state only — never in storage or the URL — and is gone when the
 * page closes. Optional when the server does not say whether keys are on
 * (before 2.19.9).
 */
function ClaudeKeyField({ value, onChange, optional, onCreate }) {
  const [shown, setShown] = useState(false);
  return (
    <div className="px-4 py-3 border-b border-line-soft">
      <label htmlFor="claude-api-key" className="block text-sm font-medium text-fg-muted mb-1">
        API key{optional ? ' (optional)' : ''}
      </label>
      <div className="flex items-center gap-2">
        <input
          id="claude-api-key"
          type={shown ? 'text' : 'password'}
          autoComplete="off"
          spellCheck={false}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="mcp_…"
          className="flex-1 px-3 py-2 border border-line-strong rounded-md text-sm font-mono focus:ring-focus focus:border-primary-500"
        />
        <button type="button" aria-pressed={shown} onClick={() => setShown(!shown)} className={`${smallButton} bg-surface-subtle hover:bg-surface-strong`}>
          {shown ? 'Hide key' : 'Show key'}
        </button>
      </div>
      <p className="text-xs text-fg-subtle mt-1">
        Used only in the examples on this page, not saved.
        {!value && (
          <>
            {' '}
            <button type="button" onClick={onCreate} className="text-accent underline focus:outline-none focus:ring-2 focus:ring-focus rounded">Create a key</button>
          </>
        )}
      </p>
    </div>
  );
}

/** SetupSteps is a variant's steps and note, or the server settings it lacks. */
function SetupSteps({ variant }) {
  if (variant.missing) {
    return (
      <div className="bg-warning-soft border border-warning-line rounded-lg p-4 mb-6 text-sm text-warning-strong">
        <h3 className="font-semibold mb-2">Custom connectors need OAuth, which is off on this server</h3>
        <p className="mb-2">Set on the server and restart it:</p>
        <ul className="space-y-1 mb-2">
          {variant.missing.map((setting) => (
            <li key={setting}><code className="font-mono">{setting}</code></li>
          ))}
        </ul>
        <a href={MCP_DOCS} target="_blank" rel="noopener noreferrer" className="underline">OAuth 2.1 for claude.ai connectors (docs/MCP.md)</a>
      </div>
    );
  }
  return (
    <div className="bg-info-soft border border-info-line rounded-lg p-4 mb-6">
      <h3 className="text-sm font-semibold text-info-strong mb-2">Setup Instructions:</h3>
      <ol className="text-sm text-info-strong space-y-1 list-decimal list-inside">
        {variant.instructions.map((step) => (
          <li key={step}>{step}</li>
        ))}
      </ol>
      {variant.note && <p className="text-sm text-info-strong font-semibold mt-2">{variant.note}</p>}
    </div>
  );
}

export default function MCPConfigPanel() {
  const { config } = useStore();
  const [activeTab, setActiveTab] = useState('mcp');
  const [copied, setCopied] = useState(false);
  const [variantId, setVariantId] = useState(null);
  const [mcpYaml, setMcpYaml] = useState(null);
  const [mcpLoading, setMcpLoading] = useState(true);
  // null until edited: the field shows MDDB_MCP_DOMAIN, else the page's host.
  const [editedDomain, setDomain] = useState(null);
  const [apiKey, setApiKey] = useState('');

  useEffect(() => {
    // A promise chain: its state updates land in callbacks, after the request.
    void mddbClient.getMCPConfigText()
      .then(setMcpYaml)
      .catch(() => {}) // the generated config is shown instead
      .finally(() => setMcpLoading(false));
  }, []);

  const mcp = config?.protocols?.mcp ?? {};
  const domain = editedDomain ?? (mcp.domain || window.location.hostname || 'localhost');
  const httpAddr = config?.protocols?.http?.addr || config?.httpAddr || ':11023';
  const host = domain || window.location.hostname || 'localhost';
  const address = { publicUrl: mcp.publicUrl || '', domain: host, mcpAddr: mcp.addr || ':9000' };
  const mcpUrl = mcpEndpoint(address);
  // A server before 2.19.9 does not say whether keys are on: the key is optional.
  const keysKnown = typeof mcp.apiKeyEnabled === 'boolean';
  const keyField = activeTab === 'claude' && (mcp.apiKeyEnabled === true || !keysKnown);

  const variants = activeTab === 'claude'
    ? claudeVariants({ mcpUrl, apiKey: keyField ? apiKey : '', apiKeyEnabled: mcp.apiKeyEnabled === true, oauthEnabled: mcp.oauthEnabled === true })
    : [connectionTemplate(activeTab, urlOnHost(host, httpAddr), mcpBaseUrl(address))];
  const variant = variants.find((v) => v.id === variantId) ?? variants[0];

  // The MCP tab prefers the server's live config, with its addresses on the
  // Server Domain like every other example here (2.19.6).
  let displayContent = variant.content;
  if (activeTab === 'mcp' && mcpYaml) displayContent = withServerDomain(mcpYaml, host);

  const selectTab = (id) => {
    setActiveTab(id);
    setVariantId(null);
    setCopied(false);
  };

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(displayContent);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (error) {
      console.error('Failed to copy:', error);
    }
  };

  const handleDownload = () => {
    const blob = new Blob([displayContent], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = variant.filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  const currentTabInfo = TABS.find(t => t.id === activeTab);

  return (
    <div className="h-full overflow-y-auto bg-surface-muted p-6">
      <div className="max-w-4xl mx-auto">
        <div className="mb-6">
          <h1 className="text-2xl font-bold text-fg mb-2">LLM Connections</h1>
          <p className="text-fg-subtle">Configuration for connecting MDDB to AI agents and LLM tools</p>
        </div>

        {/* Domain Input */}
        <div className="bg-surface rounded-lg shadow mb-6 p-4">
          <label htmlFor="mcp-server-domain" className="block text-sm font-medium text-fg-muted mb-1">Server Domain</label>
          <div className="flex items-center gap-3">
            <input
              id="mcp-server-domain"
              type="text"
              value={domain}
              onChange={(e) => setDomain(e.target.value)}
              placeholder="e.g. myserver.com or https://myserver.com"
              className="flex-1 px-3 py-2 border border-line-strong rounded-md text-sm focus:ring-focus focus:border-primary-500"
            />
            <span className="text-xs text-fg-subtle whitespace-nowrap">All examples below use this domain</span>
          </div>
          <p className="text-xs text-fg-faint mt-1">
            A host name, or a URL (<code className="bg-surface-subtle px-1 rounded">https://host</code>) for a server behind a TLS proxy or tunnel.
            Set <code className="bg-surface-subtle px-1 rounded">MDDB_MCP_DOMAIN</code> env var to persist this value.
          </p>
          <p className="text-sm text-fg-muted mt-2">
            MCP URL: <code className="bg-surface-subtle px-1 rounded font-mono" data-testid="mcp-url">{mcpUrl}</code>
            {address.publicUrl && <span className="text-xs text-fg-subtle"> (from MDDB_MCP_PUBLIC_URL on the server)</span>}
          </p>
        </div>

        {/* Tabs */}
        <div className="bg-surface rounded-lg shadow mb-6">
          <div className="border-b border-line overflow-x-auto">
            <div className="flex min-w-max">
              {TABS.map((tab) => (
                <button
                  key={tab.id}
                  onClick={() => selectTab(tab.id)}
                  className={`px-4 py-3 text-sm font-medium whitespace-nowrap border-b-2 transition-colors ${activeTab === tab.id
                    ? 'border-primary-600 text-accent bg-accent-soft'
                    : 'border-transparent text-fg-faint hover:text-fg-muted hover:border-line-strong'
                    }`}
                >
                  {tab.label}
                </button>
              ))}
            </div>
          </div>

          {activeTab === 'api-keys' ? (
            <div className="p-4">
              <MCPAPIKeysPanel onKeyCreated={setApiKey} />
            </div>
          ) : (
            <>
              {/* Tab Description */}
              <div className="px-4 py-3 border-b border-line-soft flex items-center justify-between">
                <div className="flex items-center text-sm text-fg-subtle">
                  <Brain className="w-4 h-4 mr-2 text-accent" />
                  <span>{currentTabInfo?.desc}</span>
                </div>
                <div className="flex items-center space-x-2">
                  <button type="button" onClick={handleCopy} className={`${smallButton} bg-surface-subtle hover:bg-surface-strong`}>
                    {copied ? (
                      <>
                        <Check className="w-3.5 h-3.5 mr-1 text-success" />
                        <span className="text-success">Copied!</span>
                      </>
                    ) : (
                      <>
                        <Copy className="w-3.5 h-3.5 mr-1" />
                        <span>Copy</span>
                      </>
                    )}
                  </button>
                  {variant.filename && (
                    <button type="button" onClick={handleDownload} className={`${smallButton} bg-primary-600 text-white hover:bg-primary-700`}>
                      <Download className="w-3.5 h-3.5 mr-1" />
                      <span>Download</span>
                    </button>
                  )}
                </div>
              </div>

              {variants.length > 1 && (
                <fieldset className="px-4 py-3 border-b border-line-soft flex flex-wrap gap-1">
                  <legend className="sr-only">How to connect</legend>
                  {variants.map((v) => (
                    <button key={v.id} type="button" aria-pressed={v.id === variant.id} onClick={() => { setVariantId(v.id); setCopied(false); }} className={variantButton(v.id === variant.id)}>
                      {v.label}
                    </button>
                  ))}
                </fieldset>
              )}

              {keyField && (
                <ClaudeKeyField value={apiKey} onChange={setApiKey} optional={!keysKnown} onCreate={() => selectTab('api-keys')} />
              )}

              {/* Code Block */}
              <div className="bg-gray-900">
                <div className="px-4 py-2 border-b border-gray-700 flex items-center">
                  <Code className="w-3.5 h-3.5 text-fg-faint mr-2" />
                  <span className="text-xs text-fg-faint font-mono">{variant.filename ?? variant.label}</span>
                </div>
                <div className="p-4 overflow-x-auto">
                  {activeTab === 'mcp' && mcpLoading ? (
                    <div className="flex items-center justify-center py-8">
                      <div className="animate-spin rounded-full h-6 w-6 border-b-2 border-line-strong"></div>
                    </div>
                  ) : (
                    <pre className="text-sm text-gray-100 font-mono whitespace-pre">
                      <code>{displayContent}</code>
                    </pre>
                  )}
                </div>
              </div>
            </>
          )}
        </div>

        {activeTab !== 'api-keys' && <SetupSteps variant={variant} />}

        {/* Documentation Link */}
        <div className="bg-surface-muted border border-line rounded-lg p-4">
          <p className="text-sm text-fg-muted">
            For detailed setup guides, see the{' '}
            <a
              href="https://github.com/tradik/mddb/blob/main/docs/LLM_CONNECTIONS.md"
              target="_blank"
              rel="noopener noreferrer"
              className="text-accent hover:text-accent underline"
            >
              LLM Connections documentation
            </a>.
          </p>
        </div>
      </div>
    </div>
  );
}
