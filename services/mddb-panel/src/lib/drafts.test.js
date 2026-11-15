// @vitest-environment jsdom
// drafts (FE-019): the localStorage key of a document's draft, a draft that
// holds only the edit (never a token), unreadable or refused storage, and
// whether a draft differs from the stored document.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { draftKey, readDraft, writeDraft, removeDraft, sameMeta, draftDiffers } from './drafts';

/** A Storage-like map. */
function memoryStorage() {
  const items = new Map();
  return {
    items,
    getItem: (k) => (items.has(k) ? items.get(k) : null),
    setItem: (k, v) => items.set(k, String(v)),
    removeItem: (k) => items.delete(k),
  };
}

const doc = { collection: 'blog', key: 'hello', lang: 'en' };

afterEach(() => vi.unstubAllGlobals());

describe('draftKey', () => {
  test('names the panel host, collection, key and language', () => {
    expect(draftKey(doc, 'panel:3000')).toBe('draft:panel:3000:blog:hello:en');
    expect(draftKey(doc)).toMatch(/^draft:.*:blog:hello:en$/);
  });

  test('without a location the host part is empty', () => {
    vi.stubGlobal('location', undefined);
    expect(draftKey(doc)).toBe('draft::blog:hello:en');
  });
});

describe('write, read, remove', () => {
  test('a draft round-trips with the time it was saved', () => {
    const storage = memoryStorage();
    expect(writeDraft(doc, { contentMd: '# x', meta: { a: ['1'] }, baseRevision: 'r1' }, 1234, storage)).toBe(true);
    expect(readDraft(doc, storage)).toEqual({ contentMd: '# x', meta: { a: ['1'] }, baseRevision: 'r1', savedAt: 1234 });
    removeDraft(doc, storage);
    expect(readDraft(doc, storage)).toBeNull();
  });

  test('only the edit is stored: extra fields such as a token are dropped', () => {
    const storage = memoryStorage();
    writeDraft(doc, { contentMd: 'x', meta: {}, baseRevision: '', token: 'secret-jwt' }, 1, storage);
    const raw = [...storage.items.values()][0];
    expect(raw).not.toContain('secret-jwt');
    expect(JSON.parse(raw)).toEqual({ contentMd: 'x', meta: {}, baseRevision: '', savedAt: 1 });
  });

  test('defaults: no base revision, now as the time', () => {
    const storage = memoryStorage();
    writeDraft(doc, { contentMd: 'x', meta: {} }, undefined, storage);
    const draft = readDraft(doc, storage);
    expect(draft.baseRevision).toBe('');
    expect(draft.savedAt).toBeGreaterThan(0);
  });

  test('a malformed draft reads as none, or with safe defaults', () => {
    const storage = memoryStorage();
    storage.setItem(draftKey(doc), 'not json');
    expect(readDraft(doc, storage)).toBeNull();
    storage.setItem(draftKey(doc), JSON.stringify({ meta: {} }));
    expect(readDraft(doc, storage)).toBeNull();
    storage.setItem(draftKey(doc), JSON.stringify({ contentMd: 'x', meta: 'bad', baseRevision: 7, savedAt: 'never' }));
    expect(readDraft(doc, storage)).toEqual({ contentMd: 'x', meta: {}, baseRevision: '', savedAt: 0 });
    expect(readDraft({ ...doc, key: 'other' }, storage)).toBeNull();
  });

  test('refused storage keeps no drafts and throws nothing', () => {
    const refusing = {
      getItem: () => { throw new Error('denied'); },
      setItem: () => { throw new Error('quota'); },
      removeItem: () => { throw new Error('denied'); },
    };
    expect(readDraft(doc, refusing)).toBeNull();
    expect(writeDraft(doc, { contentMd: 'x', meta: {} }, 1, refusing)).toBe(false);
    expect(() => removeDraft(doc, refusing)).not.toThrow();
  });

  test('without localStorage (or when reading it throws) nothing is kept', () => {
    vi.stubGlobal('localStorage', undefined);
    expect(writeDraft(doc, { contentMd: 'x', meta: {} })).toBe(false);
    expect(readDraft(doc)).toBeNull();
    vi.unstubAllGlobals();
    const original = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
    Object.defineProperty(globalThis, 'localStorage', { configurable: true, get() { throw new Error('SecurityError'); } });
    try {
      expect(readDraft(doc)).toBeNull();
      expect(() => removeDraft(doc)).not.toThrow();
    } finally {
      Object.defineProperty(globalThis, 'localStorage', original);
    }
  });
});

describe('comparisons', () => {
  test('sameMeta treats a missing map as empty', () => {
    expect(sameMeta({ a: ['1'] }, { a: ['1'] })).toBe(true);
    expect(sameMeta(undefined, {})).toBe(true);
    expect(sameMeta({ a: ['1'] }, { a: ['2'] })).toBe(false);
  });

  test('draftDiffers compares content and metadata', () => {
    const draft = { contentMd: 'x', meta: {} };
    expect(draftDiffers(draft, { contentMd: 'x' })).toBe(false);
    expect(draftDiffers(draft, { contentMd: 'y', meta: {} })).toBe(true);
    expect(draftDiffers({ contentMd: '', meta: {} }, {})).toBe(false);
    expect(draftDiffers({ contentMd: 'x', meta: { a: ['1'] } }, { contentMd: 'x', meta: {} })).toBe(true);
  });
});
