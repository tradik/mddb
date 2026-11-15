import { useEffect, useState } from 'react';
import { Lock, RefreshCw, KeyRound, Play, AlertTriangle, CheckCircle2 } from 'lucide-react';
import mddbClient from '../lib/mddb-client';

// EncryptionPanel — admin surface for the at-rest encryption subsystem.
// Renders the current key posture, per-collection rotation coverage,
// and lets an operator kick off a re-encryption job.
export default function EncryptionPanel() {
  const [status, setStatus] = useState(null);
  const [jobs, setJobs] = useState([]);
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(false);
  const [rotating, setRotating] = useState(false);
  const [scopeCollection, setScopeCollection] = useState('');

  const refresh = async () => {
    setLoading(true);
    try {
      const [s, j] = await Promise.all([
        mddbClient.getEncryptionStatus(),
        mddbClient.listRotationJobs().catch(() => ({ jobs: [] })),
      ]);
      setStatus(s);
      setJobs(j.jobs || []);
      setError(null);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
  }, []);

  const startRotation = async () => {
    if (!confirm(
      `Start re-encryption${scopeCollection ? ` for "${scopeCollection}"` : ' across all collections'}?\n\n` +
      'Each document will be decrypted under the previous key and re-sealed under the current primary key. ' +
      'The job runs in the background; the database remains writable while it runs.'
    )) {
      return;
    }
    setRotating(true);
    try {
      await mddbClient.rotateEncryption({ collection: scopeCollection });
      // Wait one beat then refresh to show the queued job.
      setTimeout(refresh, 500);
    } catch (err) {
      setError(err.message);
    } finally {
      setRotating(false);
    }
  };

  return (
    <div className="flex flex-col h-full overflow-y-auto">
      <div className="px-4 py-3 border-b border-line flex items-center justify-between">
        <div>
          <h3 className="text-lg font-semibold text-fg flex items-center gap-2">
            <Lock className="w-5 h-5 text-accent" />
            At-rest encryption
          </h3>
          <p className="text-xs text-fg-faint mt-0.5">
            AES-256-GCM key rotation and per-collection coverage (ISO 27001 A.8.24 / SOC 2 CC6.7).
          </p>
        </div>
        <button
          onClick={refresh}
          disabled={loading}
          className="flex items-center gap-1 px-3 py-1.5 bg-primary-600 text-white rounded-lg hover:bg-primary-700 disabled:opacity-50 text-sm"
        >
          <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
          Refresh
        </button>
      </div>

      <div className="p-4 space-y-4">
        {error && (
          <div className="border border-danger-line bg-danger-muted rounded-lg p-3 text-sm text-danger-strong">
            {error}
          </div>
        )}

        {!status ? (
          <p className="text-sm text-fg-faint">Loading…</p>
        ) : !status.enabled ? (
          <div className="border border-warning-line bg-warning-soft rounded-lg p-4 flex items-start gap-3">
            <AlertTriangle className="w-5 h-5 text-warning flex-shrink-0 mt-0.5" />
            <div className="text-sm text-warning-strong">
              At-rest encryption is not configured. Set <span className="font-mono">MDDB_ENCRYPTION_KEY</span> (32 random bytes,
              base64-encoded) on the server to opt collections into AES-256-GCM encryption.
            </div>
          </div>
        ) : (
          <>
            <KeyPostureCard status={status} />
            <CollectionsTable collections={status.collections || []} primary={status.primaryKeyID} />
            <RotationCard
              scopeCollection={scopeCollection}
              setScopeCollection={setScopeCollection}
              startRotation={startRotation}
              rotating={rotating}
              currentJobID={status.currentJobID}
            />
            <JobsTable jobs={jobs} />
          </>
        )}
      </div>
    </div>
  );
}

function KeyPostureCard({ status }) {
  return (
    <section className="border border-line rounded-lg bg-surface">
      <header className="px-4 py-2 border-b border-line">
        <h4 className="text-sm font-semibold text-fg flex items-center gap-2">
          <KeyRound className="w-4 h-4 text-accent" />
          Key posture
        </h4>
      </header>
      <div className="p-3 grid grid-cols-2 gap-3 text-sm">
        <div>
          <p className="text-xs text-fg-faint uppercase tracking-wide">Primary key ID</p>
          <p className="font-mono text-lg font-semibold text-fg">{status.primaryKeyID}</p>
        </div>
        <div>
          <p className="text-xs text-fg-faint uppercase tracking-wide">Read-only previous keys</p>
          <p className="font-mono text-sm text-fg">
            {status.previousKeyIDs?.length ? status.previousKeyIDs.join(', ') : 'none'}
          </p>
        </div>
      </div>
    </section>
  );
}

function CollectionsTable({ collections, primary }) {
  if (collections.length === 0) {
    return (
      <p className="text-xs text-fg-faint italic">No collections in the database.</p>
    );
  }
  return (
    <section className="border border-line rounded-lg bg-surface">
      <header className="px-4 py-2 border-b border-line">
        <h4 className="text-sm font-semibold text-fg">Per-collection key coverage</h4>
      </header>
      <table className="w-full text-xs">
        <thead>
          <tr className="text-left text-fg-subtle border-b border-line bg-surface-muted">
            <th className="px-3 py-2">Collection</th>
            <th className="px-3 py-2">Encrypted</th>
            <th className="px-3 py-2 text-right">Total</th>
            <th className="px-3 py-2 text-right">Primary (id={primary})</th>
            <th className="px-3 py-2 text-right">Legacy</th>
            <th className="px-3 py-2 text-right">Plaintext</th>
          </tr>
        </thead>
        <tbody>
          {collections.map((c) => (
            <tr key={c.collection} className="border-b border-line-soft">
              <td className="px-3 py-2 font-mono">{c.collection}</td>
              <td className="px-3 py-2">
                {c.encrypted ? (
                  <span className="inline-flex items-center gap-1 text-success-strong">
                    <CheckCircle2 className="w-3 h-3" /> yes
                  </span>
                ) : (
                  <span className="text-fg-faint">no</span>
                )}
              </td>
              <td className="px-3 py-2 text-right font-mono">{c.total}</td>
              <td className="px-3 py-2 text-right font-mono">{c.withPrimary}</td>
              <td className="px-3 py-2 text-right font-mono text-warning">{c.withLegacy}</td>
              <td className="px-3 py-2 text-right font-mono text-fg-faint">{c.plaintext}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

function RotationCard({ scopeCollection, setScopeCollection, startRotation, rotating, currentJobID }) {
  return (
    <section className="border border-line rounded-lg bg-surface">
      <header className="px-4 py-2 border-b border-line">
        <h4 className="text-sm font-semibold text-fg">Run re-encryption</h4>
      </header>
      <div className="p-3 space-y-2">
        {currentJobID && (
          <div className="text-xs text-warning-strong bg-warning-soft border border-warning-line rounded px-2 py-1">
            A rotation job is currently running ({currentJobID}). Starting another will return the same ID.
          </div>
        )}
        <div className="flex items-center gap-2">
          <input
            type="text"
            placeholder="collection (empty = all)"
            value={scopeCollection}
            onChange={(e) => setScopeCollection(e.target.value)}
            className="flex-1 px-2 py-1 border border-line-strong rounded text-sm font-mono"
          />
          <button
            onClick={startRotation}
            disabled={rotating}
            className="flex items-center gap-1 px-3 py-1 bg-primary-600 text-white rounded hover:bg-primary-700 disabled:opacity-50 text-sm"
          >
            <Play className="w-3.5 h-3.5" />
            Start rotation
          </button>
        </div>
        <p className="text-[11px] text-fg-faint">
          Each document is re-sealed under the current primary key. Plaintext entries are skipped.
        </p>
      </div>
    </section>
  );
}

function JobsTable({ jobs }) {
  if (!jobs || jobs.length === 0) {
    return null;
  }
  return (
    <section className="border border-line rounded-lg bg-surface">
      <header className="px-4 py-2 border-b border-line">
        <h4 className="text-sm font-semibold text-fg">Rotation jobs</h4>
      </header>
      <table className="w-full text-xs">
        <thead>
          <tr className="text-left text-fg-subtle border-b border-line bg-surface-muted">
            <th className="px-3 py-2">Job</th>
            <th className="px-3 py-2">Status</th>
            <th className="px-3 py-2 text-right">Scanned</th>
            <th className="px-3 py-2 text-right">Re-encrypted</th>
            <th className="px-3 py-2 text-right">Errors</th>
          </tr>
        </thead>
        <tbody>
          {jobs.map((j) => (
            <tr key={j.id} className="border-b border-line-soft">
              <td className="px-3 py-2 font-mono">{j.id}</td>
              <td className="px-3 py-2">{j.status}</td>
              <td className="px-3 py-2 text-right font-mono">{j.scanned}</td>
              <td className="px-3 py-2 text-right font-mono">{j.reencrypted}</td>
              <td className="px-3 py-2 text-right font-mono text-danger">{j.errors}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
