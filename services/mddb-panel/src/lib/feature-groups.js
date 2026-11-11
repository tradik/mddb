/**
 * The Features page's categories and its bulk switching (2.19.0).
 *
 * Categories follow the menu groups of nav-groups.js, so a module sits under
 * the same heading as its pages. A module the server reports but this file
 * does not know (a newer server) lands in "Other", never disappears.
 *
 * bulkTargets() plans "Turn on all" / "Turn off all": it skips modules forced
 * off by the operator and modules already in the wanted state, and orders the
 * rest so a requirement is switched on before what needs it (chat before
 * chatExternalMcp) and off after it.
 */

/** Category id, heading and the modules in it, in display order. */
export const FEATURE_GROUPS = [
  { id: 'search', label: 'Search', features: ['crossSearch', 'geo', 'searchTuning', 'eval', 'codeIntel', 'ftsTuning', 'curation', 'spell'] },
  { id: 'data', label: 'Data', features: ['aliases', 'schemas', 'duplicates', 'temporal', 'classify', 'images', 'corpus'] },
  { id: 'chat', label: 'Chat', features: ['chat', 'chatExternalMcp', 'memory'] },
  { id: 'integrations', label: 'Automation & Integrations', features: ['automation', 'webhooks'] },
];

const OTHER = { id: 'other', label: 'Other' };

/** The server's list split into the categories above; empty ones are dropped. */
export function groupFeatures(list = []) {
  const byName = new Map(list.map((feature) => [feature.name, feature]));
  const known = new Set(FEATURE_GROUPS.flatMap((group) => group.features));
  const groups = FEATURE_GROUPS.map((group) => ({
    id: group.id,
    label: group.label,
    features: group.features.filter((name) => byName.has(name)).map((name) => byName.get(name)),
  }));
  groups.push({ ...OTHER, features: list.filter((feature) => !known.has(feature.name)) });
  return groups.filter((group) => group.features.length > 0);
}

/** The switch as stored (what the toggle shows), falling back to the state. */
const storedState = (feature) => Boolean(feature.configured ?? feature.enabled);

/** How many of `names` the module needs, within the set being switched. */
const depth = (feature, names) => (feature.requires || []).filter((name) => names.has(name)).length;

/**
 * The modules to switch for "turn all on/off" over `features`, in a safe
 * order, plus the locked ones skipped (for the confirmation text).
 */
export function bulkTargets(features = [], enabled) {
  const locked = features.filter((feature) => feature.lockedBy).map((feature) => feature.name);
  const changing = features.filter((feature) => !feature.lockedBy && storedState(feature) !== enabled);
  const names = new Set(changing.map((feature) => feature.name));
  const ordered = [...changing].sort((a, b) => depth(a, names) - depth(b, names));
  if (!enabled) ordered.reverse();
  return { names: ordered.map((feature) => feature.name), locked };
}
