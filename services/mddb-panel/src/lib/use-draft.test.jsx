// @vitest-environment jsdom
// useDraftAutosave (FE-019): a draft is written a second after the last
// change, removed when the edit matches the document again, left alone while
// paused, written at once when the editor closes or the page is left, and
// the browser asks before leaving unsaved changes.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, cleanup } from '@testing-library/react';
import { useDraftAutosave, DRAFT_DELAY_MS } from './use-draft';
import { readDraft, writeDraft } from './drafts';

const doc = { collection: 'blog', key: 'hello', lang: 'en' };
const edit = (contentMd, baseRevision = 'r1') => ({ contentMd, meta: {}, baseRevision });

function setup(initial) {
  return renderHook((props) => useDraftAutosave(props.doc, props.draft, props.options), { initialProps: initial });
}

beforeEach(() => {
  vi.useFakeTimers();
  localStorage.clear();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('useDraftAutosave', () => {
  test('writes the draft once typing pauses', () => {
    const { rerender } = setup({ doc, draft: edit('a'), options: { dirty: true } });
    rerender({ doc, draft: edit('ab'), options: { dirty: true } });
    act(() => vi.advanceTimersByTime(DRAFT_DELAY_MS - 1));
    expect(readDraft(doc)).toBeNull();
    act(() => vi.advanceTimersByTime(1));
    expect(readDraft(doc)).toMatchObject({ contentMd: 'ab', baseRevision: 'r1' });
  });

  test('a clean edit removes the stored draft; a paused one keeps it', () => {
    writeDraft(doc, edit('old'));
    const { rerender } = setup({ doc, draft: edit('x'), options: { dirty: true, paused: true } });
    act(() => vi.advanceTimersByTime(DRAFT_DELAY_MS * 2));
    expect(readDraft(doc).contentMd).toBe('old');
    rerender({ doc, draft: edit('x'), options: { dirty: false } });
    expect(readDraft(doc)).toBeNull();
  });

  test('closing the editor writes the pending draft at once', () => {
    const { unmount } = setup({ doc, draft: edit('typed'), options: { dirty: true, delay: 5000 } });
    unmount();
    expect(readDraft(doc).contentMd).toBe('typed');
  });

  test('going clean before the timer fires writes nothing, not even on close', () => {
    const { rerender, unmount } = setup({ doc, draft: edit('typed'), options: { dirty: true } });
    rerender({ doc, draft: edit(''), options: { dirty: false } });
    unmount();
    act(() => vi.advanceTimersByTime(DRAFT_DELAY_MS * 2));
    expect(readDraft(doc)).toBeNull();
  });

  test('discard drops the pending write and the stored draft', () => {
    writeDraft(doc, edit('old'));
    const { result, unmount } = setup({ doc, draft: edit('new'), options: { dirty: true } });
    act(() => result.current.discard());
    unmount();
    act(() => vi.advanceTimersByTime(DRAFT_DELAY_MS * 2));
    expect(readDraft(doc)).toBeNull();
    // A second discard with nothing pending is harmless.
    act(() => result.current.discard());
  });

  test('leaving the page with unsaved changes asks first and keeps the draft', () => {
    const { rerender } = setup({ doc, draft: edit('unsaved'), options: { dirty: true } });
    const event = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(true);
    expect(readDraft(doc).contentMd).toBe('unsaved');
    // Flushed already: a second unload writes nothing new and still asks.
    const again = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(again);
    expect(again.defaultPrevented).toBe(true);
    rerender({ doc, draft: edit(''), options: { dirty: false } });
    const clean = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(clean);
    expect(clean.defaultPrevented).toBe(false);
  });
});
