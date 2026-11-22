import { PowerOff } from 'lucide-react';
import { useStore } from '../lib/store';
import { featureLabel } from '../lib/features';
import HelpPopover from './HelpPopover';

// GO-049 (2.19.0): shown in place of a page whose server module is switched
// off. The server answers that module's routes with 404 "feature disabled",
// so the page would only show errors. FE-021: plain text first, the module's
// setting name and the operator's lock behind the help control.
export default function FeatureDisabledNotice({ feature, onOpenFeatures }) {
  const { setViewMode } = useStore();
  const openFeatures = onOpenFeatures || (() => setViewMode('features'));
  const label = featureLabel(feature);
  return (
    <div className="bg-surface-muted p-6">
      <section role="status" aria-labelledby="feature-disabled-title" className="max-w-2xl rounded-lg bg-surface p-6 shadow">
        <div className="mb-2 flex items-center gap-2">
          <PowerOff className="h-5 w-5 text-fg-muted" aria-hidden="true" />
          <h2 id="feature-disabled-title" className="text-lg font-semibold text-fg">
            {label} is switched off
          </h2>
          <HelpPopover label={label}>
            <p>
              The server&apos;s <code className="text-fg-soft">{feature}</code> module is off, so its routes answer
              404 &quot;feature disabled&quot;.
            </p>
            <p>
              If the operator locked it with MDDB_FEATURES_FORCE_OFF, only a change of the server&apos;s environment
              turns it back on.
            </p>
          </HelpPopover>
        </div>
        <p className="mb-4 text-sm text-fg-muted">
          This page needs it, so there is nothing to show. A global admin can switch it on in Features.
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
