/**
 * Reading a /v1/find-duplicates answer: each mode answers in its own field
 * (exactGroups, similarGroups, minhashGroups — absent when empty), and a
 * group's documents carry `docId` ("collection|key|lang") and `key` but no
 * `lang`, so the language is taken from the id.
 */

const GROUP_FIELDS = ['exactGroups', 'similarGroups', 'minhashGroups'];

/** Every group of the answer, exact first, each tagged with a unique `id`. */
export function duplicateGroups(result) {
  return GROUP_FIELDS.flatMap((field) => (result?.[field] || []).map((group) => ({ ...group, id: `${group.type}-${group.groupId}` })));
}

/** The language part of a document id ("blog|post|en" → "en"). */
export function langOfDocId(docId) {
  return docId.slice(docId.lastIndexOf('|') + 1);
}

/** A similarity as a percentage with one decimal, or "" when absent. */
export function formatScore(score) {
  return typeof score === 'number' ? `${(score * 100).toFixed(1)}%` : '';
}
