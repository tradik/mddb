use futures::Stream;
use serde::{Deserialize, Serialize};
use std::pin::Pin;

pub type ChunkStream = Pin<Box<dyn Stream<Item = Result<String, crate::error::AppError>> + Send>>;

/// Tool definition sent to the LLM
#[derive(Debug, Clone, Serialize)]
pub struct ToolDef {
    #[serde(rename = "type")]
    pub tool_type: String,
    pub function: ToolFunction,
}

#[derive(Debug, Clone, Serialize)]
pub struct ToolFunction {
    pub name: String,
    pub description: String,
    pub parameters: serde_json::Value,
}

/// A tool call returned by the LLM
#[derive(Debug, Clone, Deserialize)]
pub struct ToolCall {
    pub id: String,
    pub function: ToolCallFunction,
}

#[derive(Debug, Clone, Deserialize)]
pub struct ToolCallFunction {
    pub name: String,
    pub arguments: String,
}

/// Result of a non-streaming chat call (used in agentic loop)
#[derive(Debug)]
pub enum ChatResponse {
    /// LLM wants to call tools
    ToolCalls { tool_calls: Vec<ToolCall> },
    /// LLM produced a final text response
    Content(String),
}

/// What one call to the model cost (RAG-005).
///
/// `max_turns` caps how many turns a session takes; it says nothing about what
/// they cost. One turn carrying a large RAG context through several
/// tool-calling rounds can be orders of magnitude more expensive than ten short
/// ones, and on a publicly reachable chat that is the dimension a budget
/// actually runs out in.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct TokenUsage {
    pub input: u64,
    pub output: u64,
}

impl TokenUsage {
    pub fn total(&self) -> u64 {
        self.input + self.output
    }
}

impl std::ops::AddAssign for TokenUsage {
    fn add_assign(&mut self, other: Self) {
        // Saturating: a provider reporting nonsense must not wrap the counter
        // round to a small number, which would read as a session that has
        // spent almost nothing.
        self.input = self.input.saturating_add(other.input);
        self.output = self.output.saturating_add(other.output);
    }
}

/// One model call: what it produced, and what it cost.
///
/// The cost travels with the response rather than being fetched separately,
/// because the agentic loop calls the model several times per turn and a
/// budget that counts only the last call is not a budget.
#[derive(Debug)]
pub struct ChatTurn {
    pub response: ChatResponse,
    pub usage: TokenUsage,
}

/// A message in the tool-calling conversation (richer than ChatMessage)
#[derive(Debug, Clone, Serialize)]
pub struct ApiMsg {
    pub role: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub content: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tool_calls: Option<Vec<serde_json::Value>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tool_call_id: Option<String>,
}

#[async_trait::async_trait]
pub trait LlmProvider: Send + Sync {
    /// Send messages with tools, get either tool_calls or content (non-streaming)
    ///
    /// Returns what it cost alongside what it produced; see [`ChatTurn`].
    async fn chat_with_tools(
        &self,
        messages: &[ApiMsg],
        tools: &[ToolDef],
        temperature: f32,
        max_tokens: u32,
    ) -> Result<ChatTurn, crate::error::AppError>;

    /// Stream the final response from tool-calling conversation
    async fn chat_stream_raw(
        &self,
        messages: &[ApiMsg],
        temperature: f32,
        max_tokens: u32,
    ) -> Result<ChunkStream, crate::error::AppError>;
}
