//! A signed join (#405): the host's token is verified and the user's stored
//! conversation opened. The controls such a user has are in controls.rs.
//!
//! A session holds a `ConversationRef` only when its join carried a valid
//! token, and the `sub` in it is the token's; nothing here reads a user id
//! from the client.

use crate::chat::conversation::{self, Choice, Creation};
use crate::chat::handler::Connection;
use crate::chat::outbound::{Sender, send_json};
use crate::chat::scenario;
use crate::error::AppError;
use crate::identity::{self, Claims};
use crate::session::types::WsOutgoing;

/// A `join` as the client sent it.
pub struct JoinRequest {
    pub name: String,
    pub scenario: String,
    pub token: Option<String>,
    pub conversation_id: Option<String>,
    pub new_conversation: bool,
}

impl Connection {
    /// Joins: anonymous as before, or — with a valid token — into the user's
    /// stored conversation. A conversation that cannot be opened gives the
    /// seat back; the visitor is refused as if never admitted.
    pub(super) async fn join_as(
        &mut self,
        out: &mut Sender,
        request: JoinRequest,
    ) -> Result<(), AppError> {
        let state = &self.state;
        let claims = identity::identify(
            &state.config.identity,
            request.token.as_deref(),
            &request.scenario,
        )?;
        if claims.is_none() && request.conversation_id.is_some() {
            return Err(AppError::Unauthorized(
                "conversation_id needs a signed join".to_string(),
            ));
        }
        let name = claims
            .as_ref()
            .and_then(|c| c.name.clone())
            .unwrap_or(request.name.clone());
        let (id, name) = self.admit(out, name, request.scenario.clone()).await?;

        let opened = match &claims {
            Some(claims) => match self.open(&id, claims, &request, &name).await {
                Ok(opened) => Some(opened),
                Err(e) => {
                    self.state.session_manager.remove_session(&id).await;
                    return Err(e);
                }
            },
            None => None,
        };
        let conversation_id = opened.as_ref().map(|o| o.conversation.id.clone());
        self.announce(out, &id, &name, &request.scenario, conversation_id)
            .await;
        if let Some(opened) = opened.filter(|o| !o.entries.is_empty()) {
            let messages = opened.entries;
            send_json(out, &WsOutgoing::History { messages }).await;
        }
        self.session_id = Some(id);
        Ok(())
    }

    /// Opens the stored conversation for session `id` and puts its restored
    /// history in the session.
    async fn open(
        &self,
        id: &str,
        claims: &Claims,
        request: &JoinRequest,
        name: &str,
    ) -> Result<conversation::Opened, AppError> {
        let mut client = self
            .state
            .mddb
            .client()
            .await
            .ok_or(AppError::MddbUnavailable)?;
        let choice = Choice {
            conversation_id: request.conversation_id.as_deref(),
            new_conversation: request.new_conversation,
        };
        let creation = Creation {
            display_name: name,
            retention_days: self.retention_days(&claims.scenario),
        };
        let max_history = self.state.config.session.max_history_length;
        let opened =
            conversation::open(&mut client, claims, &choice, &creation, max_history).await?;
        if let Some(mut session) = self.state.session_manager.get_session_mut(id) {
            session.conversation = Some(opened.conversation.clone());
            session.history = opened.history.clone();
        }
        Ok(opened)
    }

    /// The scenario's retention for new conversations; 0 is mddbd's default.
    pub(super) fn retention_days(&self, scenario_name: &str) -> u32 {
        let state = &self.state;
        scenario::resolve(&state.config, &state.scenarios, scenario_name)
            .active()
            .map_or(0, |s| s.retention_days)
    }
}
