/**
 * Whether the server checks MCP API keys, read from /v1/config
 * (`protocols.mcp.apiKeyEnabled`, reported since 2.19.9).
 *
 * Every page that offers an mcp_ key asks the same question, so the rule
 * lives here once: MCPConfigPanel decides whether a key belongs in the
 * Claude examples, and the key panels refuse to hand out a key the MCP
 * listener would never check (#445).
 */

/**
 * mcpKeyAuthEnabled returns `true` or `false` when the server says, and
 * `undefined` when it does not — an older server, or config not loaded yet.
 * Callers treat `undefined` as before 2.19.9: keys are offered as usual.
 */
export function mcpKeyAuthEnabled(config) {
  const enabled = config?.protocols?.mcp?.apiKeyEnabled;
  return typeof enabled === 'boolean' ? enabled : undefined;
}

/**
 * mcpKeyAuthOff is true only when the server says outright that it does not
 * check MCP keys (MDDB_MCP_API_KEY_ENABLED is not true): a key created then
 * would be refused on the MCP endpoint.
 */
export function mcpKeyAuthOff(config) {
  return mcpKeyAuthEnabled(config) === false;
}
