import { useEffect, useState } from 'react';
import { AlertTriangle, X } from 'lucide-react';
import mddbClient from '../lib/mddb-client';

// ComplianceBanner — renders a dismissible amber banner at the top of the
// app when the backend reports that it is NOT running in production mode
// (i.e. MDDB_PRODUCTION != true or required ISO 27001 / SOC 2 envelope
// variables are missing). Dismissal is remembered in localStorage.
const STORAGE_KEY = 'mddb.complianceBannerDismissed';

export default function ComplianceBanner() {
  const [status, setStatus] = useState(null);
  const [dismissed, setDismissed] = useState(() => {
    try {
      return window.localStorage.getItem(STORAGE_KEY) === '1';
    } catch {
      return false;
    }
  });

  useEffect(() => {
    let cancelled = false;
    mddbClient
      .getComplianceStatus()
      .then((data) => {
        if (!cancelled) setStatus(data);
      })
      .catch(() => {
        // Endpoint unavailable (older server) — stay silent.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const handleDismiss = () => {
    try {
      window.localStorage.setItem(STORAGE_KEY, '1');
    } catch {
      // storage unavailable, dismiss for this session only
    }
    setDismissed(true);
  };

  if (dismissed || !status) return null;
  if (status.production && status.compliant) return null;

  return (
    <div className="flex-shrink-0 bg-warning-muted border-b border-warning-line text-warning-strong">
      <div className="px-4 py-2 flex items-start gap-3">
        <AlertTriangle className="w-5 h-5 flex-shrink-0 mt-0.5 text-warning" />
        <div className="flex-1 text-sm">
          <p className="font-semibold">
            Running with insecure defaults. Set <span className="font-mono">MDDB_PRODUCTION=true</span> for ISO 27001 / SOC 2 compliance.
          </p>
          {status.missing && status.missing.length > 0 && (
            <p className="text-xs mt-1 text-warning-strong">
              Missing: {status.missing.map((m) => m.envVar).join(', ')}. See docs/config.md.
            </p>
          )}
        </div>
        <button
          onClick={handleDismiss}
          className="flex-shrink-0 p-1 text-warning-strong hover:text-warning-strong hover:bg-warning-muted rounded transition-colors"
          title="Dismiss"
        >
          <X className="w-4 h-4" />
        </button>
      </div>
    </div>
  );
}
