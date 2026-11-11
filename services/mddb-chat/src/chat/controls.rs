//! What a signed-in user controls (#405): listing and deleting their
//! conversations, and ending one. Each needs a session opened by a signed
//! join; the `sub` used is the one its token carried.

use tracing::warn;

use crate::chat::conversation::LIST_LIMIT;
use crate::chat::handler::Connection;
use crate::chat::outbound::{Sender, send_error, send_json};
use crate::error::AppError;
use crate::grpc::client::{MddbClient, proto};
use crate::session::conversation::{ConversationRef, ConversationSummary};
use crate::session::types::WsOutgoing;

/// A stored conversation as a `conversations` frame lists it.
fn summary(c: proto::ChatConversationProto) -> ConversationSummary {
    ConversationSummary {
        id: c.id,
        title: c.title,
        created_at: c.created_at,
        last_message_at: c.last_message_at,
        message_count: c.message_count,
        ended: c.ended,
    }
}

/// What a signed session's controls work with, copied out of the session so
/// no lock is held across a call to mddbd.
struct Signed {
    conversation: ConversationRef,
    scenario: String,
    name: String,
    client: MddbClient,
}

impl Connection {
    /// This connection's signed session, or why there is none.
    async fn signed(&self) -> Result<Signed, AppError> {
        let held = self
            .session_id
            .as_deref()
            .and_then(|sid| self.state.session_manager.get_session(sid))
            .and_then(|s| Some((s.conversation.clone()?, s.scenario.clone(), s.name.clone())));
        let (conversation, scenario, name) =
            held.ok_or_else(|| AppError::Unauthorized("needs a signed join".to_string()))?;
        let client = self
            .state
            .mddb
            .client()
            .await
            .ok_or(AppError::MddbUnavailable)?;
        Ok(Signed {
            conversation,
            scenario,
            name,
            client,
        })
    }

    /// Answers `list_conversations`: the user's conversations in this
    /// session's scenario, newest first.
    pub(super) async fn list_conversations(&self, out: &mut Sender) {
        let listed = async {
            let mut signed = self.signed().await?;
            let sub = &signed.conversation.sub;
            signed
                .client
                .list_conversations(sub, &signed.scenario, LIST_LIMIT)
                .await
        };
        match listed.await {
            Ok(listed) => {
                let conversations = listed.into_iter().map(summary).collect();
                send_json(out, &WsOutgoing::Conversations { conversations }).await;
            }
            Err(e) => send_error(out, &e.to_string()).await,
        }
    }

    /// Answers `delete_conversations`: every conversation of the user goes,
    /// in every scenario, and the session continues in a fresh one.
    pub(super) async fn delete_conversations(&self, out: &mut Sender, confirm: bool) {
        match self.delete_all(confirm).await {
            Ok((deleted, conversation_id)) => {
                let frame = WsOutgoing::ConversationsDeleted {
                    deleted,
                    conversation_id,
                };
                send_json(out, &frame).await;
            }
            Err(e) => send_error(out, &e.to_string()).await,
        }
    }

    async fn delete_all(&self, confirm: bool) -> Result<(i32, String), AppError> {
        let Signed {
            conversation,
            scenario,
            name,
            mut client,
        } = self.signed().await?;
        if !confirm {
            return Err(AppError::InvalidMessage(
                "delete_conversations needs \"confirm\": true".to_string(),
            ));
        }
        let sub = conversation.sub;
        let deleted = client.delete_user_conversations(&sub).await?;
        let retention = self.retention_days(&scenario);
        let fresh = client
            .create_conversation(&sub, &scenario, &name, retention)
            .await?;
        let sid = self.session_id.as_deref().unwrap_or_default();
        if let Some(mut session) = self.state.session_manager.get_session_mut(sid) {
            session.conversation = Some(ConversationRef {
                sub,
                id: fresh.id.clone(),
            });
            session.history.clear();
        }
        Ok((deleted, fresh.id))
    }

    /// Marks a signed session's conversation ended in mddbd. Best effort: the
    /// session ends either way.
    pub(super) async fn end_conversation(&self, session_id: &str) {
        let conversation = self.conversation_of(session_id);
        if let (Some(conversation), Some(mut client)) =
            (conversation, self.state.mddb.client().await)
            && let Err(e) = client
                .end_conversation(&conversation.id, &conversation.sub)
                .await
        {
            warn!(conversation = conversation.id, error = %e, "could not end a stored conversation");
        }
    }

    fn conversation_of(&self, session_id: &str) -> Option<ConversationRef> {
        self.state
            .session_manager
            .get_session(session_id)
            .and_then(|s| s.conversation.clone())
    }

    /// The stored conversation of a session, for the `session` frame of a
    /// resume.
    pub(super) fn conversation_id_of(&self, session_id: &str) -> Option<String> {
        self.conversation_of(session_id).map(|c| c.id)
    }
}
