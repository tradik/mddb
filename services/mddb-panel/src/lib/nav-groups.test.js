// nav-groups (2.19.0): the menu data and the pure helpers that pick what the
// sidebar shows — conditions, module switches, the search and group lookup.
import { describe, test, expect } from 'vitest';
import { NAV_GROUPS, foldText, isEntryAvailable, visibleGroups, groupOfView, navConditions } from './nav-groups';

const everything = { features: {}, authEnabled: true, clusterEnabled: true, automationsEnabled: true };
const plain = { features: {}, authEnabled: false, clusterEnabled: false, automationsEnabled: true };
const modes = (groups) => groups.flatMap((g) => g.items.map((i) => i.mode));

describe('the menu data', () => {
  test('six groups in order, every page once, each with an icon and a label', () => {
    expect(NAV_GROUPS.map((g) => g.label)).toEqual(['Search', 'Data', 'Chat', 'Integrations', 'Security', 'System']);
    const all = modes(NAV_GROUPS);
    expect(all).toHaveLength(35);
    expect(new Set(all).size).toBe(all.length);
    for (const item of NAV_GROUPS.flatMap((g) => g.items)) {
      expect(item.icon, item.mode).toBeTruthy();
      expect(item.label, item.mode).toBeTruthy();
    }
  });
});

describe('helpers', () => {
  test('foldText drops case and diacritics', () => {
    expect(foldText('Źródła Łódź')).toBe('zrodła łodz');
    expect(foldText('Stop Words')).toBe('stop words');
  });

  test('an entry needs its condition and its module', () => {
    expect(isEntryAvailable({ mode: 'users', when: 'auth' }, plain)).toBe(false);
    expect(isEntryAvailable({ mode: 'users', when: 'auth' }, everything)).toBe(true);
    expect(isEntryAvailable({ mode: 'cluster', when: 'cluster' }, plain)).toBe(false);
    expect(isEntryAvailable({ mode: 'automation', when: 'automations' }, { ...plain, automationsEnabled: false })).toBe(false);
    expect(isEntryAvailable({ mode: 'geo' }, { ...plain, features: { geo: false } })).toBe(false);
    expect(isEntryAvailable({ mode: 'system' }, plain)).toBe(true);
  });

  test('visibleGroups lists the allowed entries; without auth or replication they are left out', () => {
    expect(modes(visibleGroups(everything))).toHaveLength(35);
    const shown = modes(visibleGroups(plain));
    expect(shown).not.toContain('users');
    expect(shown).not.toContain('cluster');
    expect(shown).toHaveLength(31);
  });

  test('the search matches labels, case and diacritics aside, and drops empty groups', () => {
    expect(visibleGroups(everything, '  stop ').map((g) => g.id)).toEqual(['search']);
    expect(modes(visibleGroups(everything, 'SEARCH'))).toEqual(['vectors', 'crossSearch', 'geo', 'searchAdvisor']);
    expect(modes(visibleGroups(everything, 'ÉNCRŸPTIÓN'.replace('Ÿ', 'Y')))).toEqual(['encryption']);
    expect(visibleGroups(everything, 'nothing like this')).toEqual([]);
  });

  test('a group whose modules are all off disappears', () => {
    const features = { chat: false, memory: false };
    const ids = visibleGroups({ ...plain, features }).map((g) => g.id);
    expect(ids).toContain('chat'); // LLM Connections is core
    const noChat = visibleGroups({ ...plain, features }, '', [{ id: 'x', label: 'X', items: [{ mode: 'chat', label: 'Chat' }] }]);
    expect(noChat).toEqual([]);
  });

  test('groupOfView finds the group of a page', () => {
    expect(groupOfView('stopwords')).toBe('search');
    expect(groupOfView('cluster')).toBe('system');
    expect(groupOfView('documents')).toBeNull();
  });
});

describe('navConditions', () => {
  test('reads the conditions from /v1/config; an unreported setting hides only auth and cluster pages', () => {
    expect(navConditions(undefined)).toEqual({ authEnabled: false, clusterEnabled: false, automationsEnabled: true, mcpEnabled: true });
    expect(navConditions({ authEnabled: true, replicationRole: 'leader', automationsEnabled: false, protocols: { mcp: { enabled: false } } }))
      .toEqual({ authEnabled: true, clusterEnabled: true, automationsEnabled: false, mcpEnabled: false });
  });

  test('MCP off hides LLM Connections; an omitted mcpEnabled or automationsEnabled counts as on', () => {
    const mcp = { mode: 'mcp', when: 'mcp' };
    expect(isEntryAvailable(mcp, { ...plain, mcpEnabled: false })).toBe(false);
    expect(isEntryAvailable(mcp, { features: {} })).toBe(true);
    expect(isEntryAvailable({ mode: 'automation', when: 'automations' }, { features: {} })).toBe(true);
    const chat = visibleGroups({ ...plain, mcpEnabled: false }).find((g) => g.id === 'chat');
    expect(chat.items.map((i) => i.mode)).not.toContain('mcp');
  });
});
