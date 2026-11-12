use super::super::{LoopDetector, LoopGuardSettings, Unit, check};
use super::{defaults, looping, tiny};

const INTRO: &str = "MDDB keeps every collection in one file, and a backup copies that file \
    while the server keeps serving; nothing has to stop for it. ";
const PARAGRAPH: &str = "To restore, call the restore endpoint with the backup's name, \
    then wait for the server to report that it is ready again. ";

/// The unit index of byte `offset` in `text`, in characters.
fn chars_before(text: &str, offset: usize) -> usize {
    text[..offset].chars().count()
}

#[test]
fn a_paragraph_streamed_forever_is_cut_within_a_window_of_the_loop() {
    let text = looping(INTRO, PARAGRAPH, 40);
    let mut detector = LoopDetector::new(defaults());
    let found = text
        .as_bytes()
        .chunks(7)
        .find_map(|chunk| detector.push(std::str::from_utf8(chunk).unwrap()))
        .expect("the loop is found");

    // The repetition began with the second copy — or a character or two
    // before it, where the intro happens to end as the paragraph does.
    let second_copy = INTRO.len() + PARAGRAPH.len();
    assert!((second_copy - 2..=second_copy).contains(&found.loop_start));
    let loop_began = chars_before(&text, second_copy) + defaults().ngram - 1;
    assert!(
        found.position - loop_began <= defaults().window,
        "found {} units after the loop began",
        found.position - loop_began
    );
    assert!(found.ratio >= 0.8);
}

#[test]
fn chunk_boundaries_do_not_change_what_is_found() {
    let text = looping("Intro ✓ — ", "ąę loop paragraph with ünïcödé ", 30);
    let whole = check(Some(defaults()), &text).expect("found whole");
    let mut detector = LoopDetector::new(defaults());
    let mut buf = [0u8; 4];
    let by_char = text
        .chars()
        .find_map(|c| detector.push(c.encode_utf8(&mut buf)))
        .expect("found char by char");
    assert_eq!(whole, by_char);
    assert!(text.is_char_boundary(whole.loop_start));
}

#[test]
fn a_short_answer_is_not_judged_before_min_output() {
    let settings = tiny(2, 10, 0.8, 100);
    assert_eq!(check(Some(settings), &"ab".repeat(40)), None);
    let found = check(Some(settings), &"ab".repeat(60)).expect("past min_output");
    assert_eq!(found.position, 100);
    assert_eq!(found.loop_start, 2, "the first \"ab\" is kept");
}

#[test]
fn an_answer_that_repeats_a_sentence_once_is_not_a_loop() {
    let text = format!("{INTRO}{PARAGRAPH}As said: {PARAGRAPH}{INTRO}");
    assert_eq!(check(Some(defaults()), &text), None);
}

#[test]
fn words_are_judged_as_units_of_their_own() {
    let settings = LoopGuardSettings {
        unit: Unit::Words,
        ..tiny(4, 20, 0.8, 20)
    };
    let text = looping(INTRO, PARAGRAPH, 10);
    let found = check(Some(settings), &text).expect("found in words");
    assert_eq!(found.loop_start, INTRO.len() + PARAGRAPH.len());
    assert_eq!(check(Some(settings), INTRO), None);

    // The last word is held until the end; `finish` judges it.
    let one = LoopGuardSettings {
        unit: Unit::Words,
        ..tiny(1, 2, 1.0, 0)
    };
    let mut detector = LoopDetector::new(one);
    assert_eq!(detector.push("a a a"), None);
    assert!(detector.finish().is_some());
}

#[test]
fn a_line_still_held_at_the_end_is_judged_by_finish() {
    let mut detector = LoopDetector::new(tiny(1, 2, 1.0, 0));
    assert_eq!(detector.push("``\n``"), None);
    let found = detector.finish().expect("the held backticks repeat");
    assert_eq!(found.position, 5);
}

#[test]
fn a_loop_after_a_clean_stretch_starts_where_its_repeats_do() {
    // A sentence repeated once early on, long clean text, then a loop: the
    // loop starts at its own first repeat, not at the early one.
    let filler: String = (0..40).map(|i| format!("item {i} differs; ")).collect();
    let early = format!("{PARAGRAPH}{PARAGRAPH}");
    let text = format!(
        "{early}{filler}{}",
        "spin around again and again ".repeat(30)
    );
    let found = check(Some(defaults()), &text).expect("the loop is found");
    let loop_copy = early.len() + filler.len() + "spin around again and again ".len();
    // The filler ends in a blank, as the loop's text does.
    assert_eq!(found.loop_start, loop_copy - 1);
}
