// @vitest-environment jsdom
// features (GO-049): the module → page map, the pure helpers and the shared
// useFeatures hook (loading, success, failure means "all on", reload).
import { describe, test, expect, vi, beforeEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';

vi.mock('./mddb-client', () => ({ default: { listFeatures: vi.fn() } }));

const { default: mddbClient } = await import('./mddb-client');
const {
  FEATURE_VIEWS, featureLabel, featureForView, isFeatureEnabled, isViewEnabled,
  disabledFeatureForView, filterMenuItems, toFeatureMap, loadFeatures, resetFeatures, useFeatures,
} = await import('./features');

const geo = { name: 'geo', enabled: false, configured: false, default: true };
const chat = { name: 'chat', enabled: true };

beforeEach(() => resetFeatures());

describe('the map', () => {
  test('lists every switchable module, core pages belong to none', () => {
    expect(Object.keys(FEATURE_VIEWS)).toEqual([
      'chat', 'chatExternalMcp', 'geo', 'temporal', 'spell', 'curation', 'automation',
      'memory', 'codeIntel', 'eval', 'corpus', 'classify', 'duplicates', 'images',
      'webhooks', 'schemas', 'aliases', 'crossSearch', 'searchTuning', 'ftsTuning',
    ]);
    expect(featureForView('vectorSpace')).toBe('searchTuning');
    expect(featureForView('stopwords')).toBe('ftsTuning');
    expect(featureForView('aliases')).toBe('aliases');
    expect(featureForView('spellcheck')).toBe('spell');
    expect(featureForView('codeSymbols')).toBe('codeIntel');
    expect(featureForView('memory')).toBe('memory');
    expect(featureForView('duplicates')).toBe('duplicates');
    expect(featureForView('documents')).toBeNull();
  });

  test('labels fall back to the name', () => {
    expect(featureLabel('codeIntel')).toBe('Code intelligence');
    expect(featureLabel('newThing')).toBe('newThing');
  });
});

describe('helpers', () => {
  const features = { geo: false, chat: true };

  test('only an explicit false switches a module off', () => {
    expect(isFeatureEnabled('geo', features)).toBe(false);
    expect(isFeatureEnabled('chat', features)).toBe(true);
    expect(isFeatureEnabled('temporal', features)).toBe(true);
    expect(isFeatureEnabled('geo')).toBe(true);
  });

  test('pages follow their module; core pages always show', () => {
    expect(isViewEnabled('geo', features)).toBe(false);
    expect(isViewEnabled('chat', features)).toBe(true);
    expect(isViewEnabled('system', features)).toBe(true);
    expect(disabledFeatureForView('geo', features)).toBe('geo');
    expect(disabledFeatureForView('system', features)).toBeNull();
  });

  test('filterMenuItems drops the pages of modules that are off', () => {
    const items = [{ mode: 'system' }, { mode: 'geo' }, { mode: 'chat' }];
    expect(filterMenuItems(items, features)).toEqual([{ mode: 'system' }, { mode: 'chat' }]);
    expect(filterMenuItems(['geo', 'eval'], features, (m) => m)).toEqual(['eval']);
  });

  test('toFeatureMap keeps a missing enabled as on', () => {
    expect(toFeatureMap([geo, chat, { name: 'eval' }])).toEqual({ geo: false, chat: true, eval: true });
    expect(toFeatureMap()).toEqual({});
  });
});

describe('useFeatures', () => {
  test('everything is on while loading, then the server answer applies', async () => {
    let answer;
    vi.mocked(mddbClient.listFeatures).mockReturnValue(new Promise((r) => { answer = r; }));
    const { result } = renderHook(() => useFeatures());
    expect(result.current.loading).toBe(true);
    expect(isViewEnabled('geo', result.current.features)).toBe(true);
    await act(async () => answer({ features: [geo, chat], version: 3 }));
    expect(result.current).toMatchObject({ loading: false, version: 3, error: null, features: { geo: false, chat: true } });
    expect(result.current.list).toEqual([geo, chat]);
  });

  test('a failed load leaves every module on and keeps the error', async () => {
    const err = Object.assign(new Error('API Error (404): not found'), { status: 404 });
    vi.mocked(mddbClient.listFeatures).mockRejectedValue(err);
    const { result } = renderHook(() => useFeatures());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe(err);
    expect(result.current.features).toEqual({});
    expect(isViewEnabled('geo', result.current.features)).toBe(true);
  });

  test('an empty answer is an empty list', async () => {
    vi.mocked(mddbClient.listFeatures).mockResolvedValue(null);
    const { result } = renderHook(() => useFeatures());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current).toMatchObject({ list: [], version: 0, features: {} });
  });

  test('components share one load, and reload fetches again for all of them', async () => {
    vi.mocked(mddbClient.listFeatures).mockResolvedValue({ features: [chat], version: 1 });
    const first = renderHook(() => useFeatures());
    const second = renderHook(() => useFeatures());
    await waitFor(() => expect(first.result.current.version).toBe(1));
    expect(mddbClient.listFeatures).toHaveBeenCalledTimes(1);

    vi.mocked(mddbClient.listFeatures).mockResolvedValue({ features: [geo], version: 2 });
    await act(() => first.result.current.reload());
    expect(second.result.current.features).toEqual({ geo: false });
    expect(mddbClient.listFeatures).toHaveBeenCalledTimes(2);

    // A third component mounted after the load does not fetch again.
    renderHook(() => useFeatures());
    expect(mddbClient.listFeatures).toHaveBeenCalledTimes(2);
  });

  test('a client without listFeatures counts as a failed load, not a crash', async () => {
    const original = mddbClient.listFeatures;
    mddbClient.listFeatures = undefined;
    try {
      await loadFeatures();
      const { result } = renderHook(() => useFeatures());
      expect(result.current.error).toBeInstanceOf(TypeError);
      expect(result.current.features).toEqual({});
    } finally {
      mddbClient.listFeatures = original;
    }
  });

  test('concurrent loads share the request in flight', async () => {
    vi.mocked(mddbClient.listFeatures).mockResolvedValue({ features: [], version: 1 });
    const a = loadFeatures();
    expect(loadFeatures()).toBe(a);
    await a;
    expect(mddbClient.listFeatures).toHaveBeenCalledTimes(1);
  });
});
