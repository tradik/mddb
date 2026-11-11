// chat-scenario: the editor's flat form state and its conversion to and from
// the scenario object of /v1/chat/scenarios/{name} (#403).
import { describe, test, expect } from 'vitest';
import { toForm, fromForm, formProblem, parseExtraValue, offTopicModes, emojiPolicies, verbosityLevels } from './chat-scenario';

const stored = {
  name: 'support',
  description: 'Support desk',
  enabled: false,
  operatorPrompt: 'You are Ada.',
  guardrails: 'No legal advice.',
  offTopic: { mode: 'redirect', redirectTemplate: 'Ask me about MDDB.' },
  allowedCollections: ['docs', 'runbooks'],
  skills: ['triage', 'refunds'],
  style: { emoji: 'sparingly', verbosity: 'short', markdown: false, citationTemplate: '[{{key}}]' },
  llm: { model: 'gpt-x', temperature: 0, maxTokens: 800, extraBody: { reasoning_effort: 'low', top_k: 40, nested: { a: 1 } } },
  maxTurns: 12,
  tokenBudgetPerSession: 50000,
  rateLimit: { messagesPerMinute: 20 },
  createdAt: '2026-11-10T12:00:00Z',
  updatedAt: '2026-11-10T12:00:00Z',
};

describe('toForm', () => {
  test('a new scenario starts enabled, refusing off-topic questions, in Markdown', () => {
    expect(toForm()).toEqual({
      name: '', description: '', enabled: true, operatorPrompt: '', guardrails: '',
      offTopicMode: 'refuse', redirectTemplate: '', allowedCollections: [], skills: [],
      emoji: 'off', verbosity: 'normal', markdown: true, citationTemplate: '',
      model: '', temperature: '', maxTokens: '', extraBody: [],
      maxTurns: '', tokenBudgetPerSession: '', messagesPerMinute: '', mcpServers: [],
    });
  });

  test('a stored scenario fills every field; temperature 0 stays 0', () => {
    const form = toForm(stored);
    expect(form).toMatchObject({
      name: 'support', enabled: false, offTopicMode: 'redirect', redirectTemplate: 'Ask me about MDDB.',
      skills: ['triage', 'refunds'], emoji: 'sparingly', verbosity: 'short', markdown: false,
      temperature: '0', maxTokens: '800', maxTurns: '12', tokenBudgetPerSession: '50000', messagesPerMinute: '20',
    });
    expect(form.extraBody).toEqual([
      { key: 'reasoning_effort', value: 'low' },
      { key: 'top_k', value: '40' },
      { key: 'nested', value: '{"a":1}' },
    ]);
  });
});

describe('fromForm', () => {
  test('a stored scenario survives the round trip, without the server fields', () => {
    const { name: _name, createdAt: _created, updatedAt: _updated, ...body } = stored;
    expect(fromForm(toForm(stored))).toEqual(body);
  });

  test('empty numbers are 0, an empty temperature is left out, blank extras are dropped, skills keep their order', () => {
    const form = { ...toForm(), model: ' m ', skills: ['b', 'a'], extraBody: [{ key: ' ', value: 'x' }, { key: ' on ', value: 'true' }], maxTurns: 'many' };
    const body = fromForm(form);
    expect(body.llm).toEqual({ model: 'm', maxTokens: 0, extraBody: { on: true } });
    expect(body.skills).toEqual(['b', 'a']);
    expect(body.maxTurns).toBe(0);
    expect(fromForm(toForm()).llm).toEqual({ model: '', maxTokens: 0 });
  });

  test('MCP servers (MCP-005) are sent only when there are some', () => {
    expect('mcpServers' in fromForm(toForm())).toBe(false);
    const withServer = toForm({ mcpServers: [{ name: 'wiki', url: 'https://w', enabled: true, allowedTools: ['search'] }] });
    expect(fromForm(withServer).mcpServers).toEqual([
      { name: 'wiki', url: 'https://w', enabled: true, secretRef: '', allowedTools: ['search'], timeoutMs: 0, resultMaxChars: 0 },
    ]);
    expect(formProblem({ ...withServer, name: 'ok', mcpServers: [{ ...withServer.mcpServers[0], url: 'http://w' }] })).toMatch(/https/);
  });
});

describe('parseExtraValue', () => {
  test.each([
    ['low', 'low'],
    ['40', 40],
    ['true', true],
    ['{"a":1}', { a: 1 }],
    ['', ''],
  ])('%j reads as %j', (text, value) => {
    expect(parseExtraValue(text)).toEqual(value);
  });
});

describe('formProblem', () => {
  test.each([
    [{ name: ' ' }, 'Give the scenario a name.'],
    [{ name: 'a b' }, 'The name may not contain spaces, "/" or "|", or be "version".'],
    [{ name: 'a/b' }, 'The name may not contain spaces, "/" or "|", or be "version".'],
    [{ name: 'version' }, 'The name may not contain spaces, "/" or "|", or be "version".'],
    [{ name: 'ok', temperature: '2.5' }, 'Temperature must be between 0 and 2.'],
    [{ name: 'ok', temperature: '-1' }, 'Temperature must be between 0 and 2.'],
    [{ name: 'ok', temperature: 'warm' }, 'Temperature must be between 0 and 2.'],
    [{ name: 'ok', temperature: '0' }, null],
    [{ name: 'ok' }, null],
  ])('%j → %j', (fields, problem) => {
    expect(formProblem({ ...toForm(), ...fields })).toBe(problem);
  });
});

test('the choices match the server enums', () => {
  expect(offTopicModes.map((o) => o.value)).toEqual(['refuse', 'redirect', 'brief']);
  expect(emojiPolicies.map((o) => o.value)).toEqual(['off', 'sparingly', 'free']);
  expect(verbosityLevels.map((o) => o.value)).toEqual(['short', 'normal', 'long']);
});
