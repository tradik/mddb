/**
 * Strips HTML tags from a string, then trims and truncates it.
 *
 * The tag removal is a character scan, not `replace(/<[^>]*>/g, '')`. That
 * regex requires a closing `>` to match anything, so an **unterminated** tag
 * survives it untouched: `"<script src=x"` and
 * `"hi <img src=x onerror=alert(1)"` come out exactly as they went in. That is
 * what CodeQL's js/incomplete-multi-character-sanitization means by "this
 * string may still contain <script", and it matters because the fragment is
 * later concatenated with other markup, which supplies the `>`.
 *
 * The scan drops everything from a `<` onward until a `>` closes it, or to the
 * end of the string if none does. The consequence is deliberate: `"a<b"`
 * becomes `"a"`. Losing a stray less-than is the same trade the server makes,
 * and this is deliberately the same algorithm as its `strip_html_tags`
 * (services/mddb-chat/src/security/sanitizer.rs), so what the widget echoes
 * locally matches what the server stores. The server remains the authority;
 * this exists so the two agree, not instead of it.
 */
export function sanitizeInput(input: string, maxLength: number): string {
  return stripHtmlTags(input).trim().slice(0, maxLength);
}

function stripHtmlTags(input: string): string {
  let out = '';
  let inTag = false;

  for (const ch of input) {
    if (ch === '<') {
      inTag = true;
    } else if (ch === '>' && inTag) {
      inTag = false;
    } else if (!inTag) {
      out += ch;
    }
  }

  return out;
}
