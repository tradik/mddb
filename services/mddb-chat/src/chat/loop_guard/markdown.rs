//! Which lines of an answer the loop guard leaves alone (#413).
//!
//! Code blocks and Markdown tables repeat by nature — the same `|---|`
//! separators, the same config key on ten lines — and must not be cut. Every
//! character of a fenced block (its fence lines included) and of a table row
//! is marked exempt, and the detector does not count it.
//!
//! A line is classified by its first non-blank characters, which a stream may
//! deliver one at a time: until they are known, the line's characters wait.

/// Characters of one line held back until the line is classified.
type Held = Vec<(char, usize)>;

/// Classifies lines as they stream in.
#[derive(Debug, Default)]
pub struct LineClassifier {
    /// Whether the current line is still being classified.
    classified: bool,
    /// Whether the current, classified line is exempt.
    exempt: bool,
    /// Inside a fenced code block.
    in_fence: bool,
    held: Held,
}

/// How a line opens, by its first non-blank characters.
enum Opening {
    /// Not enough seen yet.
    Unknown,
    Fence,
    Table,
    Text,
}

/// Classifies the start of a line: leading blanks are skipped, a `|` opens
/// a table row, three backticks or tildes a fence.
fn opening(held: &Held) -> Opening {
    let rest: Vec<char> = held
        .iter()
        .map(|(c, _)| *c)
        .skip_while(|c| *c == ' ' || *c == '\t')
        .collect();
    let Some(&first) = rest.first() else {
        return Opening::Unknown;
    };
    match first {
        '|' => return Opening::Table,
        '`' | '~' => {}
        _ => return Opening::Text,
    }
    let run = rest.iter().take_while(|c| **c == first).count();
    match (run >= 3, rest.len() > run) {
        (true, _) => Opening::Fence,
        (false, true) => Opening::Text,
        (false, false) => Opening::Unknown,
    }
}

impl LineClassifier {
    /// Takes one character at byte `offset` and appends to `out` every
    /// character whose line is now known, with whether it is exempt.
    pub fn feed(&mut self, c: char, offset: usize, out: &mut Vec<(char, usize, bool)>) {
        if self.classified {
            out.push((c, offset, self.exempt));
            if c == '\n' {
                self.classified = false;
            }
            return;
        }
        self.held.push((c, offset));
        if c == '\n' {
            // A blank line: part of whatever block it is in.
            self.release(self.in_fence, out);
            self.classified = false;
            return;
        }
        match opening(&self.held) {
            Opening::Unknown => {}
            Opening::Table => self.release(true, out),
            Opening::Text => self.release(self.in_fence, out),
            Opening::Fence => {
                self.in_fence = !self.in_fence;
                self.release(true, out);
            }
        }
    }

    /// Releases what is still held at the end of the text, as the block it
    /// is in.
    pub fn flush(&mut self, out: &mut Vec<(char, usize, bool)>) {
        self.release(self.in_fence, out);
    }

    /// Classifies the current line as `exempt` and releases what it held.
    fn release(&mut self, exempt: bool, out: &mut Vec<(char, usize, bool)>) {
        self.classified = true;
        self.exempt = exempt;
        out.extend(self.held.drain(..).map(|(c, o)| (c, o, exempt)));
    }
}
