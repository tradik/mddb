/**
 * Chat scenarios (#403): the editor's flat form state and its conversion to
 * and from the scenario object of /v1/chat/scenarios/{name}.
 *
 * Numbers are held as the strings the inputs produce; an empty string means
 * "not set", which matters for temperature, where 0 is a real value.
 */

import { toMcpRows, fromMcpRows, mcpServerProblem } from './chat-mcp';
import { toLoopGuardForm, fromLoopGuardForm, loopGuardProblem } from './chat-loop-guard';

export const inputClass = 'w-full px-2 py-1.5 border border-gray-300 rounded text-sm text-gray-900 focus:outline-none focus:ring-2 focus:ring-primary-500';
export const labelClass = 'block text-xs font-medium text-gray-700 mb-1';

export const offTopicModes = [
  { value: 'refuse', label: 'Refuse politely' },
  { value: 'redirect', label: 'Refuse and redirect' },
  { value: 'brief', label: 'Answer briefly' },
];
export const emojiPolicies = [
  { value: 'off', label: 'Off' },
  { value: 'sparingly', label: 'Sparingly' },
  { value: 'free', label: 'Free' },
];
export const verbosityLevels = [
  { value: 'short', label: 'Short' },
  { value: 'normal', label: 'Normal' },
  { value: 'long', label: 'Long' },
];

/** A number field as the string an input shows; 0 and missing are empty. */
function numberText(value) {
  return value ? String(value) : '';
}

/** An extraBody value as text: strings as they are, the rest as JSON. */
function extraText(value) {
  return typeof value === 'string' ? value : JSON.stringify(value);
}

/**
 * The text of an extraBody value back to a value: JSON when it parses
 * (numbers, booleans, objects), the text itself otherwise.
 */
export function parseExtraValue(text) {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

/** The form state of a scenario; of a new one when none is given. */
export function toForm(scenario = {}) {
  const { offTopic = {}, style = {}, llm = {}, rateLimit = {}, logging = {} } = scenario;
  return {
    name: scenario.name || '',
    description: scenario.description || '',
    enabled: scenario.enabled ?? true,
    operatorPrompt: scenario.operatorPrompt || '',
    guardrails: scenario.guardrails || '',
    offTopicMode: offTopic.mode || 'refuse',
    redirectTemplate: offTopic.redirectTemplate || '',
    allowedCollections: scenario.allowedCollections || [],
    skills: [...(scenario.skills || [])], // ordered skill names (#404)
    emoji: style.emoji || 'off',
    verbosity: style.verbosity || 'normal',
    markdown: style.markdown ?? true,
    citationTemplate: style.citationTemplate || '',
    model: llm.model || '',
    temperature: llm.temperature === undefined ? '' : String(llm.temperature),
    maxTokens: numberText(llm.maxTokens),
    extraBody: Object.entries(llm.extraBody || {}).map(([key, value]) => ({ key, value: extraText(value) })),
    ...toLoopGuardForm(llm.loopGuard), // #413, lib/chat-loop-guard.js
    maxTurns: numberText(scenario.maxTurns),
    tokenBudgetPerSession: numberText(scenario.tokenBudgetPerSession),
    messagesPerMinute: numberText(rateLimit.messagesPerMinute),
    mcpServers: toMcpRows(scenario.mcpServers), // MCP-005, lib/chat-mcp.js
    // #406: the turn log — off unless enabled, redaction on unless turned off.
    loggingEnabled: logging.enabled ?? false,
    logRetentionDays: numberText(logging.retentionDays),
    logRedact: logging.redact ?? true,
  };
}

/** A whole number from an input; 0 when empty or not a number. */
function wholeNumber(text) {
  return Number.parseInt(text, 10) || 0;
}

/** The scenario object a form state stands for, ready to PUT. */
export function fromForm(form) {
  const llm = { model: form.model.trim(), maxTokens: wholeNumber(form.maxTokens) };
  if (form.temperature !== '') llm.temperature = Number(form.temperature);
  const extraBody = {};
  form.extraBody.forEach(({ key, value }) => {
    if (key.trim()) extraBody[key.trim()] = parseExtraValue(value);
  });
  if (Object.keys(extraBody).length > 0) llm.extraBody = extraBody;
  const loopGuard = fromLoopGuardForm(form);
  if (loopGuard) llm.loopGuard = loopGuard;
  const scenario = {
    description: form.description,
    enabled: form.enabled,
    operatorPrompt: form.operatorPrompt,
    guardrails: form.guardrails,
    offTopic: { mode: form.offTopicMode, redirectTemplate: form.redirectTemplate },
    allowedCollections: form.allowedCollections,
    skills: form.skills,
    style: {
      emoji: form.emoji,
      verbosity: form.verbosity,
      markdown: form.markdown,
      citationTemplate: form.citationTemplate,
    },
    llm,
    maxTurns: wholeNumber(form.maxTurns),
    tokenBudgetPerSession: wholeNumber(form.tokenBudgetPerSession),
    rateLimit: { messagesPerMinute: wholeNumber(form.messagesPerMinute) },
    logging: { enabled: form.loggingEnabled, retentionDays: wholeNumber(form.logRetentionDays), redact: form.logRedact },
  };
  // A scenario without servers is sent as before; the PUT replaces it whole.
  if (form.mcpServers.length > 0) scenario.mcpServers = fromMcpRows(form.mcpServers);
  return scenario;
}

/** Why a form cannot be saved yet, or null when it can. */
export function formProblem(form) {
  const name = form.name.trim();
  if (!name) return 'Give the scenario a name.';
  if (/[|/\s]/.test(name) || name === 'version') return 'The name may not contain spaces, "/" or "|", or be "version".';
  const temperature = Number(form.temperature);
  if (form.temperature !== '' && !(temperature >= 0 && temperature <= 2)) return 'Temperature must be between 0 and 2.';
  const days = Number(form.logRetentionDays);
  if (form.logRetentionDays !== '' && !(Number.isInteger(days) && days >= 0 && days <= 3650)) return 'Log retention must be 0 to 3650 days.';
  return loopGuardProblem(form) || mcpServerProblem(form.mcpServers);
}
