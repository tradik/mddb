// mcp-key-auth (#445): whether the server checks MCP API keys, as /v1/config
// reports it, with "not reported" kept apart from "off".
import { describe, test, expect } from 'vitest';
import { mcpKeyAuthEnabled, mcpKeyAuthOff } from './mcp-key-auth';

const withKeys = (apiKeyEnabled) => ({ protocols: { mcp: { apiKeyEnabled } } });

describe('mcp-key-auth', () => {
  test('a reported setting is returned as given', () => {
    expect(mcpKeyAuthEnabled(withKeys(true))).toBe(true);
    expect(mcpKeyAuthEnabled(withKeys(false))).toBe(false);
    expect(mcpKeyAuthOff(withKeys(false))).toBe(true);
    expect(mcpKeyAuthOff(withKeys(true))).toBe(false);
  });

  test('no config, an older server or a non-boolean is "not reported", never off', () => {
    for (const config of [null, undefined, {}, { protocols: {} }, { protocols: { mcp: {} } }, withKeys('false')]) {
      expect(mcpKeyAuthEnabled(config)).toBeUndefined();
      expect(mcpKeyAuthOff(config)).toBe(false);
    }
  });
});
