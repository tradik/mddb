/**
 * Words of a Markdown text for the editor's collection spelling (FE-020).
 * The panel sends /v1/spell-suggest a plain list of distinct words rather
 * than the text, so code, link targets, tags and addresses are not checked
 * and each word is sent once.
 */

/** Markdown parts that are not prose: code and link targets. */
const NOT_PROSE = [
  /```[\s\S]*?(?:```|$)/g, // fenced code blocks (an unclosed one runs to the end)
  /`[^`\n]*`/g, // inline code
  /\]\([^)]*\)/g, // link and image targets
];

/**
 * `text` with its HTML tags (`<` up to the next `>` on the same line)
 * replaced by spaces. A loop, not a regex, so a stray `<` costs one scan.
 */
function stripTags(text) {
  let out = '';
  let from = 0;
  for (let open = text.indexOf('<'); open !== -1; open = text.indexOf('<', from)) {
    const close = text.indexOf('>', open);
    const lineEnd = text.indexOf('\n', open);
    if (close === -1 || (lineEnd !== -1 && lineEnd < close)) break;
    out += `${text.slice(from, open)} `;
    from = close + 1;
  }
  return out + text.slice(from);
}

/** A whitespace-separated token that is an address, not a word. */
function isAddress(token) {
  return token.includes('@') || token.includes('://') || /^mailto:/i.test(token);
}

/** A word the server checks: three letters or more (shorter ones it skips). */
const WORD = /\p{L}{3,}/gu;

/** The distinct words of `markdown`'s prose, lower-cased, in first-seen order. */
export function extractWords(markdown = '') {
  const prose = stripTags(NOT_PROSE.reduce((text, pattern) => text.replace(pattern, ' '), markdown ?? ''));
  const words = new Set();
  for (const token of prose.split(/\s+/)) {
    if (isAddress(token)) continue;
    for (const match of token.matchAll(WORD)) words.add(match[0].toLowerCase());
  }
  return [...words];
}

/** `corrected` in the case of `original`: all caps, capitalised, or as is. */
export function matchCase(original, corrected) {
  if (original.length > 1 && original === original.toUpperCase()) return corrected.toUpperCase();
  if (original[0] === original[0].toUpperCase()) return corrected[0].toUpperCase() + corrected.slice(1);
  return corrected;
}

/**
 * Replaces every whole-word occurrence of `word` (any case) in `text` with
 * `replacement`, keeping each occurrence's capitalisation. `word` is letters
 * only (it comes from extractWords), so it needs no regex escaping.
 */
export function replaceWord(text, word, replacement) {
  const pattern = new RegExp(String.raw`(?<![\p{L}\p{N}])${word}(?![\p{L}\p{N}])`, 'giu');
  return text.replace(pattern, (found) => matchCase(found, replacement));
}
