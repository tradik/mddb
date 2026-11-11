//! The parts of the protocol that exist for a signed-in user (#405): which
//! stored conversation a session writes to, and what a client is shown of
//! the stored ones.

use serde::{Deserialize, Serialize};

/// The conversation in mddbd a signed session writes to.
///
/// `sub` comes from a verified join token and nowhere else, so holding one
/// of these is what "this session has a verified identity" means.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConversationRef {
    pub sub: String,
    pub id: String,
}

/// One restored message, as the `history` frame shows it.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct HistoryEntry {
    pub role: String,
    pub content: String,
}

/// One stored conversation in a `conversations` frame. Times are Unix
/// seconds, as mddbd keeps them.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ConversationSummary {
    pub id: String,
    pub title: String,
    pub created_at: i64,
    pub last_message_at: i64,
    pub message_count: i32,
    pub ended: bool,
}
