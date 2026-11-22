import { Package, PackagePlus, Lock } from 'lucide-react';
import { useStore } from '../lib/store';
import { openCorpusPage } from '../lib/corpus-intent';

// FE-022: the ways into the Corpus packs page from the collection list —
// kept here so the sidebar places each with one line.

const rowButton = 'opacity-0 group-hover:opacity-100 focus:opacity-100 p-1 text-fg-faint hover:text-fg-subtle hover:bg-surface-subtle rounded transition-opacity';

/** Beside the collection list's title: install a pack or create one. */
export function CorpusPacksButton() {
  return (
    <button type="button" onClick={() => openCorpusPage()} title="Corpus packs: install or create a pack"
      aria-label="Corpus packs: install or create a pack"
      className="ml-auto mr-1 p-1.5 text-fg-soft rounded-lg hover:bg-surface-subtle transition-colors">
      <Package className="w-3.5 h-3.5" aria-hidden="true" />
    </button>
  );
}

/**
 * On a collection's row: a collection installed from a pack shows where it
 * came from, read-only, and opens that pack; any other offers to create a
 * pack from it.
 */
export function CollectionPackButton({ collection }) {
  const pack = useStore((s) => s.collectionConfigs?.[collection]?.corpus);
  if (pack) {
    const label = `From pack ${pack.name}${pack.version ? ' v' + pack.version : ''}, read-only`;
    return (
      <button type="button" onClick={(e) => { e.stopPropagation(); openCorpusPage({ pack: pack.name }); }}
        title={label} aria-label={label} className="p-1 text-fg-subtle hover:bg-surface-subtle rounded">
        <Lock className="w-3 h-3" aria-hidden="true" />
      </button>
    );
  }
  const label = `Create pack from ${collection}`;
  return (
    <button type="button" onClick={(e) => { e.stopPropagation(); openCorpusPage({ build: [collection] }); }}
      title={label} aria-label={label} className={rowButton}>
      <PackagePlus className="w-3 h-3" aria-hidden="true" />
    </button>
  );
}
