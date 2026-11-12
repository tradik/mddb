//! Tests of the loop guard (#413): its settings, the detector, the Markdown
//! it leaves alone, and the regression fixtures it must not cut.

mod detection;
mod fixtures;
mod markdown;
mod settings;

use super::{LoopGuardSettings, Unit};

/// Settings small enough to reason about by hand.
pub fn tiny(ngram: usize, window: usize, ratio: f64, min_output: usize) -> LoopGuardSettings {
    LoopGuardSettings {
        ngram,
        window,
        ratio,
        min_output,
        unit: Unit::Chars,
    }
}

/// The defaults, as an absent `[llm.loop_guard]` gives them.
pub fn defaults() -> LoopGuardSettings {
    super::LoopGuardConfig::default()
        .settings()
        .expect("on by default")
}

/// `paragraph` after `intro`, repeated `times` times.
pub fn looping(intro: &str, paragraph: &str, times: usize) -> String {
    format!("{intro}{}", paragraph.repeat(times))
}
