// Which files the upload dialog accepts and the key each one is stored under.
//
// Every file goes to POST /v1/upload. The server reads Markdown frontmatter
// with the same parser as /v1/ingest, mddb-cli and the folder loader (2.19.0),
// so `tags: [a, b]` and block lists arrive as lists. The panel used to parse
// frontmatter itself and stored `tags: [a, b]` as ["[a", "b]"].

/** Extensions /v1/upload converts or stores as text (docs/API.md, POST /v1/upload). */
export const SUPPORTED_EXTENSIONS = [
  '.md', '.markdown', '.txt', '.html', '.htm', '.pdf', '.docx', '.odt', '.rtf',
  '.xlsx', '.ods', '.csv', '.tsv', '.pptx', '.odp', '.epub', '.eml', '.mbox',
  '.yaml', '.yml', '.log', '.lex', '.tex', '.latex',
];

/** The server's default per-file limit. */
export const MAX_FILE_SIZE = 10 * 1024 * 1024;

const MARKDOWN = /\.(md|markdown)$/i;

/** The lower-case extension of a file name, with its dot ('' when none). */
export function extensionOf(name) {
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot).toLowerCase() : '';
}

/** True for Markdown files, which are stored as written (no conversion). */
export function isMarkdown(name) {
  return MARKDOWN.test(name);
}

/**
 * The document key for a file. Markdown keeps the key earlier panels gave it
 * (the name without its extension) so a re-upload replaces the same document;
 * other formats use the lower-case, hyphenated name the server derives.
 */
export function keyForFile(name) {
  if (isMarkdown(name)) return name.replace(MARKDOWN, '');
  return name.replace(/\.[^.]+$/, '').toLowerCase().replace(/\s+/g, '-');
}

/** A short icon for the file list. */
export function fileIcon(name) {
  const ext = extensionOf(name);
  if (ext === '.pdf') return '📄';
  if (ext === '.docx' || ext === '.odt') return '📝';
  if (ext === '.html' || ext === '.htm') return '🌐';
  if (isMarkdown(name)) return '📋';
  if (ext === '.txt') return '📃';
  return '📎';
}

/**
 * Splits a selection into files to upload and messages for the rejected ones.
 * Each accepted entry is { file, key, converted }.
 */
export function classifyFiles(fileList, maxSize = MAX_FILE_SIZE) {
  const accepted = [];
  const errors = [];
  for (const file of Array.from(fileList || [])) {
    if (!SUPPORTED_EXTENSIONS.includes(extensionOf(file.name))) {
      errors.push(`${file.name}: Unsupported format. Use ${SUPPORTED_EXTENSIONS.join(', ')}`);
    } else if (file.size > maxSize) {
      errors.push(`${file.name}: File too large (max ${Math.round(maxSize / 1024 / 1024)}MB)`);
    } else {
      accepted.push({ file, key: keyForFile(file.name), converted: !isMarkdown(file.name) && extensionOf(file.name) !== '.txt' });
    }
  }
  return { accepted, errors };
}
