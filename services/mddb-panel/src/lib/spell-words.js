/**
 * Words of a Markdown text for the editor's collection spelling (FE-020).
 * The server's /v1/spell-suggest splits its text on whitespace only, so a
 * word with punctuation ("word,") would come back as a misspelling of
 * itself. The panel therefore sends a plain word list built here.
 */

/** Markdown and markup parts that are not prose: code, URLs, link targets, tags. */
const NOT_PROSE = [
  /```[\s\S]*?(?:```|$)/g, // fenced code blocks (an unclosed one runs to the end)
  /`[^`\n]*`/g, // inline code
  /\]\([^)]*\)/g, // link and image targets
  /<[^>\n]*>/g, // HTML tags
  /\b(?:https?|ftp|mailto):\S+/gi, // bare URLs
  /\S+@\S+\.\S+/g, // e-mail addresses
];

/** A word the server checks: three letters or more (shorter ones it skips). */
const WORD = /\p{L}{3,}/gu;

/** The distinct words of `markdown`'s prose, lower-cased, in first-seen order. */
export function extractWords(markdown) {
  let prose = markdown || '';
  for (const pattern of NOT_PROSE) prose = prose.replace(pattern, ' ');
  const words = new Set();
  for (const match of prose.matchAll(WORD)) words.add(match[0].toLowerCase());
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
  const pattern = new RegExp(`(?<![\\p{L}\\p{N}])${word}(?![\\p{L}\\p{N}])`, 'giu');
  return text.replace(pattern, (found) => matchCase(found, replacement));
}
