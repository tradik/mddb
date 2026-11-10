// Helpers for image documents (2.18.9): meta values are lists of strings.

/** The first value of a meta key. */
export function firstMeta(meta, key) {
  const value = meta?.[key];
  return Array.isArray(value) ? value[0] : value;
}

/** Whether a document's meta marks it as an image document (`meta.kind: image`). */
export function isImageDocument(meta) {
  const kind = meta?.kind;
  return Array.isArray(kind) ? kind.includes('image') : kind === 'image';
}

/** A user-facing message for a failed `GET /v1/image`. */
export function imageErrorMessage(err) {
  return /404|not found/i.test(err.message) ? 'No image stored for this key.' : err.message;
}
