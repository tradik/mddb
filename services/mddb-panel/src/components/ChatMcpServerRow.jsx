import { useState } from 'react';
import { PlugZap, X } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { inputClass, labelClass } from '../lib/chat-scenario';
import { isFeatureOff, mcpFeature } from '../lib/chat-mcp';
import ChatMcpToolList from './ChatMcpToolList';

// MCP-005: one external MCP server of a scenario — its name, https URL,
// credential, switches and limits — and "Test connection", which asks mddbd
// to inspect the server and lists its tools to allow.

export default function ChatMcpServerRow({ index, row, set, remove, secrets, scenarioEnabled }) {
  const [inspected, setInspected] = useState(null); // { serverName, serverVersion, tools }
  const [featureOff, setFeatureOff] = useState(false);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);
  const id = `chat-mcp-${index}`;
  const secretNames = (secrets || []).map((s) => s.name);
  if (row.secretRef && !secretNames.includes(row.secretRef)) secretNames.push(row.secretRef);

  const test = async () => {
    setBusy(true);
    setError(null);
    try {
      setInspected(await mddbClient.inspectChatMcpServer({ url: row.url.trim(), secretRef: row.secretRef }));
      setFeatureOff(false);
    } catch (err) {
      setFeatureOff(isFeatureOff(err));
      setError(isFeatureOff(err) ? `External MCP is off: turn on the ${mcpFeature} feature on the Features page.` : err.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <li className="space-y-2 rounded border border-gray-300 p-3">
      <div className="grid gap-2 md:grid-cols-2">
        <div>
          <label htmlFor={`${id}-name`} className={labelClass}>Server name</label>
          <input id={`${id}-name`} value={row.name} onChange={(e) => set('name', e.target.value)} placeholder="wiki" className={inputClass} />
        </div>
        <div>
          <label htmlFor={`${id}-url`} className={labelClass}>URL (https, Streamable HTTP)</label>
          <input id={`${id}-url`} type="url" value={row.url} onChange={(e) => set('url', e.target.value)} placeholder="https://wiki.example.com/mcp" className={inputClass} />
        </div>
        <div>
          <label htmlFor={`${id}-secret`} className={labelClass}>Credential</label>
          <select id={`${id}-secret`} value={row.secretRef} onChange={(e) => set('secretRef', e.target.value)} className={inputClass}>
            <option value="">None</option>
            {secretNames.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
        </div>
        <div className="grid grid-cols-2 gap-2">
          <div>
            <label htmlFor={`${id}-timeout`} className={labelClass}>Timeout, ms</label>
            <input id={`${id}-timeout`} inputMode="numeric" value={row.timeoutMs} onChange={(e) => set('timeoutMs', e.target.value)} placeholder="8000" className={inputClass} />
          </div>
          <div>
            <label htmlFor={`${id}-chars`} className={labelClass}>Result limit, chars</label>
            <input id={`${id}-chars`} inputMode="numeric" value={row.resultMaxChars} onChange={(e) => set('resultMaxChars', e.target.value)} placeholder="4000" className={inputClass} />
          </div>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-2 text-sm text-gray-800">
          <input
            type="checkbox"
            role="switch"
            checked={row.enabled}
            onChange={(e) => set('enabled', e.target.checked)}
            className="h-4 w-4 rounded border-gray-400 text-primary-700 focus:ring-primary-500"
          />
          Server enabled
        </label>
        <button type="button" onClick={test} disabled={busy || !row.url.trim()} className="flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-sm text-gray-800 hover:bg-gray-100 disabled:opacity-50">
          <PlugZap className="h-4 w-4" aria-hidden="true" /> {busy ? 'Testing…' : 'Test connection'}
        </button>
        <button type="button" onClick={remove} aria-label={`Remove server ${row.name || index + 1}`} className="ml-auto rounded p-1 text-gray-700 hover:bg-gray-100">
          <X className="h-4 w-4" aria-hidden="true" />
        </button>
      </div>
      {error && <p role="alert" className="rounded border border-red-200 bg-red-50 p-2 text-sm text-red-800">{error}</p>}
      {inspected && (
        <p className="text-xs text-gray-700">
          Connected to {inspected.serverName || 'the server'} {inspected.serverVersion}: {inspected.tools.length} tools.
        </p>
      )}
      {(inspected || row.allowedTools.length > 0) && (
        <ChatMcpToolList
          id={id}
          tools={inspected?.tools || []}
          tested={Boolean(inspected)}
          allowed={row.allowedTools}
          onChange={(v) => set('allowedTools', v)}
          featureOff={featureOff}
          scenarioEnabled={scenarioEnabled}
          serverEnabled={row.enabled}
        />
      )}
    </li>
  );
}
