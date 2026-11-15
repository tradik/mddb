// MCP-005: what the server says about a saved scenario's external MCP
// servers — whether the model sees each one's allowed tools, and the first
// switch that hides them (the chat or chatExternalMcp feature, the scenario,
// the server, a missing secret), and whether it forwards the user's identity
// and so serves signed-in sessions only (#414).

export default function ChatMcpStates({ servers }) {
  if (!servers?.length) return null;
  return (
    <div className="mt-2 rounded border border-line p-2 text-xs">
      <p className="font-medium text-fg">External MCP servers</p>
      <ul className="mt-1 space-y-1">
        {servers.map((s) => (
          <li key={s.name} className="text-fg-soft">
            <span className="font-medium">{s.name}</span>{' '}
            {s.forwardIdentity && <span className="text-fg-muted">(signed-in users only) </span>}
            {s.active ? (
              <span className="text-success-strong">active — tools: {s.tools.map((t) => t.name).join(', ') || 'none allowed'}</span>
            ) : (
              <span className="text-warning-strong">inactive: {s.reason}</span>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
