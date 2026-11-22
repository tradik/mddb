//! The conversation a client sends, as a turn takes it (#448).
//!
//! The client sends the whole conversation every time, so there is no
//! session: the history of the turn is what the request holds, checked
//! against the scenario's limits that make sense without one.
//!
//! - `system` and `developer` messages are dropped: the scenario's composed
//!   prompt is the policy, and a client cannot replace its guardrails.
//! - `tool` messages and assistant messages without text (tool calls of the
//!   client's own) are dropped: the scenario's tools are the only ones.
//! - Content is a string or a list of parts; text parts are joined, any
//!   other part (an image, audio, a file) is refused with 400.
//! - Everything before the question is marked restored, which tells the
//!   model it is context, not instructions (prompt.rs, #405): it came from
//!   the client, and this server cannot vouch for what an assistant turn in
//!   it says.

use crate::chat::scenario::ActiveScenario;
use crate::identity;
use crate::openai_api::wire::{ApiError, Content, RequestMessage};
use crate::session::types::{ChatMessage, MessageRole};
use crate::state::AppState;

use crate::error::AppError;

/// The history of the turn, ending in the question.
#[derive(Debug)]
pub struct Asked {
    pub history: Vec<ChatMessage>,
    pub question: String,
}

/// Reads `messages` for `scenario`, or refuses them.
pub fn read(
    state: &AppState,
    scenario: &ActiveScenario,
    messages: &[RequestMessage],
) -> Result<Asked, ApiError> {
    let mut kept = Vec::new();
    for message in messages {
        let role = match message.role.as_str() {
            "user" => MessageRole::User,
            "assistant" => MessageRole::Assistant,
            "system" | "developer" | "tool" | "function" => continue,
            other => return Err(ApiError::bad_request(format!("unknown role '{other}'"))),
        };
        let Some(text) = text_of(message.content.as_ref())? else {
            continue;
        };
        let text = match role {
            MessageRole::User => state.sanitizer.sanitize_message(&text)?,
            _ => text,
        };
        kept.push((role, text));
    }

    let Some((MessageRole::User, question)) = kept.last().cloned() else {
        return Err(ApiError::bad_request(
            "the last message must be the user's question",
        ));
    };
    check_limits(scenario, &kept)?;

    let max = state.config.session.max_history_length.max(1);
    let skip = kept.len().saturating_sub(max);
    let last = kept.len() - 1;
    let timestamp = identity::now();
    let history = kept
        .into_iter()
        .enumerate()
        .skip(skip)
        .map(|(i, (role, content))| ChatMessage {
            role,
            content,
            timestamp,
            restored: i != last,
        })
        .collect();
    Ok(Asked { history, question })
}

/// `maxTurns` over the user messages sent, and the token budget against
/// the size of the request: its bytes / 4, the estimate mddbd's prompt
/// preview uses. A session counts what the model reports instead; without
/// one, this is what is known before the model is asked.
fn check_limits(scenario: &ActiveScenario, kept: &[(MessageRole, String)]) -> Result<(), AppError> {
    let questions = kept
        .iter()
        .filter(|(role, _)| *role == MessageRole::User)
        .count();
    if !scenario.allows_another_turn(questions - 1) {
        return Err(AppError::TurnLimitReached(
            scenario.max_turns.unwrap_or_default(),
        ));
    }
    let bytes: usize = kept.iter().map(|(_, text)| text.len()).sum();
    let approx_tokens = (bytes / 4) as u64;
    if scenario.token_budget > 0 && approx_tokens > scenario.token_budget {
        return Err(AppError::TokenBudgetExceeded(approx_tokens));
    }
    Ok(())
}

/// The text of a message, `None` when it has none, or a refusal for a part
/// that is not text.
fn text_of(content: Option<&Content>) -> Result<Option<String>, ApiError> {
    let text = match content {
        None => return Ok(None),
        Some(Content::Text(text)) => text.clone(),
        Some(Content::Parts(parts)) => {
            let mut texts = Vec::with_capacity(parts.len());
            for part in parts {
                match (part.kind.as_str(), &part.text) {
                    ("text", Some(text)) => texts.push(text.as_str()),
                    ("text", None) => {}
                    (kind, _) => {
                        return Err(ApiError::bad_request(format!(
                            "only text content is supported; a '{kind}' part is not"
                        )));
                    }
                }
            }
            texts.join("\n")
        }
    };
    Ok(Some(text).filter(|t| !t.trim().is_empty()))
}

#[cfg(test)]
#[path = "conversation_tests.rs"]
mod tests;
