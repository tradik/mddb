//! Shaping a document chunk for the model (#397, #398).
//!
//! Two jobs: reduce Markdown prose to plain text without touching the code in
//! it, and cut a chunk to a character budget without cutting a character in
//! half.

use std::borrow::Cow;
use std::sync::LazyLock;

use regex::Regex;

/// Marks a cut result, so the model knows there was more.
pub const ELLIPSIS: &str = "…";

static INLINE_CODE: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"`[^`\n]+`").unwrap());
static IMAGE: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"!\[([^\]]*)\]\([^)]+\)").unwrap());
static LINK: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"\[([^\]]+)\]\([^)]+\)").unwrap());
static HTML_TAG: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"<[^>]+>").unwrap());
static HEADING: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"(?m)^#{1,6}\s+").unwrap());
static RULE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(?m)^-{3,}$|^={3,}$|^\*{3,}$").unwrap());
static BLOCKQUOTE: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"(?m)^>\s?").unwrap());
// `[ \t]*`, not `\s*`: in multi-line mode `\s` eats the newline before the
// item and the blank line that separated a list from its paragraph with it.
static LIST_MARKER: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(?m)^[ \t]*[-*+][ \t]").unwrap());
static BLANK_RUN: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"\n{3,}").unwrap());

/// Whether a line opens or closes a fenced code block.
fn is_fence(line: &str) -> bool {
    line.trim_start().starts_with("```")
}

/// Plain text for the model: Markdown markup removed from the prose, fenced
/// code blocks and inline code kept exactly as written.
///
/// The old pass replaced every fence with "[code block removed]" and turned
/// every underscore into a space, so the command, flag or config key a user
/// asked about was the one thing the model never saw — and small models
/// filled the gap by inventing one (#398).
pub fn strip_markdown(input: &str) -> String {
    let mut segments: Vec<String> = Vec::new();
    let mut prose: Vec<&str> = Vec::new();
    let mut code: Vec<&str> = Vec::new();
    let mut in_fence = false;

    for line in input.lines() {
        match (in_fence, is_fence(line)) {
            (false, true) => {
                push_prose(&mut segments, &prose);
                prose.clear();
                code.push(line);
                in_fence = true;
            }
            (true, true) => {
                code.push(line);
                segments.push(code.join("\n"));
                code.clear();
                in_fence = false;
            }
            (true, false) => code.push(line),
            (false, false) => prose.push(line),
        }
    }
    push_prose(&mut segments, &prose);
    // A chunk boundary can fall inside a code block; the tail is still code.
    if !code.is_empty() {
        segments.push(code.join("\n"));
    }

    segments.join("\n\n")
}

fn push_prose(segments: &mut Vec<String>, lines: &[&str]) {
    let stripped = strip_prose(&lines.join("\n"));
    if !stripped.is_empty() {
        segments.push(stripped);
    }
}

/// Strip a run of prose: line-level markup on the whole text, inline markup
/// only between inline code spans, which pass through untouched.
fn strip_prose(text: &str) -> String {
    let s = HEADING.replace_all(text, "");
    let s = RULE.replace_all(&s, "");
    let s = BLOCKQUOTE.replace_all(&s, "");
    let s = LIST_MARKER.replace_all(&s, "");

    let mut out = String::with_capacity(s.len());
    let mut last = 0;
    for span in INLINE_CODE.find_iter(&s) {
        out.push_str(&strip_inline(&s[last..span.start()]));
        out.push_str(span.as_str());
        last = span.end();
    }
    out.push_str(&strip_inline(&s[last..]));

    BLANK_RUN.replace_all(&out, "\n\n").trim().to_string()
}

/// Inline markup only: images, links, HTML tags, emphasis. Underscores stay —
/// they are part of identifiers far more often than they are italics.
fn strip_inline(text: &str) -> String {
    let s = IMAGE.replace_all(text, "$1");
    let s = LINK.replace_all(&s, "$1");
    let s = HTML_TAG.replace_all(&s, "");
    s.replace("**", "").replace("__", "").replace('*', "")
}

/// The first `max_chars` characters of `text`, or all of it when it fits.
///
/// Cut on a character boundary — `&text[..800]` is a byte index and panics
/// inside a multi-byte character (#397). A cut text ends with [`ELLIPSIS`],
/// and a cut that lands inside a fenced code block closes the fence so the
/// model is not left reading an open one. `max_chars == 0` means no limit.
pub fn clip(text: &str, max_chars: usize) -> Cow<'_, str> {
    if max_chars == 0 {
        return Cow::Borrowed(text);
    }
    let Some((cut_at, _)) = text.char_indices().nth(max_chars) else {
        return Cow::Borrowed(text);
    };

    let head = &text[..cut_at];
    let mut out = String::with_capacity(head.len() + ELLIPSIS.len() + 4);
    out.push_str(head);
    out.push_str(ELLIPSIS);
    if head.lines().filter(|l| is_fence(l)).count() % 2 == 1 {
        out.push_str("\n```");
    }
    Cow::Owned(out)
}

#[cfg(test)]
mod tests;
