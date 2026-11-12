use super::super::markdown::LineClassifier;

/// `text` fed one character at a time, then flushed: each character with
/// whether it is exempt.
fn classify(text: &str) -> Vec<(char, bool)> {
    let mut lines = LineClassifier::default();
    let mut out = Vec::new();
    for (i, c) in text.char_indices() {
        lines.feed(c, i, &mut out);
    }
    lines.flush(&mut out);
    out.into_iter().map(|(c, _, exempt)| (c, exempt)).collect()
}

/// The exempt characters of `text`, as a string.
fn exempt(text: &str) -> String {
    classify(text)
        .into_iter()
        .filter(|(_, e)| *e)
        .map(|(c, _)| c)
        .collect()
}

#[test]
fn every_character_comes_out_once_and_in_order() {
    let text = "a\n  | t |\n```\ncode\n```\nb";
    let out: String = classify(text).into_iter().map(|(c, _)| c).collect();
    assert_eq!(out, text);
}

#[test]
fn fenced_blocks_and_table_rows_are_exempt() {
    assert_eq!(exempt("text\n```rs\nx\n```\nafter"), "```rs\nx\n```\n");
    assert_eq!(exempt("~~~\nx\n\ny\n~~~\n"), "~~~\nx\n\ny\n~~~\n");
    assert_eq!(exempt("a\n  | t |\nb"), "  | t |\n");
    assert_eq!(exempt("\t|x|"), "\t|x|");
}

#[test]
fn inline_code_and_short_runs_are_text() {
    assert_eq!(exempt("``x`` is inline\n"), "");
    assert_eq!(exempt("`a`\n"), "");
    assert_eq!(exempt("~ approx\n"), "");
    assert_eq!(exempt("\n\n"), "", "blank lines outside a block");
}

#[test]
fn an_unfinished_line_is_released_as_its_block() {
    assert_eq!(exempt("``"), "");
    assert_eq!(exempt("```\nx\n  "), "```\nx\n  ");
}
