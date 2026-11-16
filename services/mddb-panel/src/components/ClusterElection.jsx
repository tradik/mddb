import { useCallback, useId, useState } from 'react';
import { Crown } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { badgeClass } from '../lib/cluster-format';
import ClusterNodes from './ClusterNodes';
import ClusterActionDialog from './ClusterActionDialog';

/** One labelled figure of the election card. */
function Fact({ label, children }) {
  return (
    <div>
      <div className="text-xs text-fg-faint mb-1">{label}</div>
      <div className="text-sm text-fg">{children}</div>
    </div>
  );
}

/** The leader as id and address, or that none is known. */
function leaderText(cluster) {
  if (!cluster.leader_id) return 'none known';
  return cluster.leader_addr ? `${cluster.leader_id} (${cluster.leader_addr})` : cluster.leader_id;
}

/**
 * ClusterElection is the leader election part of the Cluster tab (GO-051),
 * shown when the node runs in a cluster (MDDB_CLUSTER_PEERS): the term, this
 * node's state, the leader, its lease and whether writes are taken, failover
 * and acknowledgement settings, the nodes, and the file of writes this node
 * set aside when it rejoined. An admin can promote a node that is not the
 * leader or step the leader down; `onChanged` reloads the status after either.
 */
export default function ClusterElection({ cluster, term, onChanged }) {
  const id = useId();
  const [action, setAction] = useState(null); // 'promote' | 'demote' | null
  const [force, setForce] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const [result, setResult] = useState(null);
  const isLeader = cluster.state === 'leader';
  const nodes = cluster.nodes || [];

  const open = (next) => {
    setAction(next);
    setForce(false);
    setError(null);
    setResult(null);
  };
  const close = useCallback(() => setAction(null), []);

  const confirm = async () => {
    setBusy(true);
    setError(null);
    try {
      const answer = action === 'promote'
        ? await mddbClient.promoteNode({ force })
        : await mddbClient.demoteNode();
      setResult(answer);
      setAction(null);
      void onChanged();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section aria-labelledby={`${id}-title`} className="bg-surface rounded-xl border border-line p-5 mb-6">
      <div className="flex items-center justify-between gap-4 mb-4">
        <h3 id={`${id}-title`} className="flex items-center gap-2 text-sm font-semibold text-fg-faint uppercase tracking-wider">
          <Crown className="w-4 h-4 text-info" aria-hidden="true" />
          Leader Election
        </h3>
        {isLeader ? (
          <button type="button" onClick={() => open('demote')} className="px-3 py-1.5 text-sm text-white bg-primary-700 rounded-lg hover:bg-primary-800 focus-visible:bg-primary-800">
            Step down
          </button>
        ) : (
          <button type="button" onClick={() => open('promote')} className="px-3 py-1.5 text-sm text-white bg-primary-700 rounded-lg hover:bg-primary-800 focus-visible:bg-primary-800">
            Promote this node
          </button>
        )}
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
        <Fact label="Term"><span className="font-mono">{cluster.term ?? term ?? '-'}</span></Fact>
        <Fact label="This node">
          <span className="font-mono mr-2">{cluster.node_id}</span>
          <span className={`inline-flex px-2 py-0.5 rounded-full text-xs font-medium ${badgeClass(cluster.state)}`}>{cluster.state || 'unknown'}</span>
        </Fact>
        <Fact label="Leader"><span className="font-mono break-all">{leaderText(cluster)}</span></Fact>
        <Fact label="Voted for"><span className="font-mono">{cluster.voted_for || '-'}</span></Fact>
        <Fact label="Lease">{cluster.lease_valid ? 'valid' : 'not held'}</Fact>
        <Fact label="Takes writes">{cluster.writable ? 'yes' : 'no'}</Fact>
        <Fact label="Automatic failover">{cluster.auto_failover ? 'on' : 'off'}</Fact>
        <Fact label="Acknowledgement">
          {cluster.ack_mode || 'async'}
          <span className="text-fg-faint"> · majority {cluster.majority ?? '-'}</span>
        </Fact>
      </div>

      {result && (
        <output className="block mb-4 text-sm text-success-strong bg-success-soft border border-success-line rounded p-2">
          {result.node_id} is now {result.state} in term {result.term}.
        </output>
      )}

      {cluster.last_rejected_file && (
        <div className="mb-4 text-sm text-warning-strong bg-warning-soft border border-warning-line rounded p-3">
          <div className="font-medium">Rejected writes</div>
          <div className="font-mono break-all">{cluster.last_rejected_file}</div>
          <p className="mt-1">
            Writes this node took that the new leader never had were cut when it rejoined; they are kept in this file for manual review.
          </p>
        </div>
      )}

      {nodes.length > 0 && <ClusterNodes nodes={nodes} />}

      {action === 'promote' && (
        <ClusterActionDialog title="Promote this node" confirmLabel="Promote" busy={busy} error={error} onConfirm={confirm} onCancel={close}>
          <p>
            Node <span className="font-mono">{cluster.node_id}</span> stands for leader in a new term. The other nodes refuse it when one of them has a later log or this node has not caught up.
          </p>
          {!cluster.auto_failover && (
            <fieldset className="border border-danger-line bg-danger-soft rounded p-3 space-y-2">
              <legend className="sr-only">Force</legend>
              <div className="flex items-center gap-2">
                <input id={`${id}-force`} type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} />
                <label htmlFor={`${id}-force`} className="font-medium text-danger-strong">Force, skipping the log check</label>
              </div>
              <p className="text-danger-strong">
                Writes another node has and this one lacks are cut from that node when it rejoins and saved to its rejected-writes file. Force only when the other nodes are lost for good.
              </p>
            </fieldset>
          )}
        </ClusterActionDialog>
      )}
      {action === 'demote' && (
        <ClusterActionDialog title="Step down" confirmLabel="Step down" busy={busy} error={error} onConfirm={confirm} onCancel={close}>
          <p>
            Node <span className="font-mono">{cluster.node_id}</span> stops taking writes and follows. Writes fail until a leader is elected again.
          </p>
        </ClusterActionDialog>
      )}
    </section>
  );
}
