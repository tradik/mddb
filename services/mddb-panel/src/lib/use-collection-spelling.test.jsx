// @vitest-environment jsdom
// useCollectionSpelling (FE-020): nothing is sent while off; when on, a
// debounced request carries only words never checked before, in batches;
// answers are kept per collection and language; a failure is reported once
// and not retried in a loop.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, cleanup } from '@testing-library/react';

vi.mock('./mddb-client', () => ({ default: { spellSuggest: vi.fn() } }));

const { default: mddbClient } = await import('./mddb-client');
const { useCollectionSpelling, SPELL_DELAY_MS } = await import('./use-collection-spelling');

const props = (over = {}) => ({ enabled: true, collection: 'blog', lang: 'en', text: 'Teh cat sat', ...over });
const setup = (initial) => renderHook((p) => useCollectionSpelling(p), { initialProps: initial });
const tick = async (ms = SPELL_DELAY_MS) => act(async () => { vi.advanceTimersByTime(ms); });

beforeEach(() => {
  vi.useFakeTimers();
  mddbClient.spellSuggest.mockReset().mockResolvedValue({
    tokenSuggestions: [{ original: 'teh', corrected: 'the', confidence: 0.65 }],
  });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('useCollectionSpelling', () => {
  test('off: no request, no suggestions', async () => {
    const { result } = setup(props({ enabled: false }));
    await tick();
    expect(mddbClient.spellSuggest).not.toHaveBeenCalled();
    expect(result.current).toEqual({ suggestions: [], checking: false, error: null });
  });

  test('on: one request after the pause, with a plain word list', async () => {
    const { result } = setup(props());
    await tick(SPELL_DELAY_MS - 1);
    expect(mddbClient.spellSuggest).not.toHaveBeenCalled();
    await tick(1);
    expect(mddbClient.spellSuggest).toHaveBeenCalledWith({ collection: 'blog', lang: 'en', text: 'teh cat sat', maxSuggestions: 3 });
    expect(result.current.suggestions).toEqual([{ word: 'teh', corrected: 'the' }]);
    expect(result.current.checking).toBe(false);
  });

  test('shows "checking" while the request runs', async () => {
    let finish;
    mddbClient.spellSuggest.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    const { result } = setup(props());
    await tick();
    expect(result.current.checking).toBe(true);
    await act(async () => finish({}));
    expect(result.current.checking).toBe(false);
    expect(result.current.suggestions).toEqual([]);
  });

  test('typing sends only new words; known ones are not sent again', async () => {
    const { result, rerender } = setup(props());
    await tick();
    mddbClient.spellSuggest.mockResolvedValue({ tokenSuggestions: [] });
    rerender(props({ text: 'Teh cat sat down' }));
    await tick();
    expect(mddbClient.spellSuggest).toHaveBeenLastCalledWith({ collection: 'blog', lang: 'en', text: 'down', maxSuggestions: 1 });
    rerender(props({ text: 'teh sat' }));
    await tick();
    expect(mddbClient.spellSuggest).toHaveBeenCalledTimes(2);
    expect(result.current.suggestions).toEqual([{ word: 'teh', corrected: 'the' }]);
  });

  test('a long text goes in batches', async () => {
    mddbClient.spellSuggest.mockResolvedValue({ tokenSuggestions: [] });
    setup({ ...props({ text: 'one two three four five' }), batch: 2 });
    await tick();
    await tick();
    await tick();
    expect(mddbClient.spellSuggest.mock.calls.map((c) => c[0].text)).toEqual(['one two', 'three four', 'five']);
  });

  test('another collection or language starts afresh', async () => {
    const { rerender } = setup(props());
    await tick();
    rerender(props({ lang: 'pl' }));
    await tick();
    expect(mddbClient.spellSuggest).toHaveBeenLastCalledWith({ collection: 'blog', lang: 'pl', text: 'teh cat sat', maxSuggestions: 3 });
  });

  test('a scope change during a request does not mix the answers in', async () => {
    let finish;
    mddbClient.spellSuggest.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    const { result, rerender } = setup(props());
    await tick();
    rerender(props({ collection: 'docs' }));
    mddbClient.spellSuggest.mockResolvedValue({ tokenSuggestions: [] });
    await act(async () => finish({ tokenSuggestions: [{ original: 'teh', corrected: 'the' }] }));
    expect(result.current.suggestions).toEqual([]);
  });

  test('a failure is reported and not retried until the words change', async () => {
    mddbClient.spellSuggest.mockRejectedValue(new Error('spell index loading, please retry'));
    const { result, rerender } = setup(props());
    await tick();
    expect(result.current.error).toBe('spell index loading, please retry');
    await tick(SPELL_DELAY_MS * 3);
    expect(mddbClient.spellSuggest).toHaveBeenCalledTimes(1);
    mddbClient.spellSuggest.mockResolvedValue({});
    rerender(props({ text: 'Teh cat sat again' }));
    await tick();
    expect(result.current.error).toBeNull();
  });
});
