// The scope of an MCP API key (#439): what a new key sends, and how a
// listed key's scope reads.
import { describe, test, expect } from 'vitest';
import { emptyScope, isLimited, scopeBody, scopeLabel } from './mcp-key-scope';

describe('scopeBody', () => {
  test('leaves out what is empty, so an unscoped key sends nothing new', () => {
    expect(scopeBody(emptyScope)).toEqual({});
    expect(scopeBody()).toEqual({});
    expect(scopeBody({ collections: ['a'], readOnly: true, scenario: 'support' }))
      .toEqual({ collections: ['a'], readOnly: true, scenario: 'support' });
  });

  test('AGT-007: read-only collections among the collections, not on a read-only key', () => {
    expect(scopeBody({ collections: ['a', 'b'], readOnlyCollections: ['b', 'gone'] }))
      .toEqual({ collections: ['a', 'b'], readOnlyCollections: ['b'] });
    expect(scopeBody({ collections: ['a'], readOnlyCollections: ['gone'] })).toEqual({ collections: ['a'] });
    expect(scopeBody({ collections: ['a'], readOnly: true, readOnlyCollections: ['a'] }))
      .toEqual({ collections: ['a'], readOnly: true });
  });

  test('AGT-008: a memory namespace, trimmed, only for a limited key', () => {
    expect(scopeBody({ collections: ['a'], memoryNamespace: ' shop ' })).toEqual({ collections: ['a'], memoryNamespace: 'shop' });
    expect(scopeBody({ scenario: 's', memoryNamespace: 'shop' })).toEqual({ scenario: 's', memoryNamespace: 'shop' });
    expect(scopeBody({ readOnly: true, memoryNamespace: 'shop' })).toEqual({ readOnly: true });
    expect(scopeBody({ collections: ['a'], memoryNamespace: '   ' })).toEqual({ collections: ['a'] });
  });
});

describe('isLimited', () => {
  test('collections or a scenario limit a key', () => {
    expect(isLimited()).toBe(false);
    expect(isLimited({ readOnly: true })).toBe(false);
    expect(isLimited({ collections: ['a'] })).toBe(true);
    expect(isLimited({ scenario: 's' })).toBe(true);
  });
});

describe('scopeLabel', () => {
  test('names the scenario, the collections and read-only', () => {
    expect(scopeLabel({})).toBe('all collections');
    expect(scopeLabel({ collections: [] })).toBe('all collections');
    expect(scopeLabel({ readOnly: true })).toBe('read-only');
    expect(scopeLabel({ collections: ['a', 'b'] })).toBe('a, b');
    expect(scopeLabel({ scenario: 's', collections: ['a'] })).toBe('scenario s · a · read-only');
    expect(scopeLabel({ collections: ['a', 'b'], readOnlyCollections: ['b'], memoryNamespace: 'shop' }))
      .toBe('a, b · read-only b · memory shop');
    expect(scopeLabel({ collections: ['a'], readOnly: true, readOnlyCollections: ['a'] })).toBe('a · read-only');
  });
});
