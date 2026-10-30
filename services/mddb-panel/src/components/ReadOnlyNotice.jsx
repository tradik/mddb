import { Lock } from 'lucide-react';

/** Where the collection came from, ending the "Read-only" sentence. */
function packSource(pack) {
  if (!pack) return '. ';
  const version = pack.version ? ' v' + pack.version : '';
  return ` — installed from corpus pack ${pack.name}${version} (${pack.license}). `;
}

// AGT-005: a collection installed from a corpus pack is read-only. Every
// write — add, update, delete, upload, reindex, TTL, sync — is refused with
// 403 READ_ONLY, so the panel says so before an operator tries.
export default function ReadOnlyNotice({ config, compact = false }) {
  if (!config?.readOnly) return null;
  return (
    <div role="note" className={`flex items-start gap-2 rounded-lg border border-gray-300 bg-gray-50 ${compact ? 'p-2' : 'p-3'}`}>
      <Lock className="mt-0.5 h-4 w-4 shrink-0 text-gray-700" aria-hidden="true" />
      <p className="text-sm text-gray-800">
        <strong>Read-only</strong>
        {packSource(config.corpus)}
        Writes to its documents are refused; remove the pack on the System page to write to this name.
      </p>
    </div>
  );
}
