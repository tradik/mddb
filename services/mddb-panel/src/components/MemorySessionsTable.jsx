import { formatUnix } from '../lib/time-format';

/**
 * MemorySessionsTable lists agent memory sessions (/v1/memory/sessions):
 * who, which scenario, how many messages, when last active and when the
 * session expires. Opening a row shows its history.
 */
export default function MemorySessionsTable({ sessions, total, selectedId, onSelect }) {
  if (sessions.length === 0) {
    return <p className="text-sm text-fg-muted">No sessions match. Agents create them through the API or MCP; you can start one below.</p>;
  }
  return (
    <div className="overflow-x-auto">
      <table className="min-w-full text-sm">
        <caption className="text-left text-xs text-fg-muted mb-1">
          {sessions.length} of {total} sessions, most recently active first
        </caption>
        <thead>
          <tr className="text-left text-fg-soft">
            <th scope="col" className="px-3 py-2">Session</th>
            <th scope="col" className="px-3 py-2">User</th>
            <th scope="col" className="px-3 py-2">Scenario</th>
            <th scope="col" className="px-3 py-2">Messages</th>
            <th scope="col" className="px-3 py-2">Last active</th>
            <th scope="col" className="px-3 py-2">Expires</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line-soft">
          {sessions.map((s) => (
            <tr key={s.sessionId} className={s.sessionId === selectedId ? 'bg-info-soft' : undefined}>
              <td className="px-3 py-2">
                <button
                  type="button"
                  onClick={() => onSelect(s)}
                  aria-current={s.sessionId === selectedId ? 'true' : undefined}
                  className="text-info-strong underline font-mono text-xs"
                >
                  {s.title || s.sessionId}
                </button>
              </td>
              <td className="px-3 py-2 text-fg">{s.userId}</td>
              <td className="px-3 py-2 text-fg">{s.scenario || '—'}</td>
              <td className="px-3 py-2 text-fg">{s.messageCount ?? 0}</td>
              <td className="px-3 py-2 text-fg-soft">{formatUnix(s.updatedAt || s.createdAt)}</td>
              <td className="px-3 py-2 text-fg-soft">{formatUnix(s.expiresAt)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
