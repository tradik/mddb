//! What one tool call did (#406).

/// What one tool call did: the text for the model and, for the turn log
/// (#406), what it looked for and found.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct ToolRun {
    pub output: String,
    /// A search's query or a document's key; empty for a refused or unknown
    /// call, which looked for nothing.
    pub query: String,
    /// The keys found, best first.
    pub result_keys: Vec<String>,
    /// The best result's score; 0 when nothing was found.
    pub top_score: f64,
}

impl ToolRun {
    pub(super) fn text(output: String) -> Self {
        Self {
            output,
            ..Self::default()
        }
    }
}
