//! The loop detector (#413): a rolling n-gram hash over the units of an
//! answer, and the share of repeated units over the latest `window`.
//!
//! A unit counts as repeated when the n-gram ending at it already occurred
//! earlier in the same answer. A loop is declared once the answer is at least
//! `min_output` units long, the window is full, and the repeated share of the
//! window reaches `ratio`. Each unit costs O(1): one hash update, one set
//! lookup, one window step.

use std::collections::{HashSet, VecDeque};

use super::markdown::LineClassifier;
use super::{LoopGuardSettings, Unit};

/// The base of the rolling hash: odd, so multiplying by it loses nothing.
const BASE: u64 = 0x100_0000_01b3;

/// FNV-1a over a word's characters, for the word unit.
const FNV_OFFSET: u64 = 0xcbf2_9ce4_8422_2325;
const FNV_PRIME: u64 = 0x100_0000_01b3;

/// Where a loop was found.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct LoopDetected {
    /// The byte offset where the repetition began; the text before it is
    /// kept.
    pub loop_start: usize,
    /// How many units had been judged when the loop was declared.
    pub position: usize,
    /// The repeated share of the window at that moment.
    pub ratio: f64,
}

/// The n-gram state: the latest `ngram` units, their rolling hash, every
/// n-gram seen so far, and the repeated flags of the latest `window` units.
#[derive(Debug)]
struct Grams {
    settings: LoopGuardSettings,
    /// The latest units with the byte offset each starts at.
    recent: VecDeque<(u64, usize)>,
    hash: u64,
    /// BASE^(ngram - 1), to drop the oldest unit from the hash.
    top: u64,
    seen: HashSet<u64>,
    window: VecDeque<bool>,
    repeated: usize,
    units: usize,
    /// Where the current run of repeats began; cleared once the window holds
    /// no repeat.
    run_start: Option<usize>,
}

impl Grams {
    fn new(settings: LoopGuardSettings) -> Self {
        let top = (1..settings.ngram).fold(1u64, |acc, _| acc.wrapping_mul(BASE));
        Self {
            settings,
            recent: VecDeque::with_capacity(settings.ngram),
            hash: 0,
            top,
            seen: HashSet::new(),
            window: VecDeque::with_capacity(settings.window + 1),
            repeated: 0,
            units: 0,
            run_start: None,
        }
    }

    /// Adds one unit starting at byte `offset`; a loop when it completes one.
    fn push(&mut self, value: u64, offset: usize) -> Option<LoopDetected> {
        if self.recent.len() == self.settings.ngram
            && let Some((old, _)) = self.recent.pop_front()
        {
            self.hash = self.hash.wrapping_sub(old.wrapping_mul(self.top));
        }
        self.hash = self.hash.wrapping_mul(BASE).wrapping_add(value);
        self.recent.push_back((value, offset));
        self.units += 1;

        let repeated = self.recent.len() == self.settings.ngram && !self.seen.insert(self.hash);
        self.window.push_back(repeated);
        self.repeated += usize::from(repeated);
        if self.window.len() > self.settings.window && self.window.pop_front() == Some(true) {
            self.repeated -= 1;
        }
        if repeated && self.run_start.is_none() {
            // The repeated n-gram starts at the oldest unit held.
            self.run_start = self.recent.front().map(|(_, at)| *at);
        }
        if self.repeated == 0 {
            self.run_start = None;
        }
        self.judge(offset)
    }

    /// Whether the window now holds a loop.
    fn judge(&self, offset: usize) -> Option<LoopDetected> {
        let full = self.window.len() == self.settings.window;
        if !full || self.units < self.settings.min_output {
            return None;
        }
        // Units are counted in the thousands at most; f64 holds them exactly.
        let ratio = self.repeated as f64 / self.settings.window as f64;
        (ratio >= self.settings.ratio).then(|| LoopDetected {
            loop_start: self.run_start.unwrap_or(offset),
            position: self.units,
            ratio,
        })
    }
}

/// Watches one answer as it arrives.
#[derive(Debug)]
pub struct LoopDetector {
    unit: Unit,
    lines: LineClassifier,
    grams: Grams,
    /// Bytes pushed so far: the offset of the next chunk.
    offset: usize,
    /// The word being read and the byte it started at, for the word unit.
    word: Option<(u64, usize)>,
    released: Vec<(char, usize, bool)>,
}

impl LoopDetector {
    pub fn new(settings: LoopGuardSettings) -> Self {
        Self {
            unit: settings.unit,
            lines: LineClassifier::default(),
            grams: Grams::new(settings),
            offset: 0,
            word: None,
            released: Vec::new(),
        }
    }

    /// Takes the next piece of the answer; the loop, if this piece completes
    /// one. Offsets count from the start of the whole answer.
    pub fn push(&mut self, text: &str) -> Option<LoopDetected> {
        let base = self.offset;
        self.offset += text.len();
        for (i, c) in text.char_indices() {
            self.lines.feed(c, base + i, &mut self.released);
            if let Some(found) = self.drain() {
                return Some(found);
            }
        }
        None
    }

    /// Judges what is still held back at the end of the answer: the start
    /// of an unclassified line and an unfinished word.
    pub fn finish(&mut self) -> Option<LoopDetected> {
        self.lines.flush(&mut self.released);
        self.drain().or_else(|| self.end_word())
    }

    /// Feeds every released character to the unit it belongs to.
    fn drain(&mut self) -> Option<LoopDetected> {
        let released = std::mem::take(&mut self.released);
        let mut found = None;
        for &(c, offset, exempt) in &released {
            found = found.or_else(|| self.unit_of(c, offset, exempt));
        }
        self.released = released;
        self.released.clear();
        found
    }

    /// One character: a unit of its own, or part of a word.
    fn unit_of(&mut self, c: char, offset: usize, exempt: bool) -> Option<LoopDetected> {
        match self.unit {
            Unit::Chars if exempt => None,
            Unit::Chars => self.grams.push(u64::from(c), offset),
            Unit::Words if exempt || c.is_whitespace() => self.end_word(),
            Unit::Words => {
                let (hash, _) = self.word.get_or_insert((FNV_OFFSET, offset));
                *hash = (*hash ^ u64::from(c)).wrapping_mul(FNV_PRIME);
                None
            }
        }
    }

    /// Counts the word being read, if any.
    fn end_word(&mut self) -> Option<LoopDetected> {
        let (hash, start) = self.word.take()?;
        self.grams.push(hash, start)
    }
}
