import { Lock } from 'lucide-react';
import { openCorpusPage } from '../lib/corpus-intent';

/** Where the collection came from, ending the "Read-only" sentence. */
function packSource(pack) {
  if (!pack) return '. ';
  const version = pack.version ? ' v' + pack.version : '';
  return ` — installed from corpus pack ${pack.name}${version} (${pack.license}). `;
}

// AGT-005: a collection installed from a corpus pack is read-only. Every
// write — add, update, delete, upload, reindex, TTL, sync — is refused with
// 403 READ_ONLY, so the panel says so before an operator tries. FE-022: the
// note links to the pack on the Corpus packs page.
export default function ReadOnlyNotice({ config, compact = false }) {
  if (!config?.readOnly) return null;
  return (
    <div role="note" className={`flex items-start gap-2 rounded-lg border border-line-strong bg-surface-muted ${compact ? 'p-2' : 'p-3'}`}>
      <Lock className="mt-0.5 h-4 w-4 shrink-0 text-fg-muted" aria-hidden="true" />
      <p className="text-sm text-fg-soft">
        <strong>Read-only</strong>
        {packSource(config.corpus)}
        Writes to its documents are refused; to write to this name, remove the pack on the{' '}
        <button type="button" onClick={() => openCorpusPage(config.corpus ? { pack: config.corpus.name } : {})} className="underline text-info-strong hover:text-fg">
          Corpus packs
        </button>{' '}page.
      </p>
    </div>
  );
}
