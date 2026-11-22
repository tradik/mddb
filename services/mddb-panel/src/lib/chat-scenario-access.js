import { visibleGroups } from './nav-groups';

// #441: the logic of the scenario's Access card (ChatScenarioAccess.jsx).

/** The screens that decide who can use a scenario, in the order shown. */
export const ACCESS_LINKS = [
  { label: 'Connect an MCP client', mode: 'mcp', target: null },
  { label: 'MCP API keys', mode: 'mcp', target: 'api-keys' },
  { label: 'Users and permissions', mode: 'users', target: null },
  { label: 'Groups', mode: 'groups', target: null },
  { label: 'My API keys', mode: 'account', target: null },
];

/**
 * The links whose view the sidebar shows for `context` ({ features,
 * authEnabled, clusterEnabled, automationsEnabled }): the sidebar's own rule,
 * visibleGroups, not a copy of its conditions.
 */
export function accessLinks(context, links = ACCESS_LINKS) {
  const shown = new Set(visibleGroups(context).flatMap((g) => g.items.map((item) => item.mode)));
  return links.filter((link) => shown.has(link.mode));
}

/** A `via` of the server written out: admin, own grant, group readers on "*". */
export function viaLabel(via) {
  if (via === 'admin') return 'admin';
  if (via === 'user') return 'own grant';
  if (via === 'user:*') return 'own grant on "*"';
  const group = /^group:(.+?)(:\*)?$/.exec(via || '');
  if (group) return group[2] ? `group ${group[1]} on "*"` : `group ${group[1]}`;
  return via;
}

/** The Access column: All collections, n of m collections, or None. */
export function accessLabel(user, total) {
  if (user.full) return 'All collections';
  const n = user.grants?.length || 0;
  return n === 0 ? 'None' : `${n} of ${total} collections`;
}

/** The Through column: each distinct rule once, in the order met. */
export function throughLabel(user) {
  return [...new Set((user.grants || []).map((g) => viaLabel(g.via)))].join(', ');
}

/** Users who can read the whole scenario first, then by name. */
export function sortReaders(users = []) {
  return [...users].sort((a, b) => Number(b.full) - Number(a.full) || a.username.localeCompare(b.username));
}

/** Whether two collection lists hold the same names, order aside. */
export function sameCollections(a = [], b = []) {
  const left = [...a].sort();
  const right = [...b].sort();
  return left.length === right.length && left.every((name, i) => name === right[i]);
}
