use super::{ELLIPSIS, clip};
use std::borrow::Cow;

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
