/**
 * External knowledge over MCP (MCP-005): the editor rows of a scenario's
 * `mcpServers`, and why a tool is not shown to the model.
 *
 * Numbers are held as the strings the inputs produce; empty means the
 * server's default (timeoutMs 8000, resultMaxChars 4000).
 */

/** The GO-049 feature that lets mddbd talk to foreign MCP servers. */
export const mcpFeature = 'chatExternalMcp';

// uid keys a row in the editor while its name is typed; it is never sent.
let lastUid = 0;

/** A new, enabled server row. */
export function newMcpServer() {
  lastUid += 1;
  return { uid: lastUid, name: '', url: '', enabled: true, secretRef: '', allowedTools: [], timeoutMs: '', resultMaxChars: '' };
}

/** The editor rows of a scenario's servers. */
export function toMcpRows(servers = []) {
  return servers.map((s) => ({
    uid: newMcpServer().uid,
    name: s.name || '',
    url: s.url || '',
    enabled: Boolean(s.enabled),
    secretRef: s.secretRef || '',
    allowedTools: [...(s.allowedTools || [])],
    timeoutMs: s.timeoutMs ? String(s.timeoutMs) : '',
    resultMaxChars: s.resultMaxChars ? String(s.resultMaxChars) : '',
  }));
}

/** The servers a list of rows stands for, ready to PUT. */
export function fromMcpRows(rows) {
  return rows.map((r) => ({
    name: r.name.trim(),
    url: r.url.trim(),
    enabled: r.enabled,
    secretRef: r.secretRef,
    allowedTools: r.allowedTools,
    timeoutMs: Number.parseInt(r.timeoutMs, 10) || 0,
    resultMaxChars: Number.parseInt(r.resultMaxChars, 10) || 0,
  }));
}

/** Why the rows cannot be saved yet, or null when they can. */
export function mcpServerProblem(rows) {
  const seen = new Set();
  for (const r of rows) {
    const name = r.name.trim();
    if (!name || /[|/\s]/.test(name)) return 'Each MCP server needs a name without spaces, "/" or "|".';
    if (seen.has(name)) return `The MCP server name "${name}" is used twice.`;
    seen.add(name);
    if (!/^https:\/\/[^\s/@]+(\/\S*)?$/.test(r.url.trim())) return `The MCP server "${name}" needs an https:// URL without a user or password.`;
  }
  return null;
}

/** A tool's hints as the server gave them; an absent hint is not false. */
export function toolHints(tool) {
  const a = tool.annotations || {};
  return { readOnly: a.readOnlyHint === true, destructive: a.destructiveHint === true };
}

/**
 * Why the model does not see a tool, from the top switch down, or null when
 * it does: the feature, the scenario, the server, the allowed list.
 */
export function toolInactiveReason({ featureOff, scenarioEnabled, serverEnabled, allowed }) {
  if (featureOff) return `Feature ${mcpFeature} is off`;
  if (!scenarioEnabled) return 'Scenario is disabled';
  if (!serverEnabled) return 'Server is disabled';
  if (!allowed) return 'Not allowed';
  return null;
}

/** Whether an inspect failed because the feature is off. */
export function isFeatureOff(err) {
  return err?.status === 404 && err.body?.feature === mcpFeature;
}
