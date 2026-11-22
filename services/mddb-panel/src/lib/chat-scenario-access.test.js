// #441: the logic of the scenario's Access card.
import { describe, test, expect } from 'vitest';
import { accessLinks, viaLabel, accessLabel, throughLabel, sortReaders, sameCollections } from './chat-scenario-access';

describe('accessLinks', () => {
  test('auth on: every link; auth off: only the LLM Connections ones', () => {
    expect(accessLinks({ features: {}, authEnabled: true }).map((l) => l.label)).toEqual([
      'Connect an MCP client', 'MCP API keys', 'Users and permissions', 'Groups', 'My API keys',
    ]);
    expect(accessLinks({ features: {}, authEnabled: false }).map((l) => `${l.mode}/${l.target}`)).toEqual(['mcp/null', 'mcp/api-keys']);
  });
});

describe('labels', () => {
  test.each([
    ['admin', 'admin'], ['user', 'own grant'], ['user:*', 'own grant on "*"'],
    ['group:readers', 'group readers'], ['group:readers:*', 'group readers on "*"'], ['other', 'other'], [undefined, undefined],
  ])('via %s', (via, want) => expect(viaLabel(via)).toBe(want));

  test('access and through', () => {
    expect(accessLabel({ full: true, grants: [] }, 0)).toBe('All collections');
    expect(accessLabel({ full: false, grants: [{}] }, 2)).toBe('1 of 2 collections');
    expect(accessLabel({ full: false }, 2)).toBe('None');
    expect(throughLabel({ grants: [{ via: 'user' }, { via: 'user' }, { via: 'group:g' }] })).toBe('own grant, group g');
    expect(throughLabel({})).toBe('');
  });
});

describe('sortReaders and sameCollections', () => {
  test('full first, then by name; the input is not changed', () => {
    const users = [{ username: 'b', full: false }, { username: 'c', full: true }, { username: 'a', full: false }];
    expect(sortReaders(users).map((u) => u.username)).toEqual(['c', 'a', 'b']);
    expect(users[0].username).toBe('b');
    expect(sortReaders()).toEqual([]);
  });

  test('order aside', () => {
    expect(sameCollections(['a', 'b'], ['b', 'a'])).toBe(true);
    expect(sameCollections(['a'], ['a', 'b'])).toBe(false);
    expect(sameCollections(['a', 'c'], ['a', 'b'])).toBe(false);
    expect(sameCollections()).toBe(true);
  });
});
