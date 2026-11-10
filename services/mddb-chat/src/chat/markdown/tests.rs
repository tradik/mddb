use super::{ELLIPSIS, clip, strip_markdown};
use std::borrow::Cow;

// #398: a result that reaches the model used to be a flattened string with
// every code block replaced by "[code block removed]". The parts users ask
// for — commands, flags, config keys — are exactly the parts that were lost.

#[test]
fn prose_markup_is_removed() {
    let input = "# Title\n\nSome **bold** and *italic* text with a [link](https://x.test) \
                 and an ![image](img.png).\n\n> quoted\n\n- item one\n- item two\n\n---\n\n<b>html</b>";
    let got = strip_markdown(input);
    assert_eq!(
        got,
        "Title\n\nSome bold and italic text with a link and an image.\n\nquoted\n\nitem one\nitem two\n\nhtml"
    );
}

#[test]
fn a_fenced_code_block_survives_verbatim() {
    let input = "Install it:\n\n```sh\nmddb-cli autoindex --source ./docs **not bold** _not italic_\n# a comment, not a heading\n```\n\nThen **run** it.";
    let got = strip_markdown(input);
    assert_eq!(
        got,
        "Install it:\n\n```sh\nmddb-cli autoindex --source ./docs **not bold** _not italic_\n# a comment, not a heading\n```\n\nThen run it."
    );
}

#[test]
fn an_unterminated_fence_runs_to_the_end_verbatim() {
    // A chunk boundary can land inside a code block; the tail is still code.
    let input = "Config:\n\n```toml\n[mddb]\nsearch_top_k = 5\n# still code";
    let got = strip_markdown(input);
    assert_eq!(
        got,
        "Config:\n\n```toml\n[mddb]\nsearch_top_k = 5\n# still code"
    );
}

#[test]
fn inline_code_keeps_its_backticks_and_its_contents() {
    let got = strip_markdown("Set `search_top_k` or pass `--source *.md` to **it**.");
    assert_eq!(got, "Set `search_top_k` or pass `--source *.md` to it.");
}

#[test]
fn snake_case_identifiers_in_prose_are_not_broken() {
    // The old pass turned every underscore into a space: result_max_chars
    // became "result max chars", which no model can map back to a key.
    let got = strip_markdown("The key result_max_chars sets the limit.");
    assert_eq!(got, "The key result_max_chars sets the limit.");
}

#[test]
fn a_list_marker_after_inline_code_is_not_mistaken_for_a_list() {
    let got = strip_markdown("Use `x` - not y");
    assert_eq!(got, "Use `x` - not y");
}

#[test]
fn blank_runs_collapse_and_edges_are_trimmed() {
    let got = strip_markdown("\n\n\nfirst\n\n\n\n\nsecond\n\n\n");
    assert_eq!(got, "first\n\nsecond");
}

#[test]
fn a_document_that_is_only_code_is_returned_as_is() {
    let input = "```\nline one\nline two\n```";
    assert_eq!(strip_markdown(input), input);
}

// #397: &clean[..800] is a byte index and panics inside a multi-byte
// character. Every cut below lands on a character boundary.

#[test]
fn text_within_the_limit_is_borrowed_unchanged() {
    assert!(matches!(clip("short", 10), Cow::Borrowed("short")));
    assert!(matches!(clip("exact", 5), Cow::Borrowed("exact")));
}

#[test]
fn zero_means_no_limit() {
    let long = "x".repeat(10_000);
    assert!(matches!(clip(&long, 0), Cow::Borrowed(_)));
}

#[test]
fn polish_text_is_cut_between_characters() {
    let text = "zażółć gęślą jaźń ".repeat(100);
    let got = clip(&text, 800);
    assert_eq!(got.chars().count(), 801, "800 characters and the ellipsis");
    assert!(got.ends_with(ELLIPSIS));
    assert!(text.starts_with(&got[..got.len() - ELLIPSIS.len()]));
}

#[test]
fn emoji_are_cut_between_characters() {
    let text = "🙂🙃😉".repeat(400);
    let got = clip(&text, 800);
    assert_eq!(got.chars().count(), 801);
    assert!(got.ends_with(ELLIPSIS));
}

#[test]
fn box_drawing_is_cut_between_characters() {
    let text = "┌─┐\n│ │\n└─┘\n".repeat(200);
    let got = clip(&text, 800);
    assert_eq!(got.chars().count(), 801);
    assert!(got.ends_with(ELLIPSIS));
}

#[test]
fn every_limit_lands_on_a_boundary() {
    // Slicing a &str inside a character panics, so reaching the assertion
    // is the test.
    let text = "aż🙂─b";
    for max in 1..=text.chars().count() + 1 {
        let got = clip(text, max);
        assert!(got.chars().count() <= max + 1);
    }
}

#[test]
fn a_cut_inside_a_code_block_closes_the_fence() {
    let text = "Run:\n\n```sh\nmddb-cli autoindex --source ./docs\nmddb-cli stats\n```\n\nDone.";
    let got = clip(text, 30);
    assert!(got.starts_with("Run:\n\n```sh\nmddb-cli autoin"));
    assert!(got.ends_with(&format!("{ELLIPSIS}\n```")), "got: {got:?}");
}

#[test]
fn a_cut_after_a_closed_code_block_adds_no_fence() {
    let text = "```\ncode\n```\nprose that goes on and on";
    let got = clip(text, 20);
    assert_eq!(got.as_ref(), format!("```\ncode\n```\nprose t{ELLIPSIS}"));
}
