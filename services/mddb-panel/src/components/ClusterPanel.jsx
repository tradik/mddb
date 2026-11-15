import { useEffect, useState, useRef } from 'react';
import { Network, RefreshCw, Activity, Database, Clock, Wifi, WifiOff } from 'lucide-react';
import mddbClient from '../lib/mddb-client';

const POLL_INTERVAL = 5000; // 5 seconds

export default function ClusterPanel() {
  const [status, setStatus] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [refreshing, setRefreshing] = useState(false);
  const [lagHistory, setLagHistory] = useState([]);
  const intervalRef = useRef(null);

  useEffect(() => {
    loadStatus();
    intervalRef.current = setInterval(loadStatus, POLL_INTERVAL);
    return () => clearInterval(intervalRef.current);
  }, []);

  const loadStatus = async () => {
    try {
      const data = await mddbClient.getReplicationStatus();
      setStatus(data);
      setError(null);

      // Track lag history (last 60 data points = 5 minutes)
      setLagHistory(prev => {
        const next = [...prev, { time: Date.now(), lag: data.replication_lag_ms || 0, lsn: data.current_lsn || 0 }];
        return next.slice(-60);
      });
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  };

  const handleRefresh = () => {
    setRefreshing(true);
    loadStatus();
  };

  const formatBytes = (bytes) => {
    if (!bytes || bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i];
  };

  const formatUptime = (seconds) => {
    if (!seconds) return '-';
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    if (days > 0) return `${days}d ${hours}h ${minutes}m`;
    if (hours > 0) return `${hours}h ${minutes}m`;
    return `${minutes}m`;
  };

  const formatLag = (ms) => {
    if (!ms || ms === 0) return '0ms';
    if (ms < 1000) return `${ms}ms`;
    if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
    return `${(ms / 60000).toFixed(1)}m`;
  };

  const getStatusBadge = (nodeStatus) => {
    const colors = {
      healthy: 'bg-success-muted text-success-strong',
      warning: 'bg-warning-muted text-warning-strong',
      unhealthy: 'bg-danger-muted text-danger-strong',
    };
    return colors[nodeStatus] || 'bg-surface-subtle text-fg-soft';
  };

  const getRoleBadge = (role) => {
    const colors = {
      leader: 'bg-info-muted text-info-strong',
      follower: 'bg-note-muted text-note-strong',
      standalone: 'bg-surface-subtle text-fg-soft',
    };
    return colors[role] || 'bg-surface-subtle text-fg-soft';
  };

  const getNodeStatus = () => {
    if (!status) return 'unknown';
    if (status.role === 'standalone') return 'healthy';
    if (status.role === 'leader') return 'healthy';
    // Follower: check lag
    if (status.replication_lag_ms > 30000) return 'unhealthy';
    if (status.replication_lag_ms > 1000) return 'warning';
    return 'healthy';
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-600"></div>
      </div>
    );
  }

  if (error && !status) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <Network className="w-12 h-12 text-line-strong mx-auto mb-4" />
          <div className="text-danger font-medium mb-2">Failed to load cluster status</div>
          <div className="text-sm text-fg-faint mb-4">{error}</div>
          <button
            onClick={handleRefresh}
            className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700"
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  const nodeStatus = getNodeStatus();

  return (
    <div className="p-6 overflow-y-auto h-full">
      {/* Header */}
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

      {/* This Node Overview */}
      <div className="bg-surface rounded-xl border border-line p-5 mb-6">
        <h3 className="text-sm font-semibold text-fg-faint uppercase tracking-wider mb-4">This Node</h3>
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          <div>
            <div className="text-xs text-fg-faint mb-1">Role</div>
            <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${getRoleBadge(status?.role)}`}>
              {status?.role || 'unknown'}
            </span>
          </div>
          <div>
            <div className="text-xs text-fg-faint mb-1">Status</div>
            <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${getStatusBadge(nodeStatus)}`}>
              <span className={`w-1.5 h-1.5 rounded-full mr-1.5 ${
                nodeStatus === 'healthy' ? 'bg-green-500' :
                nodeStatus === 'warning' ? 'bg-yellow-500' : 'bg-red-500'
              }`}></span>
              {nodeStatus}
            </span>
          </div>
          <div>
            <div className="text-xs text-fg-faint mb-1">Node ID</div>
            <div className="text-sm font-mono text-fg">{status?.node_id || '-'}</div>
          </div>
          <div>
            <div className="text-xs text-fg-faint mb-1">Uptime</div>
            <div className="text-sm text-fg">{formatUptime(status?.uptime_seconds)}</div>
          </div>
        </div>
      </div>

      {/* LSN & Binlog Info */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-6">
        <div className="bg-surface rounded-xl border border-line p-5">
          <div className="flex items-center space-x-2 mb-2">
            <Activity className="w-4 h-4 text-info" />
            <div className="text-xs text-fg-faint uppercase font-semibold">Current LSN</div>
          </div>
          <div className="text-2xl font-bold text-fg font-mono">
            {(status?.current_lsn || 0).toLocaleString()}
          </div>
        </div>
        <div className="bg-surface rounded-xl border border-line p-5">
          <div className="flex items-center space-x-2 mb-2">
            <Database className="w-4 h-4 text-note" />
            <div className="text-xs text-fg-faint uppercase font-semibold">Binlog Size</div>
          </div>
          <div className="text-2xl font-bold text-fg">
            {formatBytes(status?.binlog_size_bytes)}
          </div>
          {status?.binlog_oldest_lsn > 0 && (
            <div className="text-xs text-fg-faint mt-1">Oldest LSN: {status.binlog_oldest_lsn.toLocaleString()}</div>
          )}
        </div>
        <div className="bg-surface rounded-xl border border-line p-5">
          <div className="flex items-center space-x-2 mb-2">
            <Clock className="w-4 h-4 text-success" />
            <div className="text-xs text-fg-faint uppercase font-semibold">Replication Lag</div>
          </div>
          <div className="text-2xl font-bold text-fg">
            {formatLag(status?.replication_lag_ms)}
          </div>
        </div>
      </div>

      {/* Followers Table (Leader only) */}
      {status?.followers && status.followers.length > 0 && (
        <div className="bg-surface rounded-xl border border-line p-5 mb-6">
          <h3 className="text-sm font-semibold text-fg-faint uppercase tracking-wider mb-4">
            Connected Followers ({status.followers.length})
          </h3>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-line-soft">
                  <th className="text-left py-2 px-3 text-xs font-semibold text-fg-faint uppercase">Node ID</th>
                  <th className="text-left py-2 px-3 text-xs font-semibold text-fg-faint uppercase">Address</th>
                  <th className="text-right py-2 px-3 text-xs font-semibold text-fg-faint uppercase">LSN</th>
                  <th className="text-right py-2 px-3 text-xs font-semibold text-fg-faint uppercase">Lag</th>
                  <th className="text-center py-2 px-3 text-xs font-semibold text-fg-faint uppercase">Status</th>
                  <th className="text-right py-2 px-3 text-xs font-semibold text-fg-faint uppercase">Last Seen</th>
                </tr>
              </thead>
              <tbody>
                {status.followers.map((f) => (
                  <tr key={f.follower_id} className="border-b border-line-soft hover:bg-surface-muted">
                    <td className="py-2.5 px-3 font-mono text-fg">{f.follower_id}</td>
                    <td className="py-2.5 px-3 text-fg-subtle">{f.address || '-'}</td>
                    <td className="py-2.5 px-3 text-right font-mono text-fg">{(f.confirmed_lsn || 0).toLocaleString()}</td>
                    <td className="py-2.5 px-3 text-right font-mono text-fg">{formatLag(f.lag_ms)}</td>
                    <td className="py-2.5 px-3 text-center">
                      <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium ${getStatusBadge(f.status)}`}>
                        <span className={`w-1.5 h-1.5 rounded-full mr-1 ${
                          f.status === 'healthy' ? 'bg-green-500' :
                          f.status === 'warning' ? 'bg-yellow-500' : 'bg-red-500'
                        }`}></span>
                        {f.status}
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
      )}

      {/* Lag History Chart (simple ASCII-style bar chart) */}
      {lagHistory.length > 1 && (
        <div className="bg-surface rounded-xl border border-line p-5 mb-6">
          <h3 className="text-sm font-semibold text-fg-faint uppercase tracking-wider mb-4">
            Lag History (last {Math.round(lagHistory.length * POLL_INTERVAL / 1000)}s)
          </h3>
          <div className="flex items-end space-x-0.5 h-24">
            {lagHistory.map((point, idx) => {
              const maxLag = Math.max(...lagHistory.map(p => p.lag), 1);
              const height = Math.max((point.lag / maxLag) * 100, 2);
              const color = point.lag > 30000 ? 'bg-red-400' : point.lag > 1000 ? 'bg-yellow-400' : 'bg-green-400';
              return (
                <div
                  key={idx}
                  className={`flex-1 ${color} rounded-t transition-all duration-300`}
                  style={{ height: `${height}%` }}
                  title={`${formatLag(point.lag)} at ${new Date(point.time).toLocaleTimeString()}`}
                ></div>
              );
            })}
          </div>
          <div className="flex justify-between mt-1 text-xs text-fg-faint">
            <span>{lagHistory.length > 0 ? new Date(lagHistory[0].time).toLocaleTimeString() : ''}</span>
            <span>now</span>
          </div>
        </div>
      )}

      {/* Standalone Info */}
      {status?.role === 'standalone' && (
        <div className="bg-surface-muted rounded-xl border border-line p-5 text-center">
          <WifiOff className="w-8 h-8 text-fg-faint mx-auto mb-3" />
          <div className="text-sm font-medium text-fg-subtle mb-1">Standalone Mode</div>
          <div className="text-xs text-fg-faint">
            Replication is not configured. Set <code className="bg-surface-strong px-1 rounded">MDDB_REPLICATION_ROLE</code> to enable clustering.
          </div>
        </div>
      )}

      {/* Auto-refresh indicator */}
      <div className="text-center text-xs text-fg-faint mt-4">
        <Wifi className="w-3 h-3 inline mr-1" />
        Auto-refreshing every {POLL_INTERVAL / 1000}s
      </div>
    </div>
  );
}
