/**
 * Drafts of edited documents in localStorage (FE-019). A draft holds only
 * what the editor edits — the Markdown, the metadata — plus the revision the
 * edit started from and when it was saved; never a token or other secret
 * (FE-004). Every storage call is guarded: a browser that blocks storage
 * (private mode, quota, policy) simply keeps no drafts.
 */

/** The storage drafts live in, or null when the browser refuses it. */
function draftStorage() {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}

/**
 * The storage key of a document's draft:
 * `draft:<panel host>:<collection>:<key>:<lang>`.
 */
export function draftKey({ collection, key, lang }, host = globalThis.location?.host ?? '') {
  return `draft:${host}:${collection}:${key}:${lang}`;
}

/** The stored draft of a document, or null when there is none (or it is unreadable). */
export function readDraft(doc, storage = draftStorage()) {
  try {
    const draft = JSON.parse(storage.getItem(draftKey(doc)));
    if (typeof draft?.contentMd !== 'string') return null;
    return {
      contentMd: draft.contentMd,
      meta: draft.meta && typeof draft.meta === 'object' ? draft.meta : {},
      baseRevision: typeof draft.baseRevision === 'string' ? draft.baseRevision : '',
      savedAt: Number(draft.savedAt) || 0,
    };
  } catch {
    return null;
  }
}

/** Stores a draft; returns false when the browser refused it. */
export function writeDraft(doc, { contentMd, meta, baseRevision }, now = Date.now(), storage = draftStorage()) {
  try {
    const draft = { contentMd, meta, baseRevision: baseRevision || '', savedAt: now };
    storage.setItem(draftKey(doc), JSON.stringify(draft));
    return true;
  } catch {
    return false;
  }
}

/** Removes a document's draft, if any. */
export function removeDraft(doc, storage = draftStorage()) {
  try {
    storage.removeItem(draftKey(doc));
  } catch {
    // Nothing stored, nothing to remove.
  }
}

/** Whether two metadata maps hold the same keys and values. */
export function sameMeta(a, b) {
  return JSON.stringify(a || {}) === JSON.stringify(b || {});
}

/** Whether `draft` differs from the stored `doc` (so restoring it changes something). */
export function draftDiffers(draft, doc) {
  return draft.contentMd !== (doc.contentMd || '') || !sameMeta(draft.meta, doc.meta);
}
