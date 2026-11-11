//! A visitor's rating stored with the answer it rates (#406).
//!
//! The `feedback` frame names the answer by the `message_id` its `done`
//! frame carried; mddbd holds that answer under the same id, in a signed
//! user's conversation (#405) or an anonymous session's turn log. A session
//! with neither has nowhere to store a rating, and a rating without an id
//! cannot say which answer it is about: both stay a log line and a webhook.

use tracing::warn;

use crate::chat::handler::Connection;
use crate::session::conversation::ConversationRef;

/// The longest comment stored, in characters; mddbd refuses a longer one.
pub const MAX_COMMENT_CHARS: usize = 2000;

/// What a `feedback` frame rates.
pub struct Rating<'a> {
    pub message_id: Option<&'a str>,
    pub rating: &'a str,
    pub comment: &'a str,
}

impl Connection {
    /// Stores a rating in mddbd. Best effort: a failure is logged.
    pub(super) async fn store_rating(&self, rated: Rating<'_>) {
        let Some(message_id) = rated.message_id.filter(|id| !id.is_empty()) else {
            return;
        };
        let Some(to) = self.rated_conversation() else {
            return;
        };
        let Some(mut client) = self.state.mddb.client().await else {
            warn!("mddbd unavailable; a rating was not stored");
            return;
        };
        let comment: String = rated
            .comment
            .trim()
            .chars()
            .take(MAX_COMMENT_CHARS)
            .collect();
        let stored = client
            .rate_message((&to.id, &to.sub), message_id, rated.rating, &comment)
            .await;
        if let Err(e) = stored {
            warn!(conversation = to.id, error = %e, "could not store a rating");
        }
    }

    /// Where this session's answers are stored: its signed conversation, else
    /// its turn log.
    fn rated_conversation(&self) -> Option<ConversationRef> {
        let session = self
            .session_id
            .as_deref()
            .and_then(|sid| self.state.session_manager.get_session(sid))?;
        session.conversation.clone().or_else(|| session.log.clone())
    }
}
