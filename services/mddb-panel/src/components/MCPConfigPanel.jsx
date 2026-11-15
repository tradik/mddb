import { useEffect, useState } from 'react';
import { Code, Copy, Download, Check, Brain } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import mddbModelPyRaw from '@scripts/mddb_model.py?raw';
import MCPAPIKeysPanel from './MCPAPIKeysPanel';
import { withServerDomain, urlOnHost } from '../lib/server-address';

const TABS = [
  { id: 'mcp', label: 'MCP Config', desc: 'Custom MCP tools configuration (YAML)' },
  { id: 'api-keys', label: 'API Keys', desc: 'Manage MCP API keys' },
  { id: 'claude', label: 'Claude Desktop', desc: 'Anthropic Claude Desktop / Claude Code' },
  { id: 'chatgpt', label: 'ChatGPT', desc: 'OpenAI ChatGPT with MCP bridge' },
  { id: 'ollama', label: 'Ollama (Python)', desc: 'Local Ollama with Python MCP client' },
  { id: 'deepseek', label: 'DeepSeek', desc: 'DeepSeek AI agent connection' },
  { id: 'manus', label: 'Manus', desc: 'Manus AI agent configuration' },
  { id: 'bielik', label: 'Bielik.ai', desc: 'Bielik.ai Polish LLM agent' },
  { id: 'openwebui', label: 'Open WebUI', desc: 'Open WebUI RAG pipeline model' },
];

/**
 * generateConfig is a tab's example: file name, content and setup steps, with
 * the HTTP API at `httpBase` and MCP at `mcpBase` (both full URLs on the
 * Server Domain).
 */
function generateConfig(tabId, httpBase, mcpBase) {
  switch (tabId) {
    case 'mcp':
      return {
        filename: 'mcp_config.yaml',
        content: `# MDDB MCP Custom Tools Configuration
# Set MDDB_MCP_CONFIG=/path/to/this/file.yaml to load

# Custom tools wrap built-in tools with preset defaults
# They appear alongside built-in tools in the MCP tool list
custom_tools:
  - name: "kb_search"
    description: "Search the knowledge base using semantic similarity"
    action: "semantic_search"
    defaults:
      collection: "docs"
      top_k: 5
      threshold: 0.7
      includeContent: true
    parameters:
      - name: "query"
        type: "string"
        description: "Search query"
        required: true

  - name: "kb_lookup"
    description: "Full-text keyword search in the knowledge base"
    action: "full_text_search"
    defaults:
      collection: "docs"
      limit: 10
      algorithm: "bm25"
      # Token-saving projection (v2.10.2+): drop the body and return only
      # the meta keys you need. includeContent defaults true; fields empty
      # returns all meta. Both cut client token usage on narrow lookups.
      includeContent: false
      fields: ["title", "url", "category"]
    parameters:
      - name: "query"
        type: "string"
        description: "Search keywords"
        required: true`,
        instructions: [
          'MCP is now built into the MDDB server — no separate service needed',
          'Custom tools are optional — save this file and set MDDB_MCP_CONFIG env var',
          'For stdio mode (Claude Desktop): set MDDB_MCP_STDIO=true',
          'For HTTP mode: MCP server runs on port 9000 by default (MDDB_MCP_ADDR)',
          'Streamable HTTP (2025-11-25): POST/GET /mcp — new standard transport',
          'Legacy SSE (2024-11-05): GET /sse + POST /message — still supported',
        ],
      };

    case 'claude':
      return {
        filename: 'claude_desktop_config.json',
        content: JSON.stringify({
          mcpServers: {
            mddb: {
              command: 'docker',
              args: [
                'run', '-i', '--rm', '--network', 'host',
                '-v', 'mddb-data:/app/data',
                '-e', 'MDDB_MCP_STDIO=true',
                'tradik/mddb:latest',
              ],
              env: {},
            },
          },
        }, null, 2),
        instructions: [
          'Copy this configuration to your Claude Desktop config file:',
          'macOS: ~/Library/Application Support/Claude/claude_desktop_config.json',
          'Windows: %APPDATA%\\Claude\\claude_desktop_config.json',
          'Linux: ~/.config/Claude/claude_desktop_config.json',
          'Restart Claude Desktop to apply changes',
        ],
        alt: {
          label: 'Local Binary (no Docker)',
          content: JSON.stringify({
            mcpServers: {
              mddb: {
                command: '/path/to/mddbd',
                args: [],
                env: {
                  MDDB_MCP_STDIO: 'true',
                  MDDB_PATH: '/path/to/mddb.db',
                },
              },
            },
          }, null, 2),
        },
      };

    case 'chatgpt':
      return {
        filename: 'openai_actions.json',
        content: JSON.stringify({
          openapi: '3.1.0',
          info: { title: 'MDDB API', version: '2.9.14', description: 'AI-Native Document Database API' },
          servers: [{ url: httpBase }],
          paths: {
            '/v1/search': {
              post: {
                operationId: 'searchDocuments',
                summary: 'Search documents by metadata',
                requestBody: {
                  required: true,
                  content: {
                    'application/json': {
                      schema: {
                        type: 'object', properties: {
                          collection: { type: 'string' },
                          filterMeta: { type: 'object' },
                          sort: { type: 'string' },
                          limit: { type: 'integer', default: 10 },
                        }
                      }
                    }
                  },
                },
              },
            },
            '/v1/vector-search': {
              post: {
                operationId: 'semanticSearch',
                summary: 'Semantic search using vector embeddings',
                requestBody: {
                  required: true,
                  content: {
                    'application/json': {
                      schema: {
                        type: 'object', properties: {
                          collection: { type: 'string' },
                          query: { type: 'string' },
                          topK: { type: 'integer', default: 5 },
                          threshold: { type: 'number', default: 0.7 },
                        }
                      }
                    }
                  },
                },
              },
            },
          },
        }, null, 2),
        instructions: [
          'Go to https://platform.openai.com/gpts and create or edit a Custom GPT',
          'In "Configure" tab, scroll to "Actions" and click "Create new action"',
          'Paste this OpenAPI schema into the schema editor',
          `Make sure your MDDB server at ${httpBase} is publicly accessible`,
          'Save and test the GPT with prompts like "Search my docs for..."',
        ],
      };

    case 'ollama':
      return {
        filename: 'mddb_ollama_agent.py',
        content: `#!/usr/bin/env python3
"""MDDB + Ollama RAG Agent

Uses Ollama for LLM inference and MDDB for document retrieval.
Supports semantic search, full-text search, and metadata queries.

Requirements:
  pip install requests ollama
"""

import requests
import ollama

MDDB_URL = "${httpBase}"
OLLAMA_MODEL = "llama3.2"  # or any model you have pulled

def search_mddb(query, collection="docs", top_k=5):
    """Semantic search via MDDB vector endpoint."""
    resp = requests.post(f"{MDDB_URL}/v1/vector-search", json={
        "collection": collection,
        "query": query,
        "topK": top_k,
        "threshold": 0.6,
        "includeContent": True,
    })
    resp.raise_for_status()
    return resp.json().get("results", [])

def fts_search(query, collection="docs", limit=10):
    """Full-text search via MDDB."""
    resp = requests.post(f"{MDDB_URL}/v1/fts", json={
        "collection": collection,
        "query": query,
        "limit": limit,
    })
    resp.raise_for_status()
    return resp.json().get("results", [])

def ask(question, collection="docs"):
    """RAG: retrieve context from MDDB, then answer with Ollama."""
    results = search_mddb(question, collection)
    context = "\\n\\n---\\n\\n".join(
        f"## {r.get('key', 'unknown')}\\n{r.get('contentMd', '')[:2000]}"
        for r in results
    )
    prompt = f"""Answer the question using ONLY the context below.
If the context doesn't contain the answer, say so.

Context:
{context}

Question: {question}
Answer:"""

    response = ollama.chat(model=OLLAMA_MODEL, messages=[
        {"role": "user", "content": prompt},
    ])
    return response["message"]["content"]

if __name__ == "__main__":
    import sys
    question = " ".join(sys.argv[1:]) or "What is MDDB?"
    print(ask(question))
`,
        instructions: [
          'Install dependencies: pip install requests ollama',
          'Make sure Ollama is running: ollama serve',
          'Pull a model: ollama pull llama3.2',
          `Ensure MDDB is running at ${httpBase}`,
          'Run: python mddb_ollama_agent.py "your question here"',
        ],
      };

    case 'deepseek':
      return {
        filename: 'deepseek_mcp_config.json',
        content: JSON.stringify({
          mcpServers: {
            mddb: {
              command: 'docker',
              args: [
                'run', '-i', '--rm', '--network', 'host',
                '-v', 'mddb-data:/app/data',
                '-e', 'MDDB_MCP_STDIO=true',
                'tradik/mddb:latest',
              ],
              env: {},
            },
          },
        }, null, 2),
        instructions: [
          'DeepSeek supports MCP via compatible clients (Cline, Continue, etc.)',
          'Add this config to your MCP client settings (e.g., ~/.cline/mcp.json)',
          'Use DeepSeek as the LLM provider in your MCP client',
          'The MDDB tools will be available for document search and retrieval',
        ],
      };

    case 'manus':
      return {
        filename: 'manus_mddb_tool.yaml',
        content: `# Manus Agent - MDDB Tool Configuration
# Add this to your Manus agent's tool definitions

name: mddb_search
description: "Search the MDDB knowledge base for relevant documents"
type: api
endpoint: "${httpBase}/v1/vector-search"
method: POST
headers:
  Content-Type: "application/json"
body:
  collection: "docs"
  query: "{{input}}"
  topK: 5
  threshold: 0.6
  includeContent: true

---

name: mddb_fts
description: "Full-text search in MDDB documents"
type: api
endpoint: "${httpBase}/v1/fts"
method: POST
headers:
  Content-Type: "application/json"
body:
  collection: "docs"
  query: "{{input}}"
  limit: 10

---

name: mddb_get_document
description: "Retrieve a specific document from MDDB"
type: api
endpoint: "${httpBase}/v1/get"
method: POST
headers:
  Content-Type: "application/json"
body:
  collection: "{{collection}}"
  key: "{{key}}"`,
        instructions: [
          'Add these tool definitions to your Manus agent configuration',
          `Make sure MDDB is accessible at ${httpBase}`,
          'The agent can use semantic search, full-text search, and document retrieval',
          'Adjust collection names and parameters to match your data',
        ],
      };

    case 'bielik':
      return {
        filename: 'bielik_mddb_config.py',
        content: `#!/usr/bin/env python3
"""Bielik.ai + MDDB Integration

Connects Bielik Polish LLM to MDDB for RAG-powered responses.
Bielik excels at Polish language tasks and understanding.

Requirements:
  pip install requests
"""

import requests

MDDB_URL = "${httpBase}"
BIELIK_API_URL = "https://api.bielik.ai/v1"
BIELIK_API_KEY = "your-bielik-api-key"  # Get from https://bielik.ai
BIELIK_MODEL = "Bielik-11B-v2.3-Instruct"

def search_mddb(query, collection="docs", top_k=5):
    """Semantic search via MDDB."""
    resp = requests.post(f"{MDDB_URL}/v1/vector-search", json={
        "collection": collection,
        "query": query,
        "topK": top_k,
        "threshold": 0.6,
        "includeContent": True,
    })
    resp.raise_for_status()
    return resp.json().get("results", [])

def ask_bielik(question, collection="docs"):
    """RAG: retrieve from MDDB, answer with Bielik."""
    results = search_mddb(question, collection)
    context = "\\n\\n---\\n\\n".join(
        f"## {r.get('key', 'unknown')}\\n{r.get('contentMd', '')[:2000]}"
        for r in results
    )

    resp = requests.post(
        f"{BIELIK_API_URL}/chat/completions",
        headers={
            "Authorization": f"Bearer {BIELIK_API_KEY}",
            "Content-Type": "application/json",
        },
        json={
            "model": BIELIK_MODEL,
            "messages": [
                {"role": "system", "content": "Odpowiadaj na pytania korzystajac z podanego kontekstu. Jesli kontekst nie zawiera odpowiedzi, powiedz o tym."},
                {"role": "user", "content": f"Kontekst:\\n{context}\\n\\nPytanie: {question}"},
            ],
            "temperature": 0.3,
        },
    )
    resp.raise_for_status()
    return resp.json()["choices"][0]["message"]["content"]

if __name__ == "__main__":
    import sys
    question = " ".join(sys.argv[1:]) or "Czym jest MDDB?"
    print(ask_bielik(question))
`,
        instructions: [
          'Get your API key from https://bielik.ai',
          'Install dependencies: pip install requests',
          `Ensure MDDB is running at ${httpBase}`,
          'Set your BIELIK_API_KEY in the script',
          'Run: python bielik_mddb_config.py "Twoje pytanie tutaj"',
        ],
      };

    case 'openwebui':
      return {
        filename: 'mddb_model.py',
        content: mddbModelPyRaw.replace('http://mddb:9000', mcpBase),
        instructions: [
          'Go to Open WebUI → Admin → Functions → Add new function',
          'Paste this script and save it',
          'Configure the Valves (settings) to match your LLM provider',
          'For Ollama: set llmApiUrl to your Ollama address',
          'For OpenAI: set llmProvider=openai, llmApiUrl=https://api.openai.com, llmApiKey=sk-...',
          'For DeepSeek: set llmProvider=deepseek, llmApiUrl=https://api.deepseek.com, llmApiKey=...',
        ],
      };

    default:
      return { filename: 'config.txt', content: '', instructions: [] };
  }
}

export default function MCPConfigPanel() {
  const { config } = useStore();
  const [activeTab, setActiveTab] = useState('mcp');
  const [copied, setCopied] = useState(false);
  const [showAlt, setShowAlt] = useState(false);
  const [mcpYaml, setMcpYaml] = useState(null);
  const [mcpLoading, setMcpLoading] = useState(true);
  const [domain, setDomain] = useState('');

  useEffect(() => {
    const cfgDomain = config?.protocols?.mcp?.domain;
    setDomain(cfgDomain || window.location.hostname || 'localhost');
  }, [config]);

  useEffect(() => {
    loadMCPConfig();
  }, []);

  const loadMCPConfig = async () => {
    try {
      const text = await mddbClient.getMCPConfigText();
      setMcpYaml(text);
    } catch {
      // Will fallback to generated config
    } finally {
      setMcpLoading(false);
    }
  };

  const httpAddr = config?.protocols?.http?.addr || config?.httpAddr || ':11023';
  const mcpAddr = config?.protocols?.mcp?.addr || ':9000';
  const host = domain || window.location.hostname || 'localhost';
  const tabConfig = generateConfig(activeTab, urlOnHost(host, httpAddr), urlOnHost(host, mcpAddr));

  // The MCP tab prefers the server's live config, with its addresses on the
  // Server Domain like every other example here (2.19.6).
  let displayContent = showAlt && tabConfig.alt ? tabConfig.alt.content : tabConfig.content;
  if (activeTab === 'mcp' && mcpYaml) displayContent = withServerDomain(mcpYaml, host);
  const displayFilename = showAlt && tabConfig.alt ? `${tabConfig.filename} (${tabConfig.alt.label})` : tabConfig.filename;

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
    a.download = tabConfig.filename;
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
              placeholder="e.g. myserver.com"
              className="flex-1 px-3 py-2 border border-line-strong rounded-md text-sm focus:ring-focus focus:border-primary-500"
            />
            <span className="text-xs text-fg-subtle whitespace-nowrap">All examples below use this domain</span>
          </div>
          <p className="text-xs text-fg-faint mt-1">Set <code className="bg-surface-subtle px-1 rounded">MDDB_MCP_DOMAIN</code> env var to persist this value.</p>
        </div>

        {/* Tabs */}
        <div className="bg-surface rounded-lg shadow mb-6">
          <div className="border-b border-line overflow-x-auto">
            <div className="flex min-w-max">
              {TABS.map((tab) => (
                <button
                  key={tab.id}
                  onClick={() => { setActiveTab(tab.id); setShowAlt(false); setCopied(false); }}
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
              <MCPAPIKeysPanel />
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
                  {tabConfig.alt && (
                    <button
                      onClick={() => setShowAlt(!showAlt)}
                      className="text-xs px-2 py-1 bg-surface-subtle hover:bg-surface-strong rounded transition-colors"
                    >
                      {showAlt ? 'Docker' : tabConfig.alt.label}
                    </button>
                  )}
                  <button
                    onClick={handleCopy}
                    className="flex items-center px-3 py-1.5 text-sm bg-surface-subtle hover:bg-surface-strong rounded transition-colors"
                  >
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
                  <button
                    onClick={handleDownload}
                    className="flex items-center px-3 py-1.5 text-sm bg-primary-600 text-white hover:bg-primary-700 rounded transition-colors"
                  >
                    <Download className="w-3.5 h-3.5 mr-1" />
                    <span>Download</span>
                  </button>
                </div>
              </div>

              {/* Code Block */}
              <div className="bg-gray-900">
                <div className="px-4 py-2 border-b border-gray-700 flex items-center">
                  <Code className="w-3.5 h-3.5 text-fg-faint mr-2" />
                  <span className="text-xs text-fg-faint font-mono">{displayFilename}</span>
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

        {activeTab !== 'api-keys' && tabConfig.instructions.length > 0 && (
          <div className="bg-info-soft border border-info-line rounded-lg p-4 mb-6">
            <h3 className="text-sm font-semibold text-info-strong mb-2">Setup Instructions:</h3>
            <ol className="text-sm text-info-strong space-y-1 list-decimal list-inside">
              {tabConfig.instructions.map((step, i) => (
                <li key={i}>{step}</li>
              ))}
            </ol>
          </div>
        )}

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
