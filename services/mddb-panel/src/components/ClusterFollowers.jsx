import { badgeClass, dotClass, formatEntries, formatLag, lagBarClass } from '../lib/cluster-format';

// The followers a leader knows, and the lag history the Cluster panel keeps.

/** The followers table: connected ones and those recently gone. */
export function FollowersTable({ followers }) {
  return (
    <div className="bg-surface rounded-xl border border-line p-5 mb-6">
      <h3 className="text-sm font-semibold text-fg-faint uppercase tracking-wider mb-4">
        Followers ({followers.length})
      </h3>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-line-soft">
              <th className="text-left py-2 px-3 text-xs font-semibold text-fg-faint uppercase">Node ID</th>
              <th className="text-left py-2 px-3 text-xs font-semibold text-fg-faint uppercase">Address</th>
              <th className="text-right py-2 px-3 text-xs font-semibold text-fg-faint uppercase">Confirmed LSN</th>
              <th className="text-right py-2 px-3 text-xs font-semibold text-fg-faint uppercase">Lag</th>
              <th className="text-center py-2 px-3 text-xs font-semibold text-fg-faint uppercase">Status</th>
              <th className="text-right py-2 px-3 text-xs font-semibold text-fg-faint uppercase">Last Seen</th>
            </tr>
          </thead>
          <tbody>
            {followers.map((f) => (
              <tr key={f.follower_id} className="border-b border-line-soft">
                <td className="py-2.5 px-3 font-mono text-fg">{f.follower_id}</td>
                <td className="py-2.5 px-3 text-fg-subtle">{f.address || '-'}</td>
                <td className="py-2.5 px-3 text-right font-mono text-fg">{(f.confirmed_lsn || 0).toLocaleString()}</td>
                <td className="py-2.5 px-3 text-right font-mono text-fg">
                  {formatLag(f.lag_ms, f.lag_exact !== false)}
                  {f.lag_entries > 0 && <div className="text-xs text-fg-faint">{formatEntries(f.lag_entries)}</div>}
                </td>
                <td className="py-2.5 px-3 text-center">
                  <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium ${badgeClass(f.status)}`}>
                    <span className={`w-1.5 h-1.5 rounded-full mr-1 ${dotClass(f.status)}`}></span>
                    {f.status || 'unknown'}
                  </span>
                </td>
                <td className="py-2.5 px-3 text-right text-fg-faint">
                  {f.last_seen_at ? new Date(f.last_seen_at * 1000).toLocaleTimeString() : '-'}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/** Lag over the samples taken while the panel is open. */
export function LagHistory({ points, interval }) {
  const maxLag = Math.max(...points.map((p) => p.lag), 1);
  return (
    <div className="bg-surface rounded-xl border border-line p-5 mb-6">
      <h3 className="text-sm font-semibold text-fg-faint uppercase tracking-wider mb-4">
        Lag History (last {Math.round((points.length * interval) / 1000)}s)
      </h3>
      <div className="flex items-end space-x-0.5 h-24">
        {points.map((point) => (
          <div
            key={point.seq}
            className={`flex-1 ${lagBarClass(point.lag)} rounded-t transition-all duration-300`}
            style={{ height: `${Math.max((point.lag / maxLag) * 100, 2)}%` }}
            title={`${formatLag(point.lag)} at ${new Date(point.time).toLocaleTimeString()}`}
          ></div>
        ))}
      </div>
      <div className="flex justify-between mt-1 text-xs text-fg-faint">
        <span>{new Date(points[0].time).toLocaleTimeString()}</span>
        <span>now</span>
      </div>
    </div>
  );
}
