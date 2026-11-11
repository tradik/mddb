/**
 * Parsing of JSON the operator types or pastes: a schema, document metadata,
 * a list of documents. Errors are returned, not thrown, so a form can show
 * them next to the field while the operator is still typing.
 */

/** `{ value }` for valid JSON, `{ error }` (the parser's message) otherwise; blank text is `{ value: undefined }`. */
export function parseJson(text) {
  if (!text.trim()) return { value: undefined };
  try {
    return { value: JSON.parse(text) };
  } catch (err) {
    return { error: err.message };
  }
}

/** True for a plain object (not an array, not null). */
export function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * The error to show under a field that must hold a JSON object: the parse
 * error, `notObject` for valid JSON of another kind, null when fine or blank.
 */
export function jsonObjectError(text, notObject) {
  const { value, error } = parseJson(text);
  if (error) return error;
  if (value !== undefined && !isPlainObject(value)) return notObject;
  return null;
}

/**
 * Documents from pasted text: a JSON array of documents, or NDJSON (one
 * document per line). A document without `lang` gets `defaultLang`. Returns
 * `{ documents }` or `{ error }` naming the line that failed.
 */
export function parseDocuments(text, defaultLang = 'en') {
  const trimmed = text.trim();
  if (!trimmed) return { error: 'Paste at least one document.' };
  if (trimmed.startsWith('[')) {
    const { value, error } = parseJson(trimmed);
    if (error) return { error };
    return withLang(checkDocuments(value), defaultLang);
  }
  const documents = [];
  const lines = trimmed.split('\n');
  for (let i = 0; i < lines.length; i++) {
    if (!lines[i].trim()) continue;
    const { value, error } = parseJson(lines[i]);
    if (error) return { error: `Line ${i + 1}: ${error}` };
    documents.push(value);
  }
  return withLang(checkDocuments(documents), defaultLang);
}

/** Fills the language of documents that name none. */
function withLang(result, lang) {
  if (result.error) return result;
  return { documents: result.documents.map((doc) => (doc.lang ? doc : { ...doc, lang })) };
}

/** Every entry must be an object with a key; the server checks the rest and reports per document. */
function checkDocuments(documents) {
  if (!Array.isArray(documents) || documents.length === 0) return { error: 'Expected a non-empty list of documents.' };
  const bad = documents.findIndex((doc) => !isPlainObject(doc) || !doc.key);
  if (bad !== -1) return { error: `Document ${bad + 1} has no "key".` };
  return { documents };
}
