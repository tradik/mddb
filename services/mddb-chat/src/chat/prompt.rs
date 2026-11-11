//! What the model is shown at the start of a turn.

use crate::llm::provider::ApiMsg;
use crate::session::types::{ChatMessage, MessageRole};

/// The instruction appended to every system prompt: the tools exist to be
/// used, and the model is told so rather than left to notice them.
const TOOL_INSTRUCTIONS: &str = "You have access to tools to search the documentation database. \
     Use the search_docs tool to find relevant information before answering questions. \
     Always search first, then answer based on what you find.";

/// The system prompt followed by the conversation so far, in the shape the
/// providers take.
///
/// RAG context is not injected here: the model fetches it through the
/// `search_docs` tool, so what it reads is what it asked for.
pub fn build_api_messages(system_prompt: &str, history: &[ChatMessage]) -> Vec<ApiMsg> {
    let mut messages = Vec::with_capacity(history.len() + 1);
    messages.push(ApiMsg {
        role: "system".to_string(),
        content: Some(format!("{system_prompt}\n\n{TOOL_INSTRUCTIONS}")),
        tool_calls: None,
        tool_call_id: None,
    });
    messages.extend(history.iter().map(|msg| ApiMsg {
        role: role_name(&msg.role).to_string(),
        content: Some(msg.content.clone()),
        tool_calls: None,
        tool_call_id: None,
    }));
    messages
}

fn role_name(role: &MessageRole) -> &'static str {
    match role {
        MessageRole::User => "user",
        MessageRole::Assistant => "assistant",
        MessageRole::System => "system",
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn said(role: MessageRole, content: &str) -> ChatMessage {
        ChatMessage {
            role,
            content: content.to_string(),
            timestamp: 0,
        }
    }

    #[test]
    fn the_system_prompt_comes_first_with_the_tool_instructions() {
        let msgs = build_api_messages("Be terse.", &[]);

        assert_eq!(msgs.len(), 1);
        assert_eq!(msgs[0].role, "system");
        let content = msgs[0].content.as_deref().unwrap();
        assert!(content.starts_with("Be terse.\n\n"));
        assert!(content.contains("search_docs"));
    }

    #[test]
    fn the_history_follows_in_order_with_provider_role_names() {
        let history = [
            said(MessageRole::User, "q"),
            said(MessageRole::Assistant, "a"),
            said(MessageRole::System, "note"),
        ];

        let msgs = build_api_messages("", &history);

        let roles: Vec<&str> = msgs.iter().map(|m| m.role.as_str()).collect();
        assert_eq!(roles, vec!["system", "user", "assistant", "system"]);
        assert_eq!(msgs[1].content.as_deref(), Some("q"));
        assert!(
            msgs.iter()
                .all(|m| m.tool_calls.is_none() && m.tool_call_id.is_none())
        );
    }
}
