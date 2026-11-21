// claudeVariants (#440): the Claude tab's ways to connect — the Claude Code
// command, a project .mcp.json, a custom connector and the two local stdio
// configs — with keys on and off, OAuth on and off, and keys or URLs a shell
// would otherwise read as code.
import { describe, test, expect } from 'vitest';
import { execFileSync } from 'node:child_process';
import { claudeVariants, shellWord, KEY_PLACEHOLDER } from './claude-connection.js';
import { STDIO_NOTE } from './llm-connection-templates.js';

const mcpUrl = 'https://mcp.example.com/mcp';
const byId = (variants) => Object.fromEntries(variants.map((v) => [v.id, v]));

/** shellValue is what `sh` makes of `word`: one argument, printed back. */
const shellValue = (word) => execFileSync('sh', ['-c', `printf '%s' ${word}`], { encoding: 'utf8' });

describe('claudeVariants', () => {
  test('the command comes first; keys on puts the placeholder in every variant that sends one', () => {
    const variants = claudeVariants({ mcpUrl, apiKeyEnabled: true, oauthEnabled: true });
    expect(variants.map((v) => v.id)).toEqual(['command', 'project-file', 'connector', 'stdio-docker', 'stdio-binary']);
    const v = byId(variants);
    expect(v.command.content).toBe(`claude mcp add --transport http mddb https://mcp.example.com/mcp --header "X-API-Key: ${KEY_PLACEHOLDER}"`);
    expect(v.command.filename).toBeNull();
    expect(JSON.parse(v['project-file'].content)).toEqual({
      mcpServers: { mddb: { type: 'http', url: mcpUrl, headers: { 'X-API-Key': KEY_PLACEHOLDER } } },
    });
    expect(v['project-file'].filename).toBe('.mcp.json');
    expect(v['project-file'].instructions.join('\n')).toContain('${MDDB_MCP_API_KEY}');
    expect(v.connector.content).toBe(mcpUrl);
    expect(v.connector.filename).toBeNull();
    expect(v.connector.missing).toBeUndefined();
    expect(v.connector.instructions[0]).toContain('Customize → Connectors');
  });

  test('a typed key replaces the placeholder', () => {
    const v = byId(claudeVariants({ mcpUrl, apiKey: 'mcp_abc123', apiKeyEnabled: true, oauthEnabled: true }));
    expect(v.command.content).toBe('claude mcp add --transport http mddb https://mcp.example.com/mcp --header "X-API-Key: mcp_abc123"');
    expect(JSON.parse(v['project-file'].content).mcpServers.mddb.headers['X-API-Key']).toBe('mcp_abc123');
    for (const variant of Object.values(v)) expect(variant.content).not.toContain(KEY_PLACEHOLDER);
  });

  test('keys off: no header anywhere', () => {
    const v = byId(claudeVariants({ mcpUrl }));
    expect(v.command.content).toBe('claude mcp add --transport http mddb https://mcp.example.com/mcp');
    expect(JSON.parse(v['project-file'].content)).toEqual({ mcpServers: { mddb: { type: 'http', url: mcpUrl } } });
    expect(v['project-file'].instructions.join('\n')).not.toContain('MDDB_MCP_API_KEY');
  });

  test('OAuth off: the connector lists what the server lacks instead of steps', () => {
    const off = byId(claudeVariants({ mcpUrl, apiKeyEnabled: false })).connector;
    expect(off.instructions).toEqual([]);
    expect(off.missing).toEqual([
      'MDDB_MCP_API_KEY_ENABLED=true',
      'MDDB_MCP_OAUTH_ENABLED=true',
      expect.stringMatching(/^MDDB_MCP_OAUTH_ISSUER=/),
      expect.stringMatching(/^MDDB_MCP_OAUTH_SECRET=/),
    ]);
    const keysOn = byId(claudeVariants({ mcpUrl, apiKeyEnabled: true })).connector;
    expect(keysOn.missing[0]).toBe('MDDB_MCP_OAUTH_ENABLED=true');
    expect(keysOn.missing).toHaveLength(3);
  });

  test('both stdio variants say they start a separate local database', () => {
    const v = byId(claudeVariants({ mcpUrl }));
    for (const id of ['stdio-docker', 'stdio-binary']) {
      expect(v[id].note).toBe(STDIO_NOTE);
      expect(v[id].filename).toBe('claude_desktop_config.json');
      expect(v[id].instructions[0]).toBe('Copy this configuration to your Claude Desktop config file:');
    }
    expect(v['stdio-docker'].content).toContain('tradik/mddb:latest');
    expect(JSON.parse(v['stdio-binary'].content).mcpServers.mddb.command).toBe('/path/to/mddbd');
  });
});

describe('shell quoting', () => {
  const hostile = ['a"b', 'a$HOME', 'a`id`b', "it's", 'line\nbreak', 'back\\slash', 'bang!', "'\"$`\\\n"];

  test.each(hostile)('a key %j stays one argument with its exact value', (key) => {
    const command = byId(claudeVariants({ mcpUrl, apiKey: key, apiKeyEnabled: true })).command.content;
    const word = command.slice(command.indexOf('--header ') + '--header '.length);
    expect(shellValue(word)).toBe(`X-API-Key: ${key}`);
    expect(JSON.parse(byId(claudeVariants({ mcpUrl, apiKey: key }))['project-file'].content).mcpServers.mddb.headers['X-API-Key']).toBe(key);
  });

  test.each([...hostile, 'https://h/mcp?a=1&b=2', 'https://h/mcp; rm -rf x', 'https://h/a b'])('a URL %j stays one argument', (url) => {
    const command = byId(claudeVariants({ mcpUrl: url })).command.content;
    const word = command.slice('claude mcp add --transport http mddb '.length);
    expect(shellValue(word)).toBe(url);
    expect(JSON.parse(byId(claudeVariants({ mcpUrl: url }))['project-file'].content).mcpServers.mddb.url).toBe(url);
  });

  test('plain values keep the form the docs show', () => {
    expect(shellWord('https://mcp.example.com:8443/mcp')).toBe('https://mcp.example.com:8443/mcp');
    expect(shellWord('X-API-Key: mcp_x', true)).toBe('"X-API-Key: mcp_x"');
    expect(shellWord("it's")).toBe(String.raw`'it'\''s'`);
  });
});
