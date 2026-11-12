// MCP-005: what the server says about a saved scenario's external MCP
// servers — whether the model sees each one's allowed tools, and the first
// switch that hides them (the chat or chatExternalMcp feature, the scenario,
// the server, a missing secret), and whether it forwards the user's identity
// and so serves signed-in sessions only (#414).

export default function ChatMcpStates({ servers }) {
  if (!servers?.length) return null;
  return (
    <div className="mt-2 rounded border border-gray-200 p-2 text-xs">
      <p className="font-medium text-gray-900">External MCP servers</p>
      <ul className="mt-1 space-y-1">
        {servers.map((s) => (
          <li key={s.name} className="text-gray-800">
            <span className="font-medium">{s.name}</span>{' '}
            {s.forwardIdentity && <span className="text-gray-700">(signed-in users only) </span>}
            {s.active ? (
              <span className="text-green-800">active — tools: {s.tools.map((t) => t.name).join(', ') || 'none allowed'}</span>
            ) : (
              <span className="text-amber-900">inactive: {s.reason}</span>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
