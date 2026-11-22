/**
 * The VS Code tab of LLM Connections (2.19.10): the chat built into VS Code
 * reaches this server over MCP (Streamable HTTP), as variants
 * `{ id, label, filename, content, instructions }` like the Claude tab.
 *
 * The workspace file never holds a key: it declares a password input that
 * VS Code asks for once and keeps in its secret storage, so `.vscode/mcp.json`
 * can be committed. The `code --add-mcp` command writes the user profile, so
 * it carries the key typed in the panel (or a placeholder) as a header.
 */
import { KEY_HEADER, KEY_PLACEHOLDER, shellWord } from './claude-connection.js';

/** INPUT_ID is the input the workspace file asks the key through. */
export const INPUT_ID = 'mddbMcpKey';

/** workspaceFileVariant is `.vscode/mcp.json`, the key asked for by VS Code. */
function workspaceFileVariant(mcpUrl, withKey) {
  const server = { type: 'http', url: mcpUrl };
  const file = { servers: { mddb: server } };
  const instructions = ['Save this file as .vscode/mcp.json in your workspace'];
  if (withKey) {
    server.headers = { [KEY_HEADER]: `\${input:${INPUT_ID}}` };
    file.inputs = [{ type: 'promptString', id: INPUT_ID, description: 'MDDB MCP API key', password: true }];
    instructions.push('On first use VS Code asks for the MCP API key and keeps it in its secret storage: the file holds no key and can be committed');
  }
  instructions.push(
    'Open the Chat view, switch to Agent mode and pick the mddb tools under Configure Tools',
    'Run MCP: List Servers from the Command Palette to start, stop or inspect the server',
  );
  return {
    id: 'workspace-file',
    label: 'Workspace file',
    filename: 'mcp.json',
    content: JSON.stringify({ ...(file.inputs ? { inputs: file.inputs } : {}), servers: file.servers }, null, 2),
    instructions,
  };
}

/** userCommandVariant adds the server to the VS Code user profile. */
function userCommandVariant(mcpUrl, key) {
  const server = { name: 'mddb', type: 'http', url: mcpUrl };
  if (key !== null) server.headers = { [KEY_HEADER]: key };
  return {
    id: 'command',
    label: 'User profile (command)',
    filename: null,
    content: `code --add-mcp ${shellWord(JSON.stringify(server))}`,
    instructions: [
      'Run this command in a terminal (POSIX shell quoting) to add mddb to every workspace',
      'The key is stored in your user profile mcp.json: prefer the workspace file to keep it in secret storage',
    ],
  };
}

/** vscodeVariants lists the VS Code tab's variants, the workspace file first. */
export function vscodeVariants({ mcpUrl, apiKey = '', apiKeyEnabled = false }) {
  const withKey = apiKeyEnabled || apiKey !== '';
  let key = null;
  if (apiKey) key = apiKey;
  else if (apiKeyEnabled) key = KEY_PLACEHOLDER;
  return [workspaceFileVariant(mcpUrl, withKey), userCommandVariant(mcpUrl, key)];
}
