import { useEffect, useState } from 'react';
import { Shield, CheckCircle2, AlertTriangle, RefreshCw, ExternalLink, FileSearch, Send } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';

// SecurityDashboard — one-pane overview of ISO 27001 / SOC 2 posture:
// production guards, recent auth failures, rate-limit signals, and
// recent incident events.
export default function SecurityDashboard() {
  const { setViewMode, setAuditFilterPreset } = useStore();
  const [compliance, setCompliance] = useState(null);
  const [complianceErr, setComplianceErr] = useState(null);
  const [authFailures, setAuthFailures] = useState([]);
  const [incidents, setIncidents] = useState([]);
  const [exporters, setExporters] = useState([]);
  const [loading, setLoading] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      try {
        const data = await mddbClient.getComplianceStatus();
        setCompliance(data);
        setComplianceErr(null);
      } catch (err) {
        setComplianceErr(err.message);
      }
      try {
        const since = new Date(Date.now() - 60 * 60 * 1000).toISOString();
        const data = await mddbClient.listAuditEvents({ result: 'fail', from: since, limit: 200 });
        setAuthFailures(aggregateFailures(data.events || []));
      } catch {
        setAuthFailures([]);
      }
      try {
        const data = await mddbClient.listAuditEvents({ limit: 50 });
        const evs = (data.events || []).filter((e) =>
          (e.action || '').startsWith('security.') || (e.action || '').startsWith('ops.')
        );
        setIncidents(evs.slice(0, 20));
      } catch {
        setIncidents([]);
      }
      try {
        const data = await mddbClient.listAuditExporters();
        setExporters(data.exporters || []);
      } catch {
        setExporters([]);
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, []);

  const openAuditFor = (preset) => {
    setAuditFilterPreset(preset);
    setViewMode('auditLog');
  };

  return (
    <div className="flex flex-col h-full overflow-y-auto">
      <div className="px-4 py-3 border-b border-line flex items-center justify-between">
        <div>
          <h3 className="text-lg font-semibold text-fg flex items-center gap-2">
            <Shield className="w-5 h-5 text-accent" />
            Security Dashboard
          </h3>
          <p className="text-xs text-fg-faint mt-0.5">
            ISO 27001 / SOC 2 compliance posture and recent incidents.
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

      <div className="p-4 space-y-4">
        <ProductionStatusCard compliance={compliance} error={complianceErr} />

        <section className="border border-line rounded-lg bg-surface">
          <header className="px-4 py-2 border-b border-line">
            <h4 className="text-sm font-semibold text-fg">Recent auth failures (last hour)</h4>
          </header>
          <div className="p-3">
            {authFailures.length === 0 ? (
              <p className="text-xs text-fg-faint italic">No authentication failures recorded in the last hour.</p>
            ) : (
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-left text-fg-subtle border-b border-line">
                    <th className="py-1 font-semibold">Actor</th>
                    <th className="py-1 font-semibold">IP</th>
                    <th className="py-1 font-semibold text-right">Count</th>
                    <th className="py-1 font-semibold"></th>
                  </tr>
                </thead>
                <tbody>
                  {authFailures.slice(0, 10).map((row, idx) => (
                    <tr key={idx} className="border-b border-line-soft">
                      <td className="py-1 font-mono">{row.actor || '-'}</td>
                      <td className="py-1 font-mono">{row.ip || '-'}</td>
                      <td className="py-1 text-right font-semibold">{row.count}</td>
                      <td className="py-1 text-right">
                        <button
                          onClick={() => openAuditFor({ actor: row.actor, result: 'fail' })}
                          className="text-accent hover:text-accent inline-flex items-center gap-1"
                        >
                          <FileSearch className="w-3 h-3" />
                          View
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </section>

        <section className="border border-line rounded-lg bg-surface">
          <header className="px-4 py-2 border-b border-line flex items-center justify-between">
            <h4 className="text-sm font-semibold text-fg">Rate-limit & metrics</h4>
            <a
              href="/metrics"
              target="_blank"
              rel="noopener noreferrer"
              className="text-xs text-accent hover:text-accent inline-flex items-center gap-1"
            >
              View Prometheus metrics
              <ExternalLink className="w-3 h-3" />
            </a>
          </header>
          <div className="p-3 text-xs text-fg-subtle">
            Rate-limit breaches and other operational counters are exported on the <span className="font-mono">/metrics</span> endpoint
            (Prometheus format). Subscribe via a webhook for the <span className="font-mono">security.rate_limit_exceeded</span>{' '}
            event to receive real-time alerts.
          </div>
        </section>

        <ExporterStatusSection exporters={exporters} />

        <section className="border border-line rounded-lg bg-surface">
          <header className="px-4 py-2 border-b border-line flex items-center justify-between">
            <h4 className="text-sm font-semibold text-fg">Recent incident events</h4>
            <button
              onClick={() => openAuditFor({ action: 'security.' })}
              className="text-xs text-accent hover:text-accent inline-flex items-center gap-1"
            >
              Open Audit Log
              <FileSearch className="w-3 h-3" />
            </button>
          </header>
          <div className="p-3">
            {incidents.length === 0 ? (
              <p className="text-xs text-fg-faint italic">No recent security or ops incidents recorded.</p>
            ) : (
              <ul className="space-y-1 text-xs">
                {incidents.map((e, idx) => (
                  <li key={idx} className="flex items-start gap-2 border-b border-line-soft pb-1">
                    <AlertTriangle className="w-3.5 h-3.5 text-warning flex-shrink-0 mt-0.5" />
                    <div className="flex-1 min-w-0">
                      <p className="font-mono text-fg">{e.action}</p>
                      <p className="text-fg-faint">
                        {formatTs(e.ts)} · actor={e.actor || '-'} · ip={e.ip || '-'}
                      </p>
                      {e.detail && (
                        <p className="text-fg-subtle truncate" title={e.detail}>
                          {e.detail}
                        </p>
                      )}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </section>
      </div>
    </div>
  );
}

function ProductionStatusCard({ compliance, error }) {
  if (error) {
    return (
      <section className="border border-danger-line rounded-lg bg-danger-muted p-4">
        <p className="text-sm text-danger-strong">Failed to load compliance status: {error}</p>
      </section>
    );
  }
  if (!compliance) {
    return (
      <section className="border border-line rounded-lg bg-surface p-4">
        <p className="text-sm text-fg-faint">Loading compliance status…</p>
      </section>
    );
  }
  if (compliance.production && compliance.compliant) {
    return (
      <section className="border border-success-line rounded-lg bg-success-muted p-4 flex items-start gap-3">
        <CheckCircle2 className="w-5 h-5 text-success-strong flex-shrink-0 mt-0.5" />
        <div>
          <p className="text-sm font-semibold text-success-strong">Production mode active</p>
          <p className="text-xs text-success-strong mt-0.5">
            All ISO 27001 / SOC 2 envelope variables are configured.
          </p>
        </div>
      </section>
    );
  }
  return (
    <section className="border border-danger-line rounded-lg bg-danger-muted p-4">
      <div className="flex items-start gap-3">
        <AlertTriangle className="w-5 h-5 text-danger-strong flex-shrink-0 mt-0.5" />
        <div className="flex-1">
          <p className="text-sm font-semibold text-danger-strong">
            {compliance.production ? 'Production mode — missing requirements' : 'Development mode (not compliant)'}
          </p>
          <p className="text-xs text-danger-strong mt-0.5">
            The following envelope variables must be set for ISO 27001 / SOC 2 compliance:
          </p>
          <ul className="mt-2 space-y-1">
            {(compliance.missing || []).map((m) => (
              <li key={m.envVar} className="text-xs text-danger-strong">
                <span className="font-mono font-semibold">{m.envVar}</span>
                <span className="text-danger-strong"> — want {m.want}</span>
                <span className="block text-[10px] text-danger-strong ml-2">{m.reason}</span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </section>
  );
}

function ExporterStatusSection({ exporters }) {
  return (
    <section className="border border-line rounded-lg bg-surface">
      <header className="px-4 py-2 border-b border-line flex items-center gap-2">
        <Send className="w-4 h-4 text-accent" />
        <h4 className="text-sm font-semibold text-fg">Audit log export</h4>
      </header>
      <div className="p-3">
        {exporters.length === 0 ? (
          <p className="text-xs text-fg-faint italic">
            No external sinks configured. Set <span className="font-mono">MDDB_AUDIT_EXPORT_WEBHOOK_URL</span>
            {' '}or <span className="font-mono">MDDB_AUDIT_EXPORT_SYSLOG_ADDR</span> to mirror audit events to a SIEM or syslog collector.
          </p>
        ) : (
          <table className="w-full text-xs">
            <thead>
              <tr className="text-left text-fg-subtle border-b border-line">
                <th className="py-1 font-semibold">Sink</th>
                <th className="py-1 font-semibold">Target</th>
                <th className="py-1 font-semibold text-right">Delivered</th>
                <th className="py-1 font-semibold text-right">Failed</th>
                <th className="py-1 font-semibold text-right">Dropped</th>
                <th className="py-1 font-semibold">Last error</th>
              </tr>
            </thead>
            <tbody>
              {exporters.map((e, idx) => (
                <tr key={idx} className="border-b border-line-soft">
                  <td className="py-1 font-mono">{e.name}</td>
                  <td className="py-1 font-mono text-fg-subtle truncate max-w-[200px]" title={e.target}>{maskTarget(e.target)}</td>
                  <td className="py-1 text-right font-mono text-success">{e.delivered}</td>
                  <td className="py-1 text-right font-mono text-danger">{e.failed}</td>
                  <td className="py-1 text-right font-mono text-warning">{e.dropped}</td>
                  <td className="py-1 text-fg-faint truncate max-w-[260px]" title={e.lastError || ''}>{e.lastError || ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </section>
  );
}

// maskTarget hides any inline credentials in a webhook URL while
// keeping the host visible so an operator can recognise the sink.
function maskTarget(target) {
  if (!target) return '';
  try {
    const u = new URL(target);
    return `${u.protocol}//${u.host}${u.pathname}`;
  } catch {
    return target;
  }
}

function aggregateFailures(events) {
  const buckets = new Map();
  for (const e of events) {
    const action = e.action || '';
    if (!action.startsWith('auth.')) continue;
    const key = `${e.actor || ''}|${e.ip || ''}`;
    const existing = buckets.get(key);
    if (existing) {
      existing.count += 1;
    } else {
      buckets.set(key, { actor: e.actor, ip: e.ip, count: 1 });
    }
  }
  return Array.from(buckets.values()).sort((a, b) => b.count - a.count);
}

function formatTs(ns) {
  if (!ns) return '';
  try {
    const ms = Math.floor(Number(ns) / 1e6);
    return new Date(ms).toISOString().replace('T', ' ').replace('Z', '');
  } catch {
    return String(ns);
  }
}
