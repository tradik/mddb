import * as core from '@actions/core';

export type KeyStrategy = 'path' | 'hash' | 'filename';

export interface ActionInputs {
  mddbUrl: string;
  apiKey: string;
  collection: string;
  patterns: string[];
  ignore: string[];
  workingDirectory: string;
  language: string;
  keyStrategy: KeyStrategy;
  keyPrefix: string;
  concurrency: number;
  timeoutSeconds: number;
  verifySsl: boolean;
  dryRun: boolean;
  failOnError: boolean;
}

const VALID_KEY_STRATEGIES: readonly KeyStrategy[] = ['path', 'hash', 'filename'] as const;

export function parseBool(value: string, fallback: boolean): boolean {
  const v = value.trim().toLowerCase();
  if (v === '') return fallback;
  if (['true', '1', 'yes', 'on'].includes(v)) return true;
  if (['false', '0', 'no', 'off'].includes(v)) return false;
  throw new Error(`Invalid boolean: "${value}". Use true/false.`);
}

export function parseInteger(value: string, name: string, min: number, max: number): number {
  const n = Number.parseInt(value.trim(), 10);
  if (!Number.isFinite(n)) {
    throw new Error(`Invalid ${name}: "${value}". Must be an integer.`);
  }
  if (n < min || n > max) {
    throw new Error(`Invalid ${name}: ${n}. Must be between ${min} and ${max}.`);
  }
  return n;
}

export function splitPatterns(raw: string): string[] {
  return raw
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.length > 0 && !line.startsWith('#'));
}

export function assertKeyStrategy(value: string): KeyStrategy {
  const v = value.trim().toLowerCase();
  if ((VALID_KEY_STRATEGIES as readonly string[]).includes(v)) {
    return v as KeyStrategy;
  }
  throw new Error(
    `Invalid key-strategy: "${value}". Must be one of: ${VALID_KEY_STRATEGIES.join(', ')}.`,
  );
}

// INT-005: key-prefix is concatenated directly onto every document key, so an
// unvalidated value (control chars, spaces, traversal-like sequences, a huge
// string) pollutes the key space and causes confusing server-side errors. Allow
// only a conservative, key-safe charset and bound the length — fail fast in the
// action, consistent with the other validated inputs.
const KEY_PREFIX_PATTERN = /^[A-Za-z0-9._/-]{0,100}$/;

export function assertKeyPrefix(value: string): string {
  if (!KEY_PREFIX_PATTERN.test(value)) {
    throw new Error(
      'Invalid key-prefix. It may only contain [A-Za-z0-9._/-] and be at most 100 characters.',
    );
  }
  return value;
}

export function normaliseUrl(url: string): string {
  const trimmed = trimTrailingSlashes(url.trim());
  if (!/^https?:\/\//i.test(trimmed)) {
    throw new Error(`Invalid mddb-url: "${url}". Must start with http:// or https://.`);
  }
  return trimmed;
}

export function readInputs(getInput: (name: string) => string = core.getInput): ActionInputs {
  const collection = getInput('collection').trim();
  if (collection === '') {
    throw new Error('Input "collection" is required.');
  }

  const patterns = splitPatterns(getInput('path') || '**/*.md');
  if (patterns.length === 0) {
    throw new Error('Input "path" produced no patterns.');
  }

  return {
    mddbUrl: normaliseUrl(getInput('mddb-url') || 'https://mddb.tradik.com'),
    apiKey: getInput('api-key').trim(),
    collection,
    patterns,
    ignore: splitPatterns(getInput('ignore')),
    workingDirectory: (getInput('working-directory') || '.').trim(),
    language: (getInput('language') || 'en_US').trim(),
    keyStrategy: assertKeyStrategy(getInput('key-strategy') || 'path'),
    keyPrefix: assertKeyPrefix(getInput('key-prefix')),
    concurrency: parseInteger(getInput('concurrency') || '8', 'concurrency', 1, 64),
    timeoutSeconds: parseInteger(getInput('timeout-seconds') || '30', 'timeout-seconds', 1, 600),
    verifySsl: parseBool(getInput('verify-ssl') || 'true', true),
    dryRun: parseBool(getInput('dry-run') || 'false', false),
    failOnError: parseBool(getInput('fail-on-error') || 'true', true),
  };
}

/** Strips trailing slashes without a regex (a `/\/+$/` backtracks). */
function trimTrailingSlashes(s: string): string {
  let end = s.length;
  while (end > 0 && s[end - 1] === '/') end--;
  return s.slice(0, end);
}
