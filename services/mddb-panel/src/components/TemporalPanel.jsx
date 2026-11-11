import { useState, useEffect } from 'react';
import { Clock, Flame, BarChart2, ListOrdered } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import TemporalEventsTab from './TemporalEventsTab';

const INTERVALS = [
  { value: 'day', label: 'Daily' },
  { value: 'week', label: 'Weekly' },
  { value: 'month', label: 'Monthly' },
];

const EVENT_TYPES = [
  { value: 'access', label: 'Access' },
  { value: 'create', label: 'Create' },
  { value: 'update', label: 'Update' },
];

function formatTs(ts) {
  if (!ts) return '—';
  return new Date(ts * 1000).toLocaleString();
}

function HotDocsTab({ collection }) {
  const [entries, setEntries] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [topN, setTopN] = useState(20);

  // The tabs are only shown with a collection selected.
  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await mddbClient.temporalHot({ collection, topN });
      setEntries(data.entries || []);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, [collection]);

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3">
        <label className="text-sm text-gray-600">Top</label>
        <select
          value={topN}
          onChange={(e) => setTopN(Number(e.target.value))}
          className="px-2 py-1.5 border border-gray-300 rounded text-sm"
        >
          {[10, 20, 50].map((n) => <option key={n} value={n}>{n}</option>)}
        </select>
        <button
          onClick={load}
          className="px-3 py-1.5 text-sm bg-blue-600 text-white rounded hover:bg-blue-700 transition-colors"
        >
          Refresh
        </button>
      </div>

      {error && <div className="p-3 bg-red-50 border border-red-200 rounded text-sm text-red-700">{error}</div>}

      {loading ? (
        <div className="flex justify-center py-8">
          <div className="animate-spin rounded-full h-6 w-6 border-b-2 border-blue-600"></div>
        </div>
      ) : entries.length === 0 ? (
        <div className="text-sm text-gray-500 text-center py-8">
          No access data yet. Enable <strong>Track Access</strong> in Collection Settings to start tracking.
        </div>
      ) : (
        <div className="space-y-1">
          {entries.map((e, i) => (
            <div key={e.docId} className="flex items-center gap-3 px-3 py-2.5 bg-white border border-gray-100 rounded-lg">
              <span className="text-xs text-gray-400 w-5 text-right">{i + 1}</span>
              <div className="flex-1 min-w-0">
                <div className="text-sm font-medium text-gray-800 truncate">
                  {e.document?.key || e.docId}
                </div>
                {e.document?.lang && (
                  <div className="text-xs text-gray-400">{e.document.lang}</div>
                )}
              </div>
              <div className="flex items-center gap-1 text-amber-600">
                <Flame className="w-3.5 h-3.5" />
                <span className="text-sm font-semibold">{e.accessCount}</span>
              </div>
              <div className="text-xs text-gray-400 w-36 text-right">
                {formatTs(e.lastAccessAt)}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function HistogramTab({ collection }) {
  const [buckets, setBuckets] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [interval, setInterval] = useState('day');
  const [eventType, setEventType] = useState('access');

  // The tabs are only shown with a collection selected.
  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const now = Math.floor(Date.now() / 1000);
      const data = await mddbClient.temporalHistogram({
        collection,
        eventType,
        interval,
        from: now - 90 * 24 * 3600,
        to: now,
      });
      setBuckets(data.buckets || []);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, [collection]);

  const maxCount = buckets.reduce((m, b) => Math.max(m, b.count), 1);

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3 flex-wrap">
        <select
          value={eventType}
          onChange={(e) => setEventType(e.target.value)}
          className="px-2 py-1.5 border border-gray-300 rounded text-sm"
        >
          {EVENT_TYPES.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
        </select>
        <select
          value={interval}
          onChange={(e) => setInterval(e.target.value)}
          className="px-2 py-1.5 border border-gray-300 rounded text-sm"
        >
          {INTERVALS.map((i) => <option key={i.value} value={i.value}>{i.label}</option>)}
        </select>
        <button
          onClick={load}
          className="px-3 py-1.5 text-sm bg-blue-600 text-white rounded hover:bg-blue-700 transition-colors"
        >
          Load
        </button>
      </div>

      {error && <div className="p-3 bg-red-50 border border-red-200 rounded text-sm text-red-700">{error}</div>}

      {loading ? (
        <div className="flex justify-center py-8">
          <div className="animate-spin rounded-full h-6 w-6 border-b-2 border-blue-600"></div>
        </div>
      ) : buckets.length === 0 ? (
        <div className="text-sm text-gray-500 text-center py-8">No activity data for the selected range.</div>
      ) : (
        <div className="space-y-1">
          {buckets.map((b) => (
            <div key={b.label} className="flex items-center gap-3">
              <div className="w-24 text-xs text-gray-500 text-right flex-shrink-0">{b.label}</div>
              <div className="flex-1 bg-gray-100 rounded-full h-5 overflow-hidden">
                <div
                  className="h-full bg-blue-500 rounded-full transition-all"
                  style={{ width: `${(b.count / maxCount) * 100}%` }}
                />
              </div>
              <div className="w-10 text-xs text-gray-700 font-medium text-right flex-shrink-0">{b.count}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export default function TemporalPanel() {
  const { currentCollection } = useStore();
  const [activeTab, setActiveTab] = useState('histogram');

  const tabs = [
    { id: 'histogram', label: 'Activity Histogram', icon: BarChart2 },
    { id: 'hot', label: 'Hot Documents', icon: Flame },
    { id: 'events', label: 'Document Events', icon: ListOrdered },
  ];

  return (
    <div className="h-full flex flex-col overflow-hidden">
      <div className="p-4 border-b border-gray-200">
        <div className="flex items-center gap-2 mb-1">
          <Clock className="w-5 h-5 text-blue-600" />
          <h2 className="text-lg font-semibold text-gray-900">Temporal Analytics</h2>
        </div>
        <p className="text-sm text-gray-500">
          Document lifecycle events and access patterns
          {currentCollection && <span className="ml-1 text-blue-600">· {currentCollection}</span>}
        </p>
      </div>

      {!currentCollection ? (
        <div className="flex-1 flex items-center justify-center text-sm text-gray-500">
          Select a collection to view temporal analytics.
        </div>
      ) : (
        <>
          <div className="flex border-b border-gray-200 px-4">
            {tabs.map(({ id, label, icon: Icon }) => (
              <button
                key={id}
                onClick={() => setActiveTab(id)}
                className={`flex items-center gap-1.5 px-4 py-2.5 text-sm font-medium border-b-2 transition-colors ${
                  activeTab === id
                    ? 'border-blue-600 text-blue-600'
                    : 'border-transparent text-gray-500 hover:text-gray-700'
                }`}
              >
                <Icon className="w-3.5 h-3.5" />
                {label}
              </button>
            ))}
          </div>

          <div className="flex-1 overflow-y-auto p-4">
            {activeTab === 'histogram' && <HistogramTab collection={currentCollection} />}
            {activeTab === 'hot' && <HotDocsTab collection={currentCollection} />}
            {activeTab === 'events' && <TemporalEventsTab collection={currentCollection} />}
          </div>
        </>
      )}
    </div>
  );
}
