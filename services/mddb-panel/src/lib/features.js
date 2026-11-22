/**
 * GO-049 (2.19.0): which server module each panel page belongs to, and the
 * module switches as the server reports them (GET /v1/features).
 *
 * A module that is off hides its pages from the menu, and a direct visit to
 * one shows FeatureDisabledNotice instead. Until the list has loaded, and
 * when it cannot be loaded at all (an older server without /v1/features, a
 * network error), every module counts as on, so the panel behaves as before.
 */
import { useEffect, useSyncExternalStore } from 'react';
import mddbClient from './mddb-client';

/** Module name → the panel viewModes it owns. Core pages are not listed. */
export const FEATURE_VIEWS = {
  chat: ['chat'],
  chatExternalMcp: [],
  geo: ['geo'],
  temporal: ['temporal'],
  spell: ['spellcheck'],
  curation: ['curation'],
  automation: ['automation'],
  memory: ['memory'],
  codeIntel: ['codeSymbols'],
  eval: ['eval'],
  corpus: ['corpus'],
  classify: [],
  duplicates: ['duplicates'],
  images: [],
  webhooks: ['webhooks'],
  schemas: ['schemas'],
  aliases: ['aliases'],
  crossSearch: ['crossSearch'],
  searchTuning: ['searchAdvisor', 'vectorSpace'],
  ftsTuning: ['synonyms', 'stopwords'],
  // Endpoints for other tools to call; no panel page.
  webSearch: [],
  rerankApi: [],
  extractApi: [],
  vectorStores: [],
  esCompat: [],
  qdrantCompat: [],
};

/**
 * Plain names of the modules, the same as the server's `title` (2.19.11).
 * The server's own title wins once the list has loaded; these cover an older
 * server that sends none, and the moment before the list arrives.
 */
export const FEATURE_LABELS = {
  chat: 'Chat assistants',
  chatExternalMcp: 'Tools from other MCP servers',
  geo: 'Geo search',
  temporal: 'Activity over time',
  spell: 'Spell checker',
  curation: 'Pinned and hidden results',
  automation: 'Automation',
  memory: 'Agent memory',
  codeIntel: 'Code intelligence',
  eval: 'Search quality checks',
  corpus: 'Corpus packs',
  classify: 'Classification',
  duplicates: 'Duplicate finder',
  images: 'Image documents',
  webhooks: 'Webhooks',
  schemas: 'Collection schemas',
  aliases: 'Collection aliases',
  crossSearch: 'Search across collections',
  searchTuning: 'Search tuning tools',
  ftsTuning: 'Synonyms and stop words',
  webSearch: 'Web search endpoint',
  rerankApi: 'Rerank endpoint',
  extractApi: 'File-to-Markdown endpoint',
  vectorStores: 'Vector store API',
  esCompat: 'Search API under /es',
  qdrantCompat: 'Vector query under /qdrant',
};

/** The plain name of a module: the server's title, ours, or the name itself. */
export function featureLabel(name) {
  return state.list.find((feature) => feature.name === name)?.title || FEATURE_LABELS[name] || name;
}

/** The module a viewMode belongs to, or null for a core page. */
export function featureForView(viewMode) {
  for (const [name, views] of Object.entries(FEATURE_VIEWS)) {
    if (views.includes(viewMode)) return name;
  }
  return null;
}

/** Whether a module is on. Unknown and not-yet-loaded modules count as on. */
export function isFeatureEnabled(name, features = {}) {
  return features[name] !== false;
}

/** Whether a page may be shown: core pages always, module pages when on. */
export function isViewEnabled(viewMode, features) {
  const name = featureForView(viewMode);
  return name === null || isFeatureEnabled(name, features);
}

/** The module that hides `viewMode`, or null when the page may be shown. */
export function disabledFeatureForView(viewMode, features) {
  return isViewEnabled(viewMode, features) ? null : featureForView(viewMode);
}

/** The menu entries whose page may be shown; `getView` reads an entry's viewMode. */
export function filterMenuItems(items, features, getView = (item) => item.mode) {
  return items.filter((item) => isViewEnabled(getView(item), features));
}

/** `[{ name, enabled }]` → `{ name: enabled }`. */
export function toFeatureMap(list = []) {
  return Object.fromEntries(list.map((f) => [f.name, f.enabled !== false]));
}

// One shared copy for every component: the menu, the page router and the
// Features page see the same answer, and a change made on the Features page
// reaches the menu at once.
const initialState = { list: [], features: {}, version: 0, loading: false, loaded: false, error: null };
let state = initialState;
let inflight = null;
const listeners = new Set();

function setState(patch) {
  state = { ...state, ...patch };
  listeners.forEach((listener) => listener());
}

function subscribe(listener) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

const snapshot = () => state;

/**
 * Loads GET /v1/features; concurrent calls share one request. A client
 * without listFeatures (a test double) counts as a failed load.
 */
export function loadFeatures() {
  if (inflight) return inflight;
  setState({ loading: true, error: null });
  inflight = Promise.resolve()
    .then(() => mddbClient.listFeatures())
    .then(
      (data) => {
        const list = data?.features || [];
        setState({ list, features: toFeatureMap(list), version: data?.version || 0 });
      },
      // On failure every module counts as on (backward compatible).
      (error) => setState({ list: [], features: {}, error }),
    )
    .finally(() => {
      inflight = null;
      setState({ loading: false, loaded: true });
    });
  return inflight;
}

/** Forgets the loaded list; for tests and after a sign-out. */
export function resetFeatures() {
  inflight = null;
  state = initialState;
  listeners.forEach((listener) => listener());
}

/**
 * The module switches: `{ features, list, version, loading, error, reload }`.
 * `features` maps a module name to whether it is on; the first component to
 * mount starts the load.
 */
export function useFeatures() {
  const current = useSyncExternalStore(subscribe, snapshot);
  useEffect(() => {
    if (!state.loaded && !inflight) loadFeatures();
  }, []);
  return { ...current, reload: loadFeatures };
}
