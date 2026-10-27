import * as crypto from 'node:crypto';
import * as path from 'node:path';
import * as fs from 'node:fs/promises';
import type { KeyStrategy } from './inputs.js';

export interface MddbDocument {
  collection: string;
  key: string;
  lang: string;
  meta: Record<string, string[]>;
  contentMd: string;
}

export interface BuildDocumentOptions {
  collection: string;
  language: string;
  keyStrategy: KeyStrategy;
  keyPrefix: string;
  relativePath: string;
  absolutePath: string;
  /** Repository slug (owner/name) when available — recorded in `meta.repository`. */
  repository?: string;
  /** Git ref or SHA — recorded in `meta.ref`. */
  ref?: string;
}

const MARKDOWN_EXT = new Set(['.md', '.markdown', '.mdx']);
const TEXT_EXT = new Set(['.txt', '.text', '.rst', '.adoc']);
const CODE_FENCE_LANGS: Record<string, string> = {
  '.json': 'json',
  '.yaml': 'yaml',
  '.yml': 'yaml',
  '.toml': 'toml',
  '.html': 'html',
  '.htm': 'html',
  '.xml': 'xml',
  '.css': 'css',
  '.js': 'javascript',
  '.ts': 'typescript',
  '.py': 'python',
  '.go': 'go',
  '.rs': 'rust',
  '.sh': 'bash',
};

export function slugifyPath(relativePath: string): string {
  return trimSlashes(relativePath.replaceAll('\\', '/'))
    .toLowerCase()
    .replace(/[^a-z0-9._/-]+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '');
}

export function basenameKey(relativePath: string): string {
  const base = path.basename(relativePath, path.extname(relativePath));
  return slugifyPath(base);
}

export function deriveKey(strategy: KeyStrategy, relativePath: string, content: string): string {
  switch (strategy) {
    case 'path':
      return slugifyPath(relativePath);
    case 'filename':
      return basenameKey(relativePath);
    case 'hash':
      // A document key, not a security check; changing the algorithm would
      // change every key already stored with keyStrategy: hash.
      return crypto.createHash('sha1').update(content).digest('hex'); // NOSONAR typescript:S4790
  }
}

export function buildContentMd(relativePath: string, raw: string): string {
  const ext = path.extname(relativePath).toLowerCase();
  if (MARKDOWN_EXT.has(ext)) return raw;
  if (TEXT_EXT.has(ext)) return raw;
  const lang = CODE_FENCE_LANGS[ext] ?? '';
  return '```' + lang + '\n' + raw.replaceAll('```', '` ``') + '\n```\n';
}

export function buildMeta(opts: BuildDocumentOptions, size: number): Record<string, string[]> {
  const meta: Record<string, string[]> = {
    source: ['github-action'],
    path: [opts.relativePath.replaceAll('\\', '/')],
    extension: [path.extname(opts.relativePath).toLowerCase() || '(none)'],
    size: [String(size)],
  };
  if (opts.repository) meta.repository = [opts.repository];
  if (opts.ref) meta.ref = [opts.ref];
  return meta;
}

export async function buildDocument(opts: BuildDocumentOptions): Promise<MddbDocument> {
  const raw = await fs.readFile(opts.absolutePath, 'utf8');
  const baseKey = deriveKey(opts.keyStrategy, opts.relativePath, raw);
  const key = opts.keyPrefix ? `${opts.keyPrefix}${baseKey}` : baseKey;

  return {
    collection: opts.collection,
    key,
    lang: opts.language,
    meta: buildMeta(opts, Buffer.byteLength(raw, 'utf8')),
    contentMd: buildContentMd(opts.relativePath, raw),
  };
}

/** Strips leading and trailing slashes without a regex (`/^\/+|\/+$/` backtracks). */
function trimSlashes(s: string): string {
  let start = 0;
  let end = s.length;
  while (start < end && s[start] === '/') start++;
  while (end > start && s[end - 1] === '/') end--;
  return s.slice(start, end);
}
