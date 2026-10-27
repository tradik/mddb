/// Minimal markdown to HTML renderer
/// Supports: bold, italic, inline code, code blocks, links, line breaks
export function renderMarkdown(text: string): string {
  let html = escapeHtml(text);

  // Code blocks: ```...```
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, (_match, _lang, code) => {
    return `<pre><code>${code.trim()}</code></pre>`;
  });

  // Inline code: `...`
  html = html.replace(/`([^`]+)`/g, '<code>$1</code>');

  // Bold: **...**
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');

  // Italic: *...*
  html = html.replace(/(?<!\*)\*(?!\*)(.+?)(?<!\*)\*(?!\*)/g, '<em>$1</em>');

  // Links: [text](url) — only emit an anchor for safe schemes (FE-003).
  // An unsafe scheme (javascript:, data:, vbscript:, blob:, …) would otherwise
  // produce a clickable XSS link in the host page's origin, since the message
  // content comes from the server/LLM (prompt injection). escapeHtml already
  // ran, so the URL can't break out of the href attribute — only the scheme
  // is the risk — hence an allowlist that drops the link but keeps the text.
  html = replaceLinks(html, (label, url) => {
    if (!isSafeUrl(url)) {
      return label;
    }
    return `<a href="${url}" target="_blank" rel="noopener noreferrer">${label}</a>`;
  });

  // Line breaks
  html = html.replace(/\n/g, '<br>');

  return html;
}

/// replaceLinks rewrites every [label](url) — the matches of
/// /\[([^\]]+)\]\(([^)]+)\)/g, leftmost first — in one pass. The regex
/// backtracks on input such as many "[" with no "](": each position retried
/// the rest of the text. Here a "[" whose "]" is not followed by "(" skips to
/// that "]", since every "[" before it would end at the same place, and with
/// no ")" left nothing further can match.
export function replaceLinks(html: string, render: (label: string, url: string) => string): string {
  let out = '';
  let i = 0;
  while (i < html.length) {
    const open = html.indexOf('[', i);
    if (open < 0) break;
    const close = html.indexOf(']', open + 1);
    if (close < 0) break;
    if (close === open + 1 || html[close + 1] !== '(') {
      out += html.slice(i, close + 1);
      i = close + 1;
      continue;
    }
    const end = html.indexOf(')', close + 2);
    if (end < 0) break;
    if (end === close + 2) {
      out += html.slice(i, end + 1);
      i = end + 1;
      continue;
    }
    out += html.slice(i, open) + render(html.slice(open + 1, close), html.slice(close + 2, end));
    i = end + 1;
  }
  return out + html.slice(i);
}

const SAFE_SCHEME_RE = /^(https?|mailto):/i;

/// isSafeUrl allows only http(s)/mailto URLs or relative references; every
/// other scheme is rejected. Whitespace and control characters are stripped
/// first because browsers ignore them inside a URL scheme (so `java\tscript:`
/// would otherwise slip through).
export function isSafeUrl(raw: string): boolean {
  const url = raw.replace(/[\u0000-\u0020]+/g, "");
  if (
    url.startsWith('/') ||
    url.startsWith('#') ||
    url.startsWith('./') ||
    url.startsWith('../')
  ) {
    return true;
  }
  return SAFE_SCHEME_RE.test(url);
}

function escapeHtml(text: string): string {
  const map: Record<string, string> = {
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#039;',
  };
  return text.replace(/[&<>"']/g, (ch) => map[ch] || ch);
}
