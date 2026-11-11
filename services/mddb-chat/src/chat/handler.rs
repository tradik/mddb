//! The WebSocket loop: frames in, messages dispatched, session let go of at
//! the end.
//!
//! The rest of a connection's behaviour lives next door: `lifecycle.rs`
//! (join, resume, end, detach), `turn.rs` (one message through the
//! tool-calling loop), `prompt.rs` and `outbound.rs`.

use std::sync::Arc;

use axum::extract::ws::{Message, WebSocket};
use futures::StreamExt;
use tracing::{info, warn};

use crate::chat::outbound::{Sender, send_error, send_frame, send_json};
use crate::chat::signed::JoinRequest;
use crate::chat::{tools, turn};
use crate::error::AppError;
use crate::grpc::client::{MddbClient, SearchResult, proto};
use crate::session::types::{WsIncoming, WsOutgoing};
use crate::state::AppState;
use crate::webhook::types::WebhookPayload;

/// The real database behind the tools. Lives here, where the concrete gRPC
/// client meets the tool layer; tools.rs itself only knows the trait, which is
/// what lets its tests run without mddbd.
#[async_trait::async_trait]
impl tools::DocSearch for MddbClient {
    async fn search(
        &mut self,
        query: &str,
        collection: &str,
    ) -> Result<Vec<SearchResult>, AppError> {
        MddbClient::search(self, query, collection).await
    }

    async fn call_mcp_tool(
        &mut self,
        request: proto::CallChatMcpToolRequest,
        deadline: std::time::Duration,
    ) -> Result<proto::CallChatMcpToolResponse, AppError> {
        self.call_chat_mcp_tool(request, deadline).await
    }
}

/// Serves one upgraded WebSocket until it closes.
///
/// When the frames run out — the client closed, or the network did — the
/// session is detached, not removed: it stays resumable until its TTL (#402).
pub async fn handle_ws(socket: WebSocket, state: Arc<AppState>, client_ip: String) {
    let (mut out, mut incoming) = socket.split();
    let mut connection = Connection {
        state,
        client_ip,
        session_id: None,
    };

    while let Some(frame) = incoming.next().await {
        let text = match frame {
            Ok(Message::Text(text)) => text,
            Ok(Message::Close(_)) => break,
            Ok(Message::Ping(_)) => {
                send_frame(&mut out, Message::Pong(vec![].into())).await;
                continue;
            }
            Ok(_) => continue,
            Err(e) => {
                warn!(ip = connection.client_ip, error = %e, "websocket error");
                break;
            }
        };
        connection.handle_text(&text, &mut out).await;
    }

    connection.detach_session().await;
}

/// One visitor's connection and the session it currently holds, if any.
pub struct Connection {
    pub(super) state: Arc<AppState>,
    pub(super) client_ip: String,
    pub(super) session_id: Option<String>,
}

impl Connection {
    async fn handle_text(&mut self, text: &str, out: &mut Sender) {
        match serde_json::from_str::<WsIncoming>(text) {
            Ok(incoming) => self.dispatch(incoming, out).await,
            Err(e) => send_error(out, &format!("invalid message: {e}")).await,
        }
    }

    async fn dispatch(&mut self, incoming: WsIncoming, out: &mut Sender) {
        match incoming {
            WsIncoming::Join {
                name,
                scenario,
                token,
                conversation_id,
                new_conversation,
            } => {
                let request = JoinRequest {
                    name,
                    scenario,
                    token,
                    conversation_id,
                    new_conversation,
                };
                self.join(out, request).await
            }
            WsIncoming::Message { content } => self.message(out, &content).await,
            WsIncoming::Resume { session_id } => self.resume(out, session_id).await,
            WsIncoming::Feedback {
                rating,
                question,
                answer,
                message_id,
            } => self.feedback(&rating, &question, &answer, message_id.as_deref()),
            WsIncoming::End => self.end(out).await,
            WsIncoming::Ping => send_json(out, &WsOutgoing::Pong).await,
            WsIncoming::ListConversations => self.list_conversations(out).await,
            WsIncoming::DeleteConversations { confirm } => {
                self.delete_conversations(out, confirm).await
            }
        }
    }

    async fn message(&mut self, out: &mut Sender, content: &str) {
        let Some(sid) = self.session_id.as_deref() else {
            send_error(out, "must join first").await;
            return;
        };
        if !self.state.rate_limiter.check(&self.client_ip) {
            send_error(out, "rate limited — please slow down").await;
            return;
        }
        if let Err(e) = turn::handle_message(&self.state, out, sid, content).await {
            send_error(out, &e.to_string()).await;
        }
    }

    /// Feedback is logged, and forwarded as a webhook when there is a session
    /// to attribute it to.
    fn feedback(&self, rating: &str, question: &str, answer: &str, message_id: Option<&str>) {
        let sid_str = self.session_id.as_deref().unwrap_or("unknown");
        let session = self
            .session_id
            .as_deref()
            .and_then(|sid| self.state.session_manager.get_session(sid))
            .map(|s| (s.name.clone(), s.scenario.clone()));
        let user_name = session.as_ref().map_or_else(String::new, |s| s.0.clone());

        // #399: forwarded only when there is a session to attribute it to; a
        // rating from nobody is nothing a host app can analyse.
        if let Some((_, scenario_name)) = &session {
            self.state
                .webhook_dispatcher
                .dispatch(WebhookPayload::feedback(
                    sid_str,
                    scenario_name,
                    message_id,
                    rating,
                ));
        }

        if rating == "down" {
            warn!(
                session_id = sid_str,
                user = user_name,
                question = question,
                answer = answer,
                "NEGATIVE_FEEDBACK: bad response reported"
            );
        } else {
            info!(
                session_id = sid_str,
                user = user_name,
                "positive feedback received"
            );
        }
    }
}
