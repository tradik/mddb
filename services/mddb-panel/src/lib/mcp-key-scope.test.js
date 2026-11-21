// The scope of an MCP API key (#439): what a new key sends, and how a
// listed key's scope reads.
import { describe, test, expect } from 'vitest';
import { emptyScope, scopeBody, scopeLabel } from './mcp-key-scope';

describe('scopeBody', () => {
  test('leaves out what is empty, so an unscoped key sends nothing new', () => {
    expect(scopeBody(emptyScope)).toEqual({});
    expect(scopeBody()).toEqual({});
    expect(scopeBody({ collections: ['a'], readOnly: true, scenario: 'support' }))
      .toEqual({ collections: ['a'], readOnly: true, scenario: 'support' });
  });
});

describe('scopeLabel', () => {
  test('names the scenario, the collections and read-only', () => {
    expect(scopeLabel({})).toBe('all collections');
    expect(scopeLabel({ collections: [] })).toBe('all collections');
    expect(scopeLabel({ readOnly: true })).toBe('read-only');
    expect(scopeLabel({ collections: ['a', 'b'] })).toBe('a, b');
    expect(scopeLabel({ scenario: 's', collections: ['a'] })).toBe('scenario s · a · read-only');
  });
});
