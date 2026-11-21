/**
 * The Claude tab of LLM Connections (#440): every way to connect Claude to
 * this server, as variants `{ id, label, filename, content, instructions,
 * note, missing }`. The first is the default. `filename` is null for the
 * variants that are not a file (a command, a URL to paste), which have no
 * Download.
 *
 * Pure functions of `{ mcpUrl, apiKey, apiKeyEnabled, oauthEnabled }`: the
 * MCP endpoint, the key typed in the panel (empty: a placeholder), and what
 * the server reports in GET /v1/config → protocols.mcp. A key is part of an
 * example when keys are on or one was typed.
 *
 * The command is shell-quoted, so a key or URL containing `"`, `$`, a
 * backtick or a quote cannot end its word or run anything; the JSON
 * variants go through JSON.stringify.
 */
import { STDIO_NOTE, stdioDockerConfig } from './llm-connection-templates.js';

/** KEY_PLACEHOLDER stands in the examples for a key not typed yet. */
export const KEY_PLACEHOLDER = 'mcp_…';

/** KEY_HEADER is the header /mcp reads an MCP API key from. */
export const KEY_HEADER = 'X-API-Key';

/** MCP_DOCS is the server-side setup of custom connectors (OAuth). */
export const MCP_DOCS = 'https://github.com/tradik/mddb/blob/main/docs/MCP.md#oauth-21-for-claudeai-connectors-v2181';

// Characters a shell treats specially: inside double quotes (", $, `, \, !)
// or anywhere in a bare word (also whitespace, quotes, globs, ;, |, &, …).
const DOUBLE_QUOTE_UNSAFE = /["$`\\!\r\n]/;
const BARE_WORD = /^[\w.~:/@%+=,-]+$/;

/** singleQuoted is `value` as one single-quoted shell word. */
function singleQuoted(value) {
  return `'${value.replaceAll("'", String.raw`'\''`)}'`;
}

/**
 * shellWord is `value` as one shell word: bare when it has nothing special,
 * in double quotes when they are safe for it (the form the docs show),
 * else in single quotes.
 */
export function shellWord(value, preferDouble = false) {
  if (!preferDouble && BARE_WORD.test(value)) return value;
  if (preferDouble && !DOUBLE_QUOTE_UNSAFE.test(value)) return `"${value}"`;
  return singleQuoted(value);
}

/** keyOf is the key an example carries, or null when it carries none. */
function keyOf({ apiKey, apiKeyEnabled }) {
  if (apiKey) return apiKey;
  return apiKeyEnabled ? KEY_PLACEHOLDER : null;
}

/** commandVariant is `claude mcp add` for Claude Code. */
function commandVariant(mcpUrl, key) {
  let header = '';
  if (key !== null) {
    const headerWord = shellWord(`${KEY_HEADER}: ${key}`, true);
    header = ` --header ${headerWord}`;
  }
  return {
    id: 'command',
    label: 'Claude Code (command)',
    filename: null,
    content: `claude mcp add --transport http mddb ${shellWord(mcpUrl)}${header}`,
    instructions: [
      'Run this command in a terminal',
      'Check it with claude mcp list: mddb should be listed as connected',
      'Add --scope user to use it in all your projects, or --scope project to share it with the project through .mcp.json',
    ],
  };
}

/** projectFileVariant is a project-level `.mcp.json`. */
function projectFileVariant(mcpUrl, key) {
  const server = { type: 'http', url: mcpUrl };
  if (key !== null) server.headers = { [KEY_HEADER]: key };
  const instructions = ['Save this file as .mcp.json in the root of your project'];
  if (key !== null) {
    instructions.push(`Do not commit a real key: write "${KEY_HEADER}": "\${MDDB_MCP_API_KEY}" and set MDDB_MCP_API_KEY in your environment`);
  }
  instructions.push('Check it with claude mcp list, run in that directory: mddb should be listed as connected');
  return {
    id: 'project-file',
    label: 'Project file',
    filename: '.mcp.json',
    content: JSON.stringify({ mcpServers: { mddb: server } }, null, 2),
    instructions,
  };
}

/** connectorVariant is the URL to add as a custom connector, or what the server lacks for one. */
function connectorVariant(mcpUrl, { apiKeyEnabled, oauthEnabled }) {
  const variant = { id: 'connector', label: 'Custom connector', filename: null, content: mcpUrl };
  if (!oauthEnabled) {
    const settings = apiKeyEnabled ? [] : ['MDDB_MCP_API_KEY_ENABLED=true'];
    variant.missing = [
      ...settings,
      'MDDB_MCP_OAUTH_ENABLED=true',
      'MDDB_MCP_OAUTH_ISSUER=<the public https URL of the MCP listener>',
      'MDDB_MCP_OAUTH_SECRET=<at least 32 random characters>',
    ];
    variant.instructions = [];
    return variant;
  }
  variant.instructions = [
    'In Claude (web or desktop app), open Customize → Connectors, select Add, then Add custom connector',
    'Enter a name (for example mddb) and paste the URL above as the remote MCP server URL, then click Continue',
    'Keep the authentication settings marked Detected and click Add',
    'Click Connect; on the page this server opens, paste an MCP API key',
    'Team and Enterprise plans: an Owner adds it under Organization settings → Connectors (Add, then Custom), and members click Connect',
    "Claude connects to the URL from Anthropic's servers, not from your computer: it must be an https address reachable from the internet",
  ];
  return variant;
}

/** Today's (2.19.8) Claude Desktop setup steps, shared by both stdio variants. */
const STDIO_INSTRUCTIONS = [
  'Copy this configuration to your Claude Desktop config file:',
  'macOS: ~/Library/Application Support/Claude/claude_desktop_config.json',
  'Windows: %APPDATA%\\Claude\\claude_desktop_config.json',
  'Linux: ~/.config/Claude/claude_desktop_config.json',
  'Restart Claude Desktop to apply changes',
];

/** stdioBinaryConfig runs a local mddbd binary over stdio. */
function stdioBinaryConfig() {
  return JSON.stringify({
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
  }, null, 2);
}

/** claudeVariants lists the Claude tab's variants, the command first. */
export function claudeVariants({ mcpUrl, apiKey = '', apiKeyEnabled = false, oauthEnabled = false }) {
  const key = keyOf({ apiKey, apiKeyEnabled });
  return [
    commandVariant(mcpUrl, key),
    projectFileVariant(mcpUrl, key),
    connectorVariant(mcpUrl, { apiKeyEnabled, oauthEnabled }),
    {
      id: 'stdio-docker',
      label: 'Local stdio (Docker)',
      filename: 'claude_desktop_config.json',
      content: stdioDockerConfig(),
      instructions: STDIO_INSTRUCTIONS,
      note: STDIO_NOTE,
    },
    {
      id: 'stdio-binary',
      label: 'Local stdio (binary)',
      filename: 'claude_desktop_config.json',
      content: stdioBinaryConfig(),
      instructions: STDIO_INSTRUCTIONS,
      note: STDIO_NOTE,
    },
  ];
}
