// MCP-005: the MCP server rows of the scenario editor.
import { describe, test, expect } from 'vitest';
import {
  newMcpServer, toMcpRows, fromMcpRows, mcpServerProblem, toolHints, toolInactiveReason, isFeatureOff, mcpFeature,
} from './chat-mcp';

describe('rows', () => {
  test('a stored server round-trips; defaults stay unset', () => {
    const stored = [{ name: 'wiki', url: 'https://w/mcp', enabled: true, secretRef: 'k', allowedTools: ['search'], timeoutMs: 500, resultMaxChars: 900 }];
    const rows = toMcpRows(stored);
    expect(rows[0]).toEqual({ uid: expect.any(Number), name: 'wiki', url: 'https://w/mcp', enabled: true, secretRef: 'k', allowedTools: ['search'], timeoutMs: '500', resultMaxChars: '900' });
    expect(fromMcpRows(rows)).toEqual(stored);
    expect(toMcpRows([{}])[0]).toEqual({ ...newMcpServer(), uid: expect.any(Number), enabled: false });
    expect(newMcpServer().uid).not.toBe(newMcpServer().uid);
    expect(toMcpRows()).toEqual([]);
    expect(fromMcpRows([{ ...newMcpServer(), name: ' a ', url: ' https://a ' }])[0]).toMatchObject({ name: 'a', url: 'https://a', timeoutMs: 0, resultMaxChars: 0 });
  });

  test('problems', () => {
    const row = (name, url) => ({ ...newMcpServer(), name, url });
    expect(mcpServerProblem([])).toBeNull();
    expect(mcpServerProblem([row('wiki', 'https://w.example.com/mcp')])).toBeNull();
    expect(mcpServerProblem([row('', 'https://w')])).toMatch(/needs a name/);
    expect(mcpServerProblem([row('a b', 'https://w')])).toMatch(/needs a name/);
    expect(mcpServerProblem([row('a', 'https://w'), row('a', 'https://x')])).toMatch(/used twice/);
    expect(mcpServerProblem([row('a', 'http://w')])).toMatch(/https:\/\//);
    expect(mcpServerProblem([row('a', 'https://u:p@w')])).toMatch(/without a user/);
  });
});

describe('tools', () => {
  test('hints', () => {
    expect(toolHints({ annotations: { readOnlyHint: true, destructiveHint: true } })).toEqual({ readOnly: true, destructive: true });
    expect(toolHints({})).toEqual({ readOnly: false, destructive: false });
  });

  test('the first switch that is off', () => {
    const on = { featureOff: false, scenarioEnabled: true, serverEnabled: true, allowed: true };
    expect(toolInactiveReason(on)).toBeNull();
    expect(toolInactiveReason({ ...on, allowed: false })).toBe('Not allowed');
    expect(toolInactiveReason({ ...on, serverEnabled: false, allowed: false })).toBe('Server is disabled');
    expect(toolInactiveReason({ ...on, scenarioEnabled: false, serverEnabled: false })).toBe('Scenario is disabled');
    expect(toolInactiveReason({ ...on, featureOff: true, scenarioEnabled: false })).toBe(`Feature ${mcpFeature} is off`);
  });

  test('a feature-off answer', () => {
    expect(isFeatureOff({ status: 404, body: { error: 'feature disabled', feature: mcpFeature } })).toBe(true);
    expect(isFeatureOff({ status: 404, body: { error: 'mcp secret not found' } })).toBe(false);
    expect(isFeatureOff(undefined)).toBe(false);
  });
});
