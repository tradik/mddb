//! Regression fixtures (#413): real-looking answers that repeat themselves
//! legitimately and must never be cut, at the defaults or in words.

use super::super::{LoopGuardConfig, LoopGuardSettings, Unit, check};
use super::{defaults, looping};

const CODE_BLOCK: &str = include_str!("../../../../tests/fixtures/loop_guard/code_block.md");
const TABLE: &str = include_str!("../../../../tests/fixtures/loop_guard/table.md");
const LIST: &str = include_str!("../../../../tests/fixtures/loop_guard/list.md");
const PROSE: &str = include_str!("../../../../tests/fixtures/loop_guard/prose.md");

const FIXTURES: [(&str, &str); 4] = [
    ("code block", CODE_BLOCK),
    ("table", TABLE),
    ("list", LIST),
    ("prose", PROSE),
];

fn words() -> LoopGuardSettings {
    LoopGuardConfig {
        unit: Some(Unit::Words),
        ..Default::default()
    }
    .settings()
    .unwrap()
}

#[test]
fn legitimate_answers_are_never_cut() {
    for settings in [defaults(), words()] {
        for (name, text) in FIXTURES {
            assert_eq!(check(Some(settings), text), None, "{name} was cut");
            // Streamed in small pieces, as a provider sends them.
            let mut detector = super::super::LoopDetector::new(settings);
            for piece in text.as_bytes().chunks(3) {
                if let Ok(piece) = std::str::from_utf8(piece) {
                    assert_eq!(detector.push(piece), None, "{name} was cut streaming");
                }
            }
        }
    }
}

#[test]
fn the_same_repeats_outside_a_code_block_or_table_are_a_loop() {
    // What makes the fixtures safe is the Markdown, not luck: the same rows
    // and lines without their fence or pipes are caught.
    let table_rows = "docs enabled 30 days yes yes\n";
    let shell = "docker compose exec worker /usr/local/bin/worker --health-check\n";
    for repeated in [table_rows, shell] {
        let text = looping("The rows:\n", repeated, 20);
        assert!(check(Some(defaults()), &text).is_some(), "{repeated}");
        let fenced = format!("```\n{}```\n", repeated.repeat(20));
        assert_eq!(check(Some(defaults()), &fenced), None);
    }
}
