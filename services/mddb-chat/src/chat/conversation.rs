//! A signed session's conversation in mddbd (#405): which one a join opens,
//! what of it the model and the client get back, and how a turn is written.
//!
//! The `sub` passed to mddbd always comes from a verified token (through a
//! `ConversationRef` or `Claims`); mddbd answers "not found" for any
//! conversation of another sub, so a wrong id cannot cross users either.

use tracing::warn;

use crate::config::LlmConfig;
use crate::error::AppError;
use crate::grpc::client::{MddbClient, proto};
use crate::identity::Claims;
use crate::llm::provider::TokenUsage;
use crate::session::conversation::{ConversationRef, HistoryEntry};
use crate::session::types::{ChatMessage, MessageRole};
use crate::webhook::types::ToolCallRecord;

/// How many of the user's newest conversations a resume looks through for
/// one that is still open: mddbd's largest page.
const RESUME_SCAN: i32 = 200;

/// How many conversations `list_conversations` answers with, newest first.
pub const LIST_LIMIT: i32 = 50;

/// Which stored conversation a signed join asks for.
pub struct Choice<'a> {
    /// That one; wins over `new_conversation`.
    pub conversation_id: Option<&'a str>,
    /// A fresh one; otherwise the newest open one, or a fresh one if none.
    pub new_conversation: bool,
}

/// What a fresh conversation is created with.
pub struct Creation<'a> {
    pub display_name: &'a str,
    /// 0 is mddbd's default.
    pub retention_days: u32,
}

/// An opened conversation: where the session writes, and what was restored —
/// for the model, and for the client's `history` frame.
pub struct Opened {
    pub conversation: ConversationRef,
    pub history: Vec<ChatMessage>,
    pub entries: Vec<HistoryEntry>,
}

/// Opens the conversation `choice` names for `claims`' user, with the last
/// `max_history` messages restored.
pub async fn open(
    client: &mut MddbClient,
    claims: &Claims,
    choice: &Choice<'_>,
    creation: &Creation<'_>,
    max_history: usize,
) -> Result<Opened, AppError> {
    let (sub, scenario) = (claims.sub.as_str(), claims.scenario.as_str());
    let existing = match (choice.conversation_id, choice.new_conversation) {
        (Some(id), _) => Some(id.to_string()),
        (None, true) => None,
        (None, false) => latest_open(client, sub, scenario).await?,
    };
    let conversation = match existing {
        Some(id) => reopen(client, &id, sub, scenario, max_history).await?,
        None => {
            client
                .create_conversation(
                    sub,
                    scenario,
                    creation.display_name,
                    creation.retention_days,
                )
                .await?
        }
    };
    let (history, entries) = restore(&conversation.messages);
    Ok(Opened {
        conversation: ConversationRef {
            sub: sub.to_string(),
            id: conversation.id,
        },
        history,
        entries,
    })
}

/// The id of the user's newest conversation in `scenario` that has not ended.
async fn latest_open(
    client: &mut MddbClient,
    sub: &str,
    scenario: &str,
) -> Result<Option<String>, AppError> {
    let listed = client
        .list_conversations(sub, scenario, RESUME_SCAN)
        .await
        .map_err(store_error)?;
    Ok(listed.into_iter().find(|c| !c.ended).map(|c| c.id))
}

/// A stored conversation of `sub` in `scenario` that may take more messages.
/// One of another scenario is "not found", like one of another user.
async fn reopen(
    client: &mut MddbClient,
    id: &str,
    sub: &str,
    scenario: &str,
    max_history: usize,
) -> Result<proto::ChatConversationProto, AppError> {
    let conversation = client
        .get_conversation(id, sub, max_history)
        .await
        .map_err(store_error)?;
    match (conversation.scenario == scenario, conversation.ended) {
        (false, _) => Err(AppError::ConversationNotFound),
        (true, true) => Err(AppError::ConversationEnded),
        (true, false) => Ok(conversation),
    }
}

/// mddbd's NOT_FOUND as the error the client reads; anything else as it came.
fn store_error(e: AppError) -> AppError {
    match e {
        AppError::GrpcError(status) if status.code() == tonic::Code::NotFound => {
            AppError::ConversationNotFound
        }
        other => other,
    }
}

/// The user and assistant turns of stored messages, marked as restored, and
/// the same turns as the client is shown them. Tool messages stay in mddbd.
fn restore(messages: &[proto::ChatMessageProto]) -> (Vec<ChatMessage>, Vec<HistoryEntry>) {
    messages
        .iter()
        .filter_map(|m| {
            let role = match m.role.as_str() {
                "user" => MessageRole::User,
                "assistant" => MessageRole::Assistant,
                _ => return None,
            };
            let message = ChatMessage {
                role,
                content: m.content.clone(),
                timestamp: u64::try_from(m.created_at).unwrap_or(0),
                restored: true,
            };
            let entry = HistoryEntry {
                role: m.role.clone(),
                content: m.content.clone(),
            };
            Some((message, entry))
        })
        .unzip()
}

/// Appends one message. A failure is logged and swallowed: the visitor's
/// conversation goes on, and only its stored copy misses the message.
pub async fn record(
    client: &mut MddbClient,
    to: &ConversationRef,
    message: proto::ChatMessageProto,
) {
    if let Err(e) = client.append_messages(&to.id, &to.sub, vec![message]).await {
        warn!(conversation = to.id, error = %e, "could not store a chat message");
    }
}

/// The visitor's message as stored.
pub fn user_message(content: &str) -> proto::ChatMessageProto {
    proto::ChatMessageProto {
        role: "user".to_string(),
        content: content.to_string(),
        ..Default::default()
    }
}

/// The final answer as stored: the tools called on the way (name and
/// collection), the model that answered and what the turn cost.
///
/// MCP-005: the stored tool call has no field for a server, so an external
/// tool is stored by name as `mcp:<server>/<tool>`, without a collection.
pub fn answer_message(
    text: &str,
    tools: &[ToolCallRecord],
    llm: &LlmConfig,
    spent: TokenUsage,
) -> proto::ChatMessageProto {
    proto::ChatMessageProto {
        role: "assistant".to_string(),
        content: text.to_string(),
        tool_calls: tools
            .iter()
            .map(|t| proto::ChatToolCallProto {
                name: stored_tool_name(t),
                collection: t.collection.clone(),
                duration_ms: 0,
            })
            .collect(),
        model: llm.model.clone(),
        provider: llm.provider.clone(),
        prompt_tokens: i64::try_from(spent.input).unwrap_or(i64::MAX),
        completion_tokens: i64::try_from(spent.output).unwrap_or(i64::MAX),
        ..Default::default()
    }
}

/// How a tool call is named in a stored conversation: as called for a
/// built-in tool, `mcp:<server>/<tool>` for an external one (MCP-005).
fn stored_tool_name(record: &ToolCallRecord) -> String {
    match &record.server {
        Some(server) => format!("mcp:{server}/{}", record.name),
        None => record.name.clone(),
    }
}

#[cfg(test)]
#[path = "conversation_tests.rs"]
mod tests;
