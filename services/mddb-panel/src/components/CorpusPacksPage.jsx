import { useCallback, useEffect, useState } from 'react';
import { Package } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { useMe } from '../lib/use-me';
import { groupPacks } from '../lib/corpus-packs';
import { useCorpusIntent } from '../lib/corpus-intent';
import CorpusPacksCard from './CorpusPacksCard';
import CorpusUploadCard from './CorpusUploadCard';
import CorpusBuildForm from './CorpusBuildForm';

/**
 * CorpusPacksPage (FE-022) is the Corpus packs page, beside the
 * collections: the installed packs, uploading one, and creating one from
 * collections. Listing is open to readers; the rest needs an administrator
 * (admin on "*"), so it is hidden from anyone else when sign-in is on. It
 * shows the intent another page opened it with (corpus-intent.js) until it is left.
 */
export default function CorpusPacksPage() {
  const { stats, setStats, config } = useStore();
  const { me } = useMe();
  const { intent, setIntent } = useCorpusIntent();
  const [packs, setPacks] = useState([]);
  const [loadError, setLoadError] = useState(null);
  const preset = intent?.build;
  const highlight = intent?.pack;
  const canManage = !config?.authEnabled || !!me?.admin;

  const load = useCallback(() => mddbClient.listCorpus()
    .then((data) => { setPacks(groupPacks(data.packs)); setLoadError(null); })
    .catch(setLoadError), []);

  // After an install or a removal the collection list changes too.
  const changed = async () => {
    await load();
    setStats(await mddbClient.getStats());
  };

  useEffect(() => { load(); }, [load]);

  // The intent lasts while the page is open; leaving the page drops it.
  useEffect(() => () => setIntent(null), [setIntent]);

  const collections = (stats?.collections || []).map((c) => c.name);

  return (
    <div className="h-full overflow-y-auto p-4 sm:p-6 bg-surface-muted space-y-4">
      <div>
        <h2 className="text-xl font-bold text-fg flex items-center gap-2">
          <Package className="w-5 h-5" aria-hidden="true" /> Corpus packs
        </h2>
        <p className="text-sm text-fg-muted">
          A pack is a copy of one or more collections in a single file, ready to install on another server.
          Installed packs are read-only.
        </p>
        <details className="mt-2 text-sm text-fg-soft">
          <summary className="cursor-pointer text-info-strong">How packs work</summary>
          <div className="mt-2 space-y-1">
            <p>A pack carries the documents together with their search index, so another server can search them at once, without preparing them again.</p>
            <p>Each collection in a pack comes back as a collection of the same name. Nothing in it can be changed there; to change it, change the original and make a new pack.</p>
            <p>If the other server uses another embedding model, keyword search works on the pack but meaning-based search does not.</p>
            <p>A pack never holds users, keys or settings of this server.</p>
          </div>
        </details>
        {!canManage && (
          <p role="note" className="mt-2 text-sm text-fg-soft">You can see the installed packs. Uploading, creating, downloading and removing packs needs an administrator account.</p>
        )}
      </div>
      <CorpusPacksCard packs={packs} loadError={loadError} canManage={canManage} highlight={highlight} onChanged={changed} />
      {canManage && (
        <div className="grid grid-cols-1 xl:grid-cols-2 gap-4 items-start">
          <CorpusUploadCard onInstalled={changed} />
          {/* A new preset starts the form afresh. */}
          <CorpusBuildForm key={preset?.join('\n') || ''} collections={collections} preset={preset} />
        </div>
      )}
    </div>
  );
}
