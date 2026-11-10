//! Shaping a document chunk for the model (#397).
//!
//! Cutting a chunk to a character budget without cutting a character in half.

use std::borrow::Cow;

/// Marks a cut result, so the model knows there was more.
pub const ELLIPSIS: &str = "…";

/// Whether a line opens or closes a fenced code block.
fn is_fence(line: &str) -> bool {
    line.trim_start().starts_with("```")
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
