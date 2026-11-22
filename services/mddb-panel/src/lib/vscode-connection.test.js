// The VS Code tab of LLM Connections (2.19.10).
import { describe, test, expect } from 'vitest';
import { vscodeVariants, INPUT_ID } from './vscode-connection';

const mcpUrl = 'https://db.example.com/mcp';

describe('vscodeVariants', () => {
  test('keys on: the workspace file asks for the key and holds none; the command carries the placeholder', () => {
    const [file, command] = vscodeVariants({ mcpUrl, apiKeyEnabled: true });
    const parsed = JSON.parse(file.content);
    expect(file).toMatchObject({ id: 'workspace-file', filename: 'mcp.json' });
    expect(parsed.inputs).toEqual([{ type: 'promptString', id: INPUT_ID, description: 'MDDB MCP API key', password: true }]);
    expect(parsed.servers.mddb).toEqual({ type: 'http', url: mcpUrl, headers: { 'X-API-Key': `\${input:${INPUT_ID}}` } });
    expect(file.content).not.toContain('mcp_');
    expect(file.instructions.some((i) => i.includes('secret storage'))).toBe(true);
    expect(command.filename).toBeNull();
    expect(command.content).toBe(`code --add-mcp '{"name":"mddb","type":"http","url":"${mcpUrl}","headers":{"X-API-Key":"mcp_…"}}'`);
  });

  test('a typed key goes into the command only, quoted so it cannot end the word', () => {
    const [file, command] = vscodeVariants({ mcpUrl, apiKey: "mcp_a'b" });
    expect(file.content).not.toContain("mcp_a'b");
    expect(command.content).toContain(String.raw`"X-API-Key":"mcp_a'\''b"`);
  });

  test('keys off and none typed: no key anywhere', () => {
    const [file, command] = vscodeVariants({ mcpUrl });
    expect(JSON.parse(file.content)).toEqual({ servers: { mddb: { type: 'http', url: mcpUrl } } });
    expect(file.instructions[1]).toContain('Agent mode');
    expect(command.content).toBe(`code --add-mcp '{"name":"mddb","type":"http","url":"${mcpUrl}"}'`);
  });
});
