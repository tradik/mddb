import { useState, useEffect, useCallback, useRef } from 'react';
import { RefreshCw, AlertCircle, CheckCircle2, MinusCircle } from 'lucide-react';
import mddbClient from '../lib/mddb-client';

function relativeTime(ts) {
  const diff = Math.floor(Date.now() / 1000) - ts;
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

const StatusBadge = ({ status }) => {
  const cfg = {
    success: { icon: CheckCircle2, cls: 'bg-success-muted text-success-strong' },
    error: { icon: AlertCircle, cls: 'bg-danger-muted text-danger-strong' },
    skipped: { icon: MinusCircle, cls: 'bg-surface-subtle text-fg-subtle' },
  };
  const c = cfg[status] || cfg.skipped;
  const Icon = c.icon;
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${c.cls}`}>
      <Icon className="w-3 h-3" />
      {status}
    </span>
  );
};

const TypeBadge = ({ type }) => {
  const cfg = {
    trigger: { label: 'Trigger', cls: 'bg-warning-muted text-warning-strong' },
    cron: { label: 'Cron', cls: 'bg-note-muted text-note-strong' },
  };
  const c = cfg[type];
  if (!c) return <span className="text-xs text-fg-faint">{type}</span>;
  return (
    <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${c.cls}`}>
      {c.label}
    </span>
  );
};

// What a run did: a webhook call, a backup (OPS-023: where it went and what
// retention removed) or a synced source (INT-022: the five counts).
const ActionCell = ({ log }) => {
  if (log.action === 'sync') {
    const s = log.sync || {};
    return (
      <span>
        <span className="font-medium text-fg">sync</span>{' '}
        +{s.added ?? 0} added, {s.updated ?? 0} updated, {s.unchanged ?? 0} unchanged, {s.deleted ?? 0} deleted
        {s.failed > 0 && <span className="text-danger">, {s.failed} failed</span>}
      </span>
    );
  }
  if (log.action === 'backup') {
    return (
      <span>
        <span className="font-medium text-fg">backup</span>{' '}
        <span className="font-mono">{log.target || '-'}</span>
        {log.removed?.length > 0 && `, ${log.removed.length} old removed`}
      </span>
    );
  }
  return <span className="font-mono">{log.webhookUrl || '-'}</span>;
};

export default function AutomationLogsTab() {
  const [logs, setLogs] = useState([]);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState(null);
  const [cursor, setCursor] = useState('');
  const [hasMore, setHasMore] = useState(false);
  const [statusFilter, setStatusFilter] = useState('');
  const [autoRefresh, setAutoRefresh] = useState(false);
  const intervalRef = useRef(null);

  const fetchLogs = useCallback(async (append = false, cursorOverride = '') => {
    if (append) {
      setLoadingMore(true);
    } else {
      setLoading(true);
    }
    setError(null);
    try {
      const useCursor = append ? cursorOverride || cursor : '';
      const data = await mddbClient.listAutomationLogs({
        limit: 50,
        cursor: useCursor,
        status: statusFilter,
      });
      const entries = Array.isArray(data.logs) ? data.logs : [];
      if (append) {
        setLogs((prev) => [...prev, ...entries]);
      } else {
        setLogs(entries);
      }
      setCursor(data.nextCursor || '');
      setHasMore(!!data.nextCursor);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
      setLoadingMore(false);
    }
  }, [cursor, statusFilter]);

  // Initial load and when filter changes
  useEffect(() => {
    fetchLogs(false);
  }, [statusFilter]);

  // Auto-refresh
  useEffect(() => {
    if (autoRefresh) {
      intervalRef.current = setInterval(() => {
        fetchLogs(false);
      }, 10000);
    }
    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
    };
  }, [autoRefresh, statusFilter]);

  const handleLoadMore = () => {
    fetchLogs(true, cursor);
  };

  return (
    <div className="flex-1 overflow-y-auto p-6">
      <div className="max-w-7xl mx-auto space-y-4">
        {/* Filter row */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <label className="text-sm text-fg-subtle">Status:</label>
            <select
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value)}
              className="px-3 py-1.5 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus text-sm"
            >
              <option value="">All</option>
              <option value="success">Success</option>
              <option value="error">Error</option>
              <option value="skipped">Skipped</option>
            </select>
          </div>
          <div className="flex items-center gap-3">
            <label className="flex items-center gap-2 text-sm text-fg-subtle cursor-pointer">
              <input
                type="checkbox"
                checked={autoRefresh}
                onChange={(e) => setAutoRefresh(e.target.checked)}
                className="w-4 h-4 text-info rounded border-line-strong focus:ring-focus"
              />
              Auto-refresh
            </label>
            <button
              onClick={() => fetchLogs(false)}
              disabled={loading}
              className="px-3 py-1.5 bg-surface-subtle text-fg-muted rounded-lg hover:bg-surface-strong flex items-center gap-2 text-sm disabled:opacity-50"
            >
              <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
              Refresh
            </button>
          </div>
        </div>

        {/* Error */}
        {error && (
          <div className="bg-danger-soft border border-danger-line rounded-lg p-4 text-danger text-sm">
            {error}
          </div>
        )}

        {/* Table */}
        <div className="bg-surface rounded-lg shadow overflow-x-auto">
          {loading && logs.length === 0 ? (
            <div className="flex items-center justify-center py-12">
              <div className="text-center">
                <div className="animate-spin rounded-full h-10 w-10 border-b-2 border-blue-600 mx-auto mb-3"></div>
                <p className="text-fg-faint text-sm">Loading automation logs...</p>
              </div>
            </div>
          ) : logs.length === 0 ? (
            <div className="p-12 text-center text-fg-faint">
              <p>No automation logs found.</p>
            </div>
          ) : (
            <table className="w-full">
              <thead>
                <tr className="bg-surface-muted border-b border-line">
                  <th className="text-left px-4 py-3 text-xs font-medium text-fg-faint uppercase tracking-wider">Time</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-fg-faint uppercase tracking-wider">Rule Name</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-fg-faint uppercase tracking-wider">Type</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-fg-faint uppercase tracking-wider">Action</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-fg-faint uppercase tracking-wider">Status</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-fg-faint uppercase tracking-wider">HTTP Code</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-fg-faint uppercase tracking-wider">Duration</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-fg-faint uppercase tracking-wider">Attempt</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-fg-faint uppercase tracking-wider">Error</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {logs.map((log, idx) => (
                  <tr key={log.id || idx} className="hover:bg-surface-muted">
                    <td className="px-4 py-3 text-sm text-fg-subtle whitespace-nowrap">
                      {log.timestamp ? relativeTime(log.timestamp) : '-'}
                    </td>
                    <td className="px-4 py-3 text-sm font-medium text-fg whitespace-nowrap">
                      {log.ruleName || '-'}
                    </td>
                    <td className="px-4 py-3 whitespace-nowrap">
                      <TypeBadge type={log.ruleType} />
                    </td>
                    <td className="px-4 py-3 text-xs text-fg-muted max-w-[320px]" title={log.target || log.webhookUrl}>
                      <ActionCell log={log} />
                    </td>
                    <td className="px-4 py-3 whitespace-nowrap">
                      <StatusBadge status={log.status} />
                    </td>
                    <td className="px-4 py-3 text-sm text-fg-subtle whitespace-nowrap">
                      {log.httpStatus || '-'}
                    </td>
                    <td className="px-4 py-3 text-sm text-fg-subtle whitespace-nowrap">
                      {log.durationMs != null ? `${log.durationMs}ms` : '-'}
                    </td>
                    <td className="px-4 py-3 text-sm text-fg-subtle whitespace-nowrap">
                      {log.attempt || '-'}
                    </td>
                    <td className="px-4 py-3 text-sm text-danger max-w-[250px] truncate" title={log.error}>
                      {log.error || ''}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        {/* Load more */}
        {hasMore && (
          <div className="text-center">
            <button
              onClick={handleLoadMore}
              disabled={loadingMore}
              className="px-6 py-2 bg-surface-subtle text-fg-muted rounded-lg hover:bg-surface-strong text-sm disabled:opacity-50 flex items-center gap-2 mx-auto"
            >
              {loadingMore ? (
                <div className="animate-spin rounded-full h-4 w-4 border-2 border-line-strong border-t-transparent"></div>
              ) : null}
              Load more
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
