import { badgeClass } from '../lib/cluster-format';

// The members of a cluster as this node sees them (GO-051): each one's
// election state and term, how far its log reaches, and when it last answered.

const th = 'py-2 px-3 text-xs font-semibold text-fg-faint uppercase';

/** A node's last answer as a local time, "-" when it never answered. */
function lastSeen(value) {
  if (!value) return '-';
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? '-' : at.toLocaleTimeString();
}

/** The nodes table of the Cluster tab. */
export default function ClusterNodes({ nodes }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <caption className="sr-only">Cluster nodes</caption>
        <thead>
          <tr className="border-b border-line-soft">
            <th scope="col" className={`text-left ${th}`}>Node</th>
            <th scope="col" className={`text-left ${th}`}>Address</th>
            <th scope="col" className={`text-center ${th}`}>State</th>
            <th scope="col" className={`text-right ${th}`}>Term</th>
            <th scope="col" className={`text-right ${th}`}>Last LSN / Term</th>
            <th scope="col" className={`text-center ${th}`}>Reachable</th>
            <th scope="col" className={`text-right ${th}`}>Last Seen</th>
          </tr>
        </thead>
        <tbody>
          {nodes.map((n) => (
            <tr key={n.id} className="border-b border-line-soft">
              <td className="py-2.5 px-3 font-mono text-fg">
                {n.id}
                {n.self && <span className="ml-2 text-xs font-sans text-fg-faint">(this node)</span>}
              </td>
              <td className="py-2.5 px-3 text-fg-subtle">
                <div>{n.http_addr || '-'}</div>
                {n.grpc_addr && <div className="text-xs text-fg-faint font-mono">gRPC {n.grpc_addr}</div>}
              </td>
              <td className="py-2.5 px-3 text-center">
                <span className={`inline-flex px-2 py-0.5 rounded-full text-xs font-medium ${badgeClass(n.state)}`}>
                  {n.state || 'unknown'}
                </span>
              </td>
              <td className="py-2.5 px-3 text-right font-mono text-fg">{n.term ?? '-'}</td>
              <td className="py-2.5 px-3 text-right font-mono text-fg">
                {(n.last_lsn || 0).toLocaleString()} / {n.last_term ?? '-'}
              </td>
              <td className="py-2.5 px-3 text-center">
                <span className={n.reachable ? 'text-success-strong' : 'text-danger-strong'}>
                  {n.reachable ? 'yes' : 'no'}
                </span>
              </td>
              <td className="py-2.5 px-3 text-right text-fg-faint">{lastSeen(n.last_seen)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
