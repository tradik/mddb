/**
 * Leader election actions of a cluster node (GO-051). Both need admin and
 * answer `{ node_id, state, term }`; a refusal is a 409 (another node has a
 * later log, the election was lost, this node is not the leader) and a node
 * outside a cluster answers 400 — the client's error carries the server's words.
 */
import { postJson } from './http';

export const replicationMethods = {
  /**
   * Asks this node to stand for leader. `force` skips the log check and is
   * accepted only when the cluster runs without automatic failover.
   */
  promoteNode({ force = false } = {}) {
    return postJson(this, '/replication/promote', { force });
  },

  /** Asks the leader to step down and follow. */
  demoteNode() {
    return this.request('/replication/demote', { method: 'POST' });
  },
};
