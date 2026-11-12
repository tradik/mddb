// #414: the "Forward the user's identity" fields of an MCP server row.
import { describe, test, expect } from 'vitest';
import { identityRowFields, identityFromRow, identityProblem, identityHeaderDefault, identityTtlDefault } from './chat-mcp-identity';
import { toMcpRows, fromMcpRows, mcpServerProblem, newMcpServer } from './chat-mcp';

const on = (extra = {}) => ({ ...newMcpServer(), name: 'acct', url: 'https://a', secretRef: 'acct-service', forwardIdentity: true,
  identitySecretRef: 'acct-identity', ...extra });

describe('identity fields', () => {
  test('off by default, and nothing is sent while off', () => {
    expect(identityRowFields()).toEqual({ forwardIdentity: false, identitySecretRef: '', identityHeader: '', identityTtl: '' });
    expect(identityFromRow(newMcpServer())).toEqual({});
    expect(fromMcpRows([{ ...newMcpServer(), name: 'a', url: 'https://a' }])[0]).not.toHaveProperty('forwardIdentity');
  });

  test('a stored setting round-trips; defaults stay unset', () => {
    const stored = [{ name: 'acct', url: 'https://a', enabled: true, secretRef: 's', allowedTools: [], timeoutMs: 0, resultMaxChars: 0,
      forwardIdentity: { secretRef: 'k', header: 'X-User', ttlSeconds: 60 } }];
    const rows = toMcpRows(stored);
    expect(rows[0]).toMatchObject({ forwardIdentity: true, identitySecretRef: 'k', identityHeader: 'X-User', identityTtl: '60' });
    expect(fromMcpRows(rows)).toEqual(stored);
    expect(identityRowFields({ forwardIdentity: {} })).toEqual({ forwardIdentity: true, identitySecretRef: '', identityHeader: '', identityTtl: '' });
    expect(identityFromRow(on({ identityHeader: ' ', identityTtl: '' }))).toEqual({ forwardIdentity: { secretRef: 'acct-identity', header: '', ttlSeconds: 0 } });
  });

  test('the documented defaults', () => {
    expect(identityHeaderDefault).toBe('X-MDDB-Chat-Identity');
    expect(identityTtlDefault).toBe(120);
  });
});

describe('identity problems', () => {
  test('a key, other than the credential; a plain header; a lifetime of 10 to 300 s', () => {
    expect(identityProblem(newMcpServer(), 'acct')).toBeNull();
    expect(identityProblem(on(), 'acct')).toBeNull();
    expect(identityProblem(on({ identityHeader: 'X-User', identityTtl: '300' }), 'acct')).toBeNull();
    expect(identityProblem(on({ identitySecretRef: '' }), 'acct')).toMatch(/needs a key secret/);
    expect(identityProblem(on({ identitySecretRef: 'acct-service' }), 'acct')).toMatch(/other than its credential/);
    expect(identityProblem(on({ identityHeader: 'X User' }), 'acct')).toMatch(/letters, digits/);
    for (const ttl of ['5', '301', '1.5', 'x']) {
      expect(identityProblem(on({ identityTtl: ttl }), 'acct')).toMatch(/10 to 300 seconds/);
    }
  });

  test('the editor refuses to save a row with a problem', () => {
    expect(mcpServerProblem([on({ identitySecretRef: '' })])).toMatch(/needs a key secret/);
    expect(mcpServerProblem([on()])).toBeNull();
  });
});
