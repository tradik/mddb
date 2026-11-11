import { Plus } from 'lucide-react';
import { labelClass } from '../lib/chat-scenario';
import { newMcpServer, mcpFeature } from '../lib/chat-mcp';
import ChatMcpServerRow from './ChatMcpServerRow';

// MCP-005: the "External knowledge (MCP)" section of the scenario editor —
// foreign MCP servers whose read-only tools the assistant may call next to
// its collections. mddbd checks every allowed tool against the server when
// the scenario is saved.

export default function ChatMcpServers({ rows, onChange, secrets, scenarioEnabled }) {
  const setRow = (i) => (field, value) => onChange(rows.map((r, j) => (j === i ? { ...r, [field]: value } : r)));
  return (
    <fieldset>
      <legend className={labelClass}>External knowledge (MCP)</legend>
      <p className="mb-2 text-xs text-gray-600">
        Live data or knowledge someone else keeps behind an MCP server. Only read-only tools can be allowed, and mddbd checks
        them against the server on save. A tool reaches the model only while the {mcpFeature} feature, the scenario, the server
        and the tool are all on. For static knowledge a collection is faster and cites by key.
      </p>
      {rows.length > 0 && (
        <ul className="space-y-2">
          {rows.map((row, i) => (
            <ChatMcpServerRow
              key={row.uid}
              index={i}
              row={row}
              set={setRow(i)}
              remove={() => onChange(rows.filter((_, j) => j !== i))}
              secrets={secrets}
              scenarioEnabled={scenarioEnabled}
            />
          ))}
        </ul>
      )}
      {secrets === null && <p className="mt-1 text-xs text-gray-600">The secret list needs admin on &quot;*&quot;; stored references are kept.</p>}
      <button
        type="button"
        onClick={() => onChange([...rows, newMcpServer()])}
        className="mt-2 flex items-center gap-1 rounded border border-gray-300 px-2 py-1.5 text-sm text-gray-800 hover:bg-gray-100"
      >
        <Plus className="h-4 w-4" aria-hidden="true" /> Add an MCP server
      </button>
    </fieldset>
  );
}
