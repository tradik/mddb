import { useEffect, useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { firstMeta, imageErrorMessage, isImageDocument } from '../lib/image-meta';

/**
 * ImagePreview shows an image document's picture (2.18.9, `GET /v1/image`)
 * with its alt text, caption, page and parent document. It renders nothing
 * for a document that is not an image.
 */
export default function ImagePreview({ collection, docKey, lang, meta }) {
  const image = isImageDocument(meta);
  // The answer is kept with the image it belongs to, so a stale picture is
  // never shown for another key while the new one loads.
  const imageId = `${collection}\u0000${docKey}\u0000${lang ?? ''}`;
  const [loaded, setLoaded] = useState({ id: null, url: null, error: null });

  useEffect(() => {
    if (!image) return undefined;
    let cancelled = false;
    let objectUrl = null;
    mddbClient
      .getImage({ collection, key: docKey, lang })
      .then(({ blob }) => {
        if (cancelled) return;
        objectUrl = URL.createObjectURL(blob);
        setLoaded({ id: imageId, url: objectUrl, error: null });
      })
      .catch((err) => {
        if (!cancelled) setLoaded({ id: imageId, url: null, error: imageErrorMessage(err) });
      });
    return () => {
      cancelled = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [image, imageId, collection, docKey, lang]);

  const current = loaded.id === imageId ? loaded : { url: null, error: null };
  const { url, error } = current;

  if (!image) return null;

  const caption = firstMeta(meta, 'caption');
  const page = firstMeta(meta, 'page');
  const parent = firstMeta(meta, 'parent');

  return (
    <figure className="mb-4 border border-gray-200 rounded-lg p-3 bg-gray-50">
      {error && <p role="alert" className="text-sm text-red-700">{error}</p>}
      {!error && !url && <p className="text-sm text-gray-600">Loading image…</p>}
      {url && <img src={url} alt={firstMeta(meta, 'altText') || ''} className="max-w-full max-h-96 mx-auto" />}
      {(caption || page || parent) && (
        <figcaption className="mt-2 text-xs text-gray-700 space-y-0.5">
          {caption && <div>{caption}</div>}
          {page && <div>Page {page}</div>}
          {parent && <div>From document <span className="font-mono">{parent}</span></div>}
        </figcaption>
      )}
    </figure>
  );
}
