import { PowerOff } from 'lucide-react';
import { useStore } from '../lib/store';
import { featureLabel } from '../lib/features';

// GO-049 (2.19.0): shown in place of a page whose server module is switched
// off. The server answers that module's routes with 404 "feature disabled",
// so the page would only show errors.
export default function FeatureDisabledNotice({ feature, onOpenFeatures }) {
  const { setViewMode } = useStore();
  const openFeatures = onOpenFeatures || (() => setViewMode('features'));
  return (
    <div className="h-full overflow-y-auto bg-surface-muted p-6">
      <section role="status" aria-labelledby="feature-disabled-title" className="max-w-2xl rounded-lg bg-surface p-6 shadow">
        <div className="mb-2 flex items-center gap-2">
          <PowerOff className="h-5 w-5 text-fg-muted" aria-hidden="true" />
          <h2 id="feature-disabled-title" className="text-lg font-semibold text-fg">
            {featureLabel(feature)} is switched off
          </h2>
        </div>
        <p className="mb-4 text-sm text-fg-muted">
          The server's <code className="rounded bg-surface-subtle px-1 text-fg">{feature}</code> module is disabled,
          so this page has nothing to talk to. A global admin can turn it back on from the Features page,
          unless the operator forced it off with <code className="rounded bg-surface-subtle px-1 text-fg">MDDB_FEATURES_FORCE_OFF</code>.
        </p>
        <button
          type="button"
          onClick={openFeatures}
          className="rounded bg-primary-700 px-3 py-1.5 text-sm text-white hover:bg-primary-800"
        >
          Open Features
        </button>
      </section>
    </div>
  );
}
