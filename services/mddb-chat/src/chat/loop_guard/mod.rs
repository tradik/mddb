//! The loop guard (#413): stops an answer that has fallen into a repetition
//! loop instead of letting it run until `max_tokens`.
//!
//! Small models behind Ollama sometimes emit the same few lines over and
//! over. `max_tokens` and `max_response_length` bound the damage only after
//! the whole budget is spent; the guard watches the text as it arrives and
//! ends the answer once most of what the model writes is a repeat of what it
//! already wrote.
//!
//! The settings come from `[llm.loop_guard]`, laid over by a scenario's
//! `llm.loopGuard` in mddbd field by field. The detector is `detector.rs`,
//! the Markdown it leaves alone `markdown.rs`, and what a detection reports
//! `report.rs`.

mod detector;
mod markdown;
pub mod report;

#[cfg(test)]
mod tests;

use serde::Deserialize;

use crate::grpc::client::proto::ChatLoopGuardProto;

pub use detector::{LoopDetected, LoopDetector};

/// Appended to an answer the guard stopped, after the text up to the loop.
pub const MARKER: &str = "[Response stopped: repetition detected]";

/// What the guard counts in.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Unit {
    /// Characters: no tokenizer needed, and the default.
    #[default]
    Chars,
    /// Whitespace-separated words, a rough stand-in for tokens.
    Words,
}

impl Unit {
    /// The unit a scenario names in mddbd; anything else is not set.
    pub fn parse(name: &str) -> Option<Self> {
        match name {
            "chars" => Some(Self::Chars),
            "words" => Some(Self::Words),
            _ => None,
        }
    }

    /// The defaults of this unit: `(ngram, window, min_output)`. In words,
    /// 16 words is roughly the 64 characters of the character default.
    fn defaults(self) -> (usize, usize, usize) {
        match self {
            Self::Chars => (64, 256, 200),
            Self::Words => (16, 64, 50),
        }
    }
}

/// The repeated share over the window that counts as a loop.
pub const DEFAULT_RATIO: f64 = 0.8;

/// `[llm.loop_guard]` as written, and a scenario's `llm.loopGuard`: every
/// field optional, so an override changes only what it names.
#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
pub struct LoopGuardConfig {
    /// On unless set to false.
    pub enabled: Option<bool>,
    /// Units per compared fragment.
    pub ngram: Option<usize>,
    /// How many of the latest units the repeated share is measured over.
    pub window: Option<usize>,
    /// The repeated share, in (0, 1], that counts as a loop.
    pub ratio: Option<f64>,
    /// Units an answer must have before it is judged at all.
    pub min_output: Option<usize>,
    pub unit: Option<Unit>,
}

/// The guard as one answer applies it, every value settled.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct LoopGuardSettings {
    pub ngram: usize,
    pub window: usize,
    pub ratio: f64,
    pub min_output: usize,
    pub unit: Unit,
}

impl LoopGuardConfig {
    /// `over` laid over this one, field by field.
    pub fn overlay(&self, over: &LoopGuardConfig) -> LoopGuardConfig {
        LoopGuardConfig {
            enabled: over.enabled.or(self.enabled),
            ngram: over.ngram.or(self.ngram),
            window: over.window.or(self.window),
            ratio: over.ratio.or(self.ratio),
            min_output: over.min_output.or(self.min_output),
            unit: over.unit.or(self.unit),
        }
    }

    /// Settings that cannot work, refused at startup.
    pub fn validate(&self) -> Result<(), String> {
        if let Some(ratio) = self.ratio
            && !(ratio > 0.0 && ratio <= 1.0)
        {
            return Err(format!(
                "[llm.loop_guard] ratio must be above 0 and at most 1, not {ratio}"
            ));
        }
        if self.ngram == Some(0) || self.window == Some(0) {
            return Err("[llm.loop_guard] ngram and window must be at least 1".to_string());
        }
        Ok(())
    }

    /// The settings of one answer, `None` when the guard is off. A window
    /// shorter than the n-gram is widened to it, and a ratio out of range —
    /// which only a scenario mddbd did not validate could carry — is the
    /// default, so a bad override loosens nothing it should not.
    pub fn settings(&self) -> Option<LoopGuardSettings> {
        if self.enabled == Some(false) {
            return None;
        }
        let unit = self.unit.unwrap_or_default();
        let (ngram, window, min_output) = unit.defaults();
        let ngram = self.ngram.filter(|n| *n > 0).unwrap_or(ngram);
        let ratio = self
            .ratio
            .filter(|r| *r > 0.0 && *r <= 1.0)
            .unwrap_or(DEFAULT_RATIO);
        Some(LoopGuardSettings {
            ngram,
            window: self.window.unwrap_or(window).max(ngram),
            ratio,
            min_output: self.min_output.unwrap_or(min_output),
            unit,
        })
    }
}

impl LoopGuardConfig {
    /// A scenario's `llm.loopGuard` as mddbd sends it: a 0 or an empty value
    /// is not set, and no guard at all overrides nothing.
    pub fn from_proto(guard: Option<ChatLoopGuardProto>) -> Self {
        let Some(guard) = guard else {
            return Self::default();
        };
        let count = |n: i32| usize::try_from(n).ok().filter(|n| *n > 0);
        Self {
            enabled: guard.enabled,
            ngram: count(guard.ngram),
            window: count(guard.window),
            ratio: (guard.ratio > 0.0).then_some(guard.ratio),
            min_output: count(guard.min_output),
            unit: Unit::parse(&guard.unit),
        }
    }
}

/// Looks for a loop in a whole answer — a non-streaming call's content.
pub fn check(settings: Option<LoopGuardSettings>, text: &str) -> Option<LoopDetected> {
    let mut detector = LoopDetector::new(settings?);
    detector.push(text).or_else(|| detector.finish())
}

/// Where an answer with a loop is cut: where the loop began, moved past the
/// punctuation that closes the word before it. A paragraph that ends as the
/// text before it did — both with ". " — is found a character or two early,
/// and the sentence before the loop keeps its full stop.
pub fn cut_at(text: &str, detected: &LoopDetected) -> usize {
    let start = detected.loop_start.min(text.len());
    let glued = text[..start]
        .chars()
        .next_back()
        .is_some_and(|c| !c.is_whitespace());
    if !glued {
        return start;
    }
    let closing = text[start..]
        .chars()
        .take_while(|c| {
            matches!(
                c,
                '.' | ',' | ';' | ':' | '!' | '?' | ')' | ']' | '"' | '\''
            )
        })
        .map(char::len_utf8)
        .sum::<usize>();
    start + closing
}

/// `text` up to the loop, with the marker after it.
pub fn stopped_answer(text: &str, detected: &LoopDetected) -> String {
    let kept = text[..cut_at(text, detected)].trim_end();
    if kept.is_empty() {
        return MARKER.to_string();
    }
    format!("{kept}\n\n{MARKER}")
}
