// FE-021: what a person reads in the panel carries no issue number ("#413")
// and no ticket ID ("INT-023"). References belong in code comments, which
// this check skips; string literals, template literals and JSX text are
// checked in every source file but tests.
import { describe, test, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const srcDir = fileURLToPath(new URL('.', import.meta.url));

// Comments first, so a quote inside one is never read as a string.
const TOKEN = /\/\/[^\n]*|\/\*[\s\S]*?\*\/|"(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'|`(?:[^`\\]|\\.)*`|>[^<>{}]+</g;
// An issue number is up to five digits; colours (#111827) and entities (&#10;) are not.
const REF = /(?<![&\w])#\d{1,5}(?![\w;])|\b(?!SHA-|AES-)[A-Z]{2,5}-\d{3}\b/;

/** The literals and JSX texts of `source` that carry a reference. */
function internalRefs(source) {
  const found = [];
  for (const [token] of source.matchAll(TOKEN)) {
    if (token.startsWith('//') || token.startsWith('/*')) continue;
    if (REF.test(token)) found.push(token.trim());
  }
  return found;
}

function sourceFiles(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return sourceFiles(path);
    return /\.(js|jsx)$/.test(entry.name) && !entry.name.includes('.test.') ? [path] : [];
  });
}

describe('user-visible texts', () => {
  test('the check finds references in texts and not in comments', () => {
    expect(internalRefs('const a = "Loop stopped (#413)."; // #413')).toEqual(['"Loop stopped (#413)."']);
    expect(internalRefs('<p>Synced (INT-023)</p>')).toEqual(['>Synced (INT-023)<']);
    expect(internalRefs('label: `GO-046 ${x}`')).toEqual(['`GO-046 ${x}`']);
    expect(internalRefs('/* RAG-011 */ x = { a: 1 }; {/* #12 */}')).toEqual([]);
    expect(internalRefs("color: '#059669', h: 'SHA-256', c: 'AES-256', e: '&#10;', x: '#3B82F6'")).toEqual([]);
  });

  test('no panel source shows an issue number or a ticket ID', () => {
    const offenders = sourceFiles(srcDir).flatMap((file) =>
      internalRefs(readFileSync(file, 'utf8')).map((text) => `${relative(srcDir, file)}: ${text}`));
    expect(offenders).toEqual([]);
  });
});
