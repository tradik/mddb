import ClassifyBox from './ClassifyBox';
import TtlControl from './TtlControl';

/**
 * DocumentTools groups the per-document actions of the viewer that are not
 * editing: expiry (TTL) and zero-shot classification, each folded away until
 * opened. Keyed by the document, so switching documents starts afresh.
 */
export default function DocumentTools({ document: doc }) {
  return (
    <div className="border-b border-gray-200 px-4 py-2 space-y-1">
      <details>
        <summary className="cursor-pointer text-sm font-medium text-gray-900">Expiry (TTL)</summary>
        <div className="pt-2"><TtlControl document={doc} /></div>
      </details>
      <details>
        <summary className="cursor-pointer text-sm font-medium text-gray-900">Classify</summary>
        <div className="pt-2"><ClassifyBox document={doc} /></div>
      </details>
    </div>
  );
}
