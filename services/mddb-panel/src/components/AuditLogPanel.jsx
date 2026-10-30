import { useEffect, useState, useCallback } from 'react';
import { FileSearch, RefreshCw, AlertCircle, AlertTriangle } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';

// AuditLogPanel — ISO 27001 A.8.15 / SOC 2 CC7.2 audit trail viewer.
// Admin-only; renders an access-denied notice if the backend responds 403.
export default function AuditLogPanel() {
  const { auditFilterPreset, setAuditFilterPreset } = useStore();
  const [events, setEvents] = useState([]);
  const [dropped, setDropped] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [forbidden, setForbidden] = useState(false);
  const [actor, setActor] = useState('');
  const [action, setAction] = useState('');
  const [result, setResult] = useState('');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [limit, setLimit] = useState(200);

  // Apply incoming preset (e.g. from Security dashboard "View all" links).
  useEffect(() => {
    if (!auditFilterPreset) return;
    if (auditFilterPreset.actor !== undefined) setActor(auditFilterPreset.actor);
    if (auditFilterPreset.action !== undefined) setAction(auditFilterPreset.action);
    if (auditFilterPreset.result !== undefined) setResult(auditFilterPreset.result);
    setAuditFilterPreset(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [auditFilterPreset]);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    setForbidden(false);
    try {
      const filters = { actor, action, result, limit };
      if (from) filters.from = new Date(from).toISOString();
      if (to) filters.to = new Date(to).toISOString();
      const data = await mddbClient.listAuditEvents(filters);
      setEvents(data.events || []);
      setDropped(data.dropped || 0);
    } catch (err) {
      if (err.message.includes('403') || err.message.toLowerCase().includes('forbidden')) {
        setForbidden(true);
      } else if (err.message.includes('404')) {
        setError('Audit log is disabled on the server (set MDDB_AUDIT_ENABLED=true).');
      } else {
        setError(err.message);
      }
    } finally {
      setLoading(false);
    }
  }, [actor, action, result, from, to, limit]);

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const formatTs = (ns) => {
    if (!ns) return '';
    try {
      const ms = Math.floor(Number(ns) / 1e6);
      return new Date(ms).toISOString().replace('T', ' ').replace('Z', '');
    } catch {
      return String(ns);
    }
  };

  if (forbidden) {
    return (
      <div className="p-8 text-center text-gray-500">
        <FileSearch className="w-10 h-10 mx-auto text-gray-300 mb-2" />
        <p className="text-sm font-medium text-gray-700">Admin only</p>
        <p className="text-xs mt-1">Audit log access is restricted to administrator accounts.</p>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      <div className="px-4 py-3 border-b border-gray-200 flex items-center justify-between">
        <div>
          <h3 className="text-lg font-semibold text-gray-900 flex items-center gap-2">
            <FileSearch className="w-5 h-5 text-primary-600" />
            Audit Log
            {dropped > 0 && (
              <span
                className="inline-flex items-center gap-1 px-2 py-0.5 bg-amber-100 text-amber-900 rounded text-xs font-semibold"
                title="Events dropped because the audit buffer was full"
              >
                <AlertTriangle className="w-3 h-3" />
                {dropped.toLocaleString()} dropped
              </span>
            )}
          </h3>
          <p className="text-xs text-gray-500 mt-0.5">
            ISO 27001 A.8.15 / SOC 2 CC7.2 — access and administration events.
          </p>
        </div>
        <button
          onClick={load}
          disabled={loading}
          className="flex items-center gap-1 px-3 py-1.5 bg-primary-600 text-white rounded-lg hover:bg-primary-700 disabled:opacity-50 text-sm"
        >
          <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
          Refresh
        </button>
      </div>

      {/* Filter Bar */}
      <div className="px-4 py-3 border-b border-gray-200 bg-gray-50 grid grid-cols-2 md:grid-cols-6 gap-2">
        <input
          type="text"
          value={actor}
          onChange={(e) => setActor(e.target.value)}
          placeholder="actor"
          className="px-2 py-1.5 border border-gray-300 rounded text-sm"
        />
        <input
          type="text"
          value={action}
          onChange={(e) => setAction(e.target.value)}
          placeholder="action"
          className="px-2 py-1.5 border border-gray-300 rounded text-sm"
        />
        <select
          value={result}
          onChange={(e) => setResult(e.target.value)}
          className="px-2 py-1.5 border border-gray-300 rounded text-sm"
        >
          <option value="">all results</option>
          <option value="ok">ok</option>
          <option value="fail">fail</option>
        </select>
        <input
          type="datetime-local"
          value={from}
          onChange={(e) => setFrom(e.target.value)}
          className="px-2 py-1.5 border border-gray-300 rounded text-sm"
        />
        <input
          type="datetime-local"
          value={to}
          onChange={(e) => setTo(e.target.value)}
          className="px-2 py-1.5 border border-gray-300 rounded text-sm"
        />
        <div className="flex items-center gap-2">
          <input
            type="number"
            min="1"
            max="10000"
            value={limit}
            onChange={(e) => setLimit(Number(e.target.value) || 200)}
            className="w-20 px-2 py-1.5 border border-gray-300 rounded text-sm"
            title="limit"
          />
          <button
            onClick={load}
            className="px-3 py-1.5 bg-gray-700 text-white rounded hover:bg-gray-800 text-sm"
          >
            Apply
          </button>
        </div>
      </div>

      {error && (
        <div className="mx-4 mt-3 p-3 bg-red-100 border border-red-200 rounded-lg flex items-start gap-2">
          <AlertCircle className="w-4 h-4 text-red-900 flex-shrink-0 mt-0.5" />
          <p className="text-sm text-red-900">{error}</p>
        </div>
      )}

      <div className="flex-1 overflow-auto p-4">
        {loading ? (
          <p className="text-sm text-gray-500">Loading…</p>
        ) : events.length === 0 ? (
          <p className="text-sm text-gray-400 italic">No audit events match the current filter.</p>
        ) : (
          <table className="w-full text-xs">
            <thead>
              <tr className="text-left text-gray-600 border-b border-gray-200">
                <th className="px-2 py-2 font-semibold">Timestamp</th>
                <th className="px-2 py-2 font-semibold">Actor</th>
                <th className="px-2 py-2 font-semibold">Action</th>
                <th className="px-2 py-2 font-semibold">Result</th>
                <th className="px-2 py-2 font-semibold">IP</th>
                <th className="px-2 py-2 font-semibold">Resource</th>
                <th className="px-2 py-2 font-semibold">User-Agent</th>
                <th className="px-2 py-2 font-semibold">Detail</th>
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <AuditRow key={auditEventKey(e)} event={e} formatTs={formatTs} />
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

// auditEventKey identifies an event across reloads: the server stamps each
// event with a nanosecond timestamp, and the actor and action separate the
// rare events recorded in the same nanosecond. An array index would hand one
// event's row to another whenever a filter changes the list.
function auditEventKey(event) {
  return `${event.ts}|${event.actor || ''}|${event.action || ''}`;
}

function AuditRow({ event, formatTs }) {
  const resultClass = event.result === 'ok'
    ? 'bg-green-100 text-green-900'
    : event.result === 'fail'
      ? 'bg-red-100 text-red-900'
      : 'bg-gray-100 text-gray-800';
  return (
    <tr className="border-b border-gray-100 hover:bg-gray-50">
      <td className="px-2 py-1.5 font-mono whitespace-nowrap text-gray-700">{formatTs(event.ts)}</td>
      <td className="px-2 py-1.5 font-mono">{event.actor || '-'}</td>
      <td className="px-2 py-1.5 font-mono">{event.action || '-'}</td>
      <td className="px-2 py-1.5">
        <span className={`inline-flex px-1.5 py-0.5 rounded text-[10px] font-semibold ${resultClass}`}>
          {(event.result || '-').toUpperCase()}
        </span>
      </td>
      <td className="px-2 py-1.5 font-mono">{event.ip || '-'}</td>
      <td className="px-2 py-1.5 font-mono truncate max-w-[180px]" title={event.resource || ''}>
        {event.resource || '-'}
      </td>
      <td className="px-2 py-1.5 truncate max-w-[160px]" title={event.userAgent || ''}>
        {event.userAgent || '-'}
      </td>
      <td className="px-2 py-1.5 truncate max-w-[220px]" title={event.detail || ''}>
        {event.detail || '-'}
      </td>
    </tr>
  );
}
