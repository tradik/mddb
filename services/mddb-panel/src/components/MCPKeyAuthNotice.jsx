import { AlertTriangle } from 'lucide-react';

/**
 * MCPKeyAuthNotice tells an admin that the server does not check MCP API
 * keys, so a key would be refused on the MCP endpoint (#445). Without a
 * `warning` it states the server setting read from /v1/config; with one it
 * shows the `warning` the server returned when a key was created. Announced
 * as a status, in the warning colours.
 */
export default function MCPKeyAuthNotice({ warning }) {
  return (
    <p role="status" className="flex items-start gap-2 rounded-lg border border-warning-line bg-warning-soft p-3 text-sm text-warning-strong">
      <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" aria-hidden="true" />
      <span>
        {warning || (
          <>
            This server does not check MCP API keys, so the MCP endpoint would refuse a key created here.
            Set <code className="rounded bg-surface px-1">MDDB_MCP_API_KEY_ENABLED=true</code> on mddbd and restart it to create one.
          </>
        )}
      </span>
    </p>
  );
}
