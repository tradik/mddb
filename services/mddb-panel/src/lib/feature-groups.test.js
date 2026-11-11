// feature-groups (2.19.0): the Features page's categories and the plan for
// "Turn on all" / "Turn off all".
import { describe, test, expect } from 'vitest';
import { FEATURE_GROUPS, groupFeatures, bulkTargets } from './feature-groups';
import { FEATURE_VIEWS } from './features';

const f = (name, extra = {}) => ({ name, enabled: true, configured: true, requires: [], ...extra });

describe('groupFeatures', () => {
  test('every module the panel knows has exactly one category', () => {
    const listed = FEATURE_GROUPS.flatMap((group) => group.features);
    expect(new Set(listed).size).toBe(listed.length);
    expect([...listed].sort()).toEqual(Object.keys(FEATURE_VIEWS).sort());
  });

  test('splits the list by category in display order, keeps an unknown module under Other, drops empty ones', () => {
    const groups = groupFeatures([f('webhooks'), f('geo'), f('chat'), f('futureThing'), f('crossSearch')]);
    expect(groups.map((group) => group.label)).toEqual(['Search', 'Chat', 'Automation & Integrations', 'Other']);
    expect(groups[0].features.map((feature) => feature.name)).toEqual(['crossSearch', 'geo']);
    expect(groups[3].features.map((feature) => feature.name)).toEqual(['futureThing']);
  });

  test('an empty or missing list has no categories', () => {
    expect(groupFeatures([])).toEqual([]);
    expect(groupFeatures()).toEqual([]);
  });
});

describe('bulkTargets', () => {
  const chat = f('chat', { enabled: false, configured: false });
  const external = f('chatExternalMcp', { enabled: false, configured: false, requires: ['chat'] });
  const geo = f('geo');
  const temporal = f('temporal', { enabled: false, configured: true, lockedBy: 'MDDB_FEATURES_FORCE_OFF' });

  test('turning on: a requirement before what needs it; modules already on and locked ones are left', () => {
    expect(bulkTargets([external, geo, chat, temporal], true)).toEqual({
      names: ['chat', 'chatExternalMcp'],
      locked: ['temporal'],
    });
  });

  test('turning off: what needs a module before the module itself', () => {
    const on = [f('chat'), f('chatExternalMcp', { requires: ['chat'] }), geo];
    expect(bulkTargets(on, false).names).toEqual(['chatExternalMcp', 'geo', 'chat']);
  });

  test('the stored switch counts when the server reports one; otherwise the state', () => {
    const blocked = f('chatExternalMcp', { enabled: false, configured: true, requires: ['chat'] });
    expect(bulkTargets([blocked], true).names).toEqual([]);
    expect(bulkTargets([{ name: 'x', enabled: false }], true).names).toEqual(['x']);
  });

  test('nothing to do on an empty or missing list', () => {
    expect(bulkTargets([], false)).toEqual({ names: [], locked: [] });
    expect(bulkTargets(undefined, true)).toEqual({ names: [], locked: [] });
  });
});
