import { Wrench } from 'lucide-react';
import CollectionMaintenanceCard from './CollectionMaintenanceCard';
import IndexingStatusCard from './IndexingStatusCard';

/**
 * The Collection Maintenance page (2.19.11), in the Collections group: the
 * upkeep of one collection — checksum, export, truncating its history — and
 * whether what was written is searchable yet. Both used to sit on the System
 * Info page.
 */
export default function CollectionMaintenancePanel() {
  return (
    <div className="bg-surface-muted p-6">
      <div className="max-w-4xl mx-auto space-y-6">
        <header>
          <h1 className="text-2xl font-bold text-fg mb-2 flex items-center gap-2">
            <Wrench className="w-6 h-6 text-accent" aria-hidden="true" /> Collection Maintenance
          </h1>
          <p className="text-fg-subtle">Check, copy and tidy up a collection, and see whether new documents are searchable yet.</p>
        </header>
        <CollectionMaintenanceCard />
        <IndexingStatusCard />
      </div>
    </div>
  );
}
