import { useCallback, useEffect, useRef, useState } from 'react';
import { Network, RefreshCw, Activity, Database, Clock, Wifi, WifiOff } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import {
  badgeClass, binlogUse, dotClass, formatBytes, formatEntries, formatLag, formatUptime, nodeHealth, nodeLag,
} from '../lib/cluster-format';
import { FollowersTable, LagHistory } from './ClusterFollowers';
import ClusterElection from './ClusterElection';
import ClusterPlacement from './ClusterPlacement';

const POLL_INTERVAL = 5000; // 5 seconds

// The Cluster tab: this node's replication state as GET /v1/replication/status
// reports it (GO-050) — role, health and why not, position, binlog, lag, and
// on a leader the followers it knows. In a cluster (GO-051) it adds the
// leader election: term, leader, nodes, and promote / step down. With
// placement groups (GO-052) it shows which group holds each collection.
export default function ClusterPanel() {
  const [status, setStatus] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [refreshing, setRefreshing] = useState(false);
  const [lagHistory, setLagHistory] = useState([]);
  const seq = useRef(0);

  const loadStatus = useCallback(async () => {
    try {
      const data = await mddbClient.getReplicationStatus();
      setStatus(data);
      setError(null);
      const lag = nodeLag(data);
      if (lag) {
        seq.current += 1;
        const point = { seq: seq.current, time: Date.now(), lag: lag.ms };
        setLagHistory((prev) => [...prev, point].slice(-60)); // 5 minutes
      }
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    const tick = () => void loadStatus();
    const first = setTimeout(tick, 0);
    const id = setInterval(tick, POLL_INTERVAL);
    return () => {
      clearTimeout(first);
      clearInterval(id);
    };
  }, [loadStatus]);

  const handleRefresh = () => {
    setRefreshing(true);
    void loadStatus();
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-accent"></div>
      </div>
    );
  }

  // A failed first load, or a status call that answered nothing.
  if (!status) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <Network className="w-12 h-12 text-line-strong mx-auto mb-4" />
          <div className="text-danger font-medium mb-2">Failed to load cluster status</div>
          <div className="text-sm text-fg-faint mb-4">{error}</div>
          <button onClick={handleRefresh} className="px-4 py-2 bg-primary-600 text-white rounded hover:bg-primary-700">
            Retry
          </button>
        </div>
      </div>
    );
  }

  const health = nodeHealth(status);
  const lag = nodeLag(status);
  const use = binlogUse(status);
  const followers = status.followers || [];

  return (
    <div className="p-6 overflow-y-auto h-full">
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center space-x-3">
          <Network className="w-6 h-6 text-info" />
          <h2 className="text-xl font-bold text-fg">Cluster</h2>
        </div>
        <button
          onClick={handleRefresh}
          disabled={refreshing}
          className="flex items-center space-x-2 px-3 py-2 text-sm bg-surface-subtle hover:bg-surface-strong rounded-lg transition-colors"
        >
          <RefreshCw className={`w-4 h-4 ${refreshing ? 'animate-spin' : ''}`} />
          <span>Refresh</span>
        </button>
      </div>

      <div className="bg-surface rounded-xl border border-line p-5 mb-6">
        <h3 className="text-sm font-semibold text-fg-faint uppercase tracking-wider mb-4">This Node</h3>
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          <div>
            <div className="text-xs text-fg-faint mb-1">Role</div>
            <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${badgeClass(status.role)}`}>
              {status.role || 'unknown'}
            </span>
          </div>
          <div>
            <div className="text-xs text-fg-faint mb-1">Status</div>
            <output className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${badgeClass(health)}`}>
              <span className={`w-1.5 h-1.5 rounded-full mr-1.5 ${dotClass(health)}`}></span>
              {health}
            </output>
          </div>
          <div>
            <div className="text-xs text-fg-faint mb-1">Node ID</div>
            <div className="text-sm font-mono text-fg">{status.node_id || '-'}</div>
          </div>
          <div>
            <div className="text-xs text-fg-faint mb-1">Uptime</div>
            <div className="text-sm text-fg">{formatUptime(status.uptime_seconds)}</div>
          </div>
        </div>
        {status.last_error && (
          <p role="alert" className="mt-4 text-sm text-danger bg-danger-soft border border-danger-line rounded p-2">
            {status.last_error}
          </p>
        )}
        {status.role === 'follower' && (
          <div className="mt-4 text-sm text-fg-subtle">
            Leader <span className="font-mono text-fg">{status.leader_addr || '-'}</span>
            {' · '}
            {status.connected ? 'stream open' : 'not connected'}
            {status.last_applied_at > 0 && ` · last applied ${new Date(status.last_applied_at * 1000).toLocaleTimeString()}`}
          </div>
        )}
      </div>

      {status.cluster && <ClusterElection cluster={status.cluster} term={status.term} onChanged={loadStatus} />}
      <ClusterPlacement />

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-6">
        <div className="bg-surface rounded-xl border border-line p-5">
          <div className="flex items-center space-x-2 mb-2">
            <Activity className="w-4 h-4 text-info" />
            <div className="text-xs text-fg-faint uppercase font-semibold">{status.role === 'follower' ? 'Applied LSN' : 'Current LSN'}</div>
          </div>
          <div className="text-2xl font-bold text-fg font-mono">{(status.current_lsn || 0).toLocaleString()}</div>
          {status.log_id && <div className="text-xs text-fg-faint mt-1 font-mono">Log {status.log_id}</div>}
        </div>
        <div className="bg-surface rounded-xl border border-line p-5">
          <div className="flex items-center space-x-2 mb-2">
            <Database className="w-4 h-4 text-note" />
            <div className="text-xs text-fg-faint uppercase font-semibold">Binlog</div>
          </div>
          <div className="text-2xl font-bold text-fg">{formatBytes(status.binlog_size_bytes)}</div>
          {use !== null && (
            <div className="text-xs text-fg-faint mt-1">
              {use}% of {formatBytes(status.binlog_max_size_bytes)} · sync {status.binlog_sync || 'interval'}
            </div>
          )}
          {status.binlog_oldest_lsn > 0 && (
            <div className="text-xs text-fg-faint mt-1">Oldest LSN: {status.binlog_oldest_lsn.toLocaleString()}</div>
          )}
        </div>
        <div className="bg-surface rounded-xl border border-line p-5">
          <div className="flex items-center space-x-2 mb-2">
            <Clock className="w-4 h-4 text-success" />
            <div className="text-xs text-fg-faint uppercase font-semibold">
              {status.role === 'leader' ? 'Furthest Follower Lag' : 'Replication Lag'}
            </div>
          </div>
          <div className="text-2xl font-bold text-fg">{lag ? formatLag(lag.ms, lag.exact) : '-'}</div>
          {lag && lag.entries > 0 && <div className="text-xs text-fg-faint mt-1">{formatEntries(lag.entries)}</div>}
        </div>
      </div>

      {followers.length > 0 && <FollowersTable followers={followers} />}
      {lagHistory.length > 1 && <LagHistory points={lagHistory} interval={POLL_INTERVAL} />}

      {status.role === 'standalone' && (
        <div className="bg-surface-muted rounded-xl border border-line p-5 text-center">
          <WifiOff className="w-8 h-8 text-fg-faint mx-auto mb-3" />
          <div className="text-sm font-medium text-fg-subtle mb-1">Standalone Mode</div>
          <div className="text-xs text-fg-faint">
            Replication is not configured. Set <code className="bg-surface-strong px-1 rounded">MDDB_REPLICATION_ROLE</code> to enable clustering.
          </div>
        </div>
      )}

      <div className="text-center text-xs text-fg-faint mt-4">
        <Wifi className="w-3 h-3 inline mr-1" />
        Auto-refreshing every {POLL_INTERVAL / 1000}s
      </div>
    </div>
  );
}
