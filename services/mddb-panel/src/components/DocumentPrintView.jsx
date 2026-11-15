import { format } from 'date-fns';
import MarkdownView from './MarkdownView';
import { metaText } from '../lib/meta-diff';

/**
 * DocumentPrintView is a document as a page of its own, without the panel
 * around it (FE-017): its key as the title, collection, language and last
 * update, the metadata, then the content rendered by MarkdownView — the same
 * sanitized pipeline as the viewer. Print / PDF shows it, and Download HTML
 * renders it to static markup.
 */
export default function DocumentPrintView({ document }) {
  const meta = Object.entries(document.meta || {});
  const facts = [
    document.collection,
    document.lang,
    document.updatedAt ? `updated ${format(new Date(document.updatedAt * 1000), 'PPpp')}` : null,
  ].filter(Boolean);
  return (
    <article className="document-export">
      <header className="mb-6">
        <h1 className="text-3xl font-bold text-gray-900">{document.key}</h1>
        <p className="document-meta-line text-sm text-gray-700">{facts.join(' · ')}</p>
      </header>
      {meta.length > 0 && (
        <dl className="mb-6 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
          {meta.map(([key, value]) => (
            <div key={key} className="contents">
              <dt className="font-semibold text-gray-800">{key}</dt>
              <dd className="text-gray-700">{metaText(value)}</dd>
            </div>
          ))}
        </dl>
      )}
      <MarkdownView>{document.contentMd || ''}</MarkdownView>
    </article>
  );
}
