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
    async fn chat_with_tools(
        &self,
        messages: &[ApiMsg],
        tools: &[ToolDef],
        temperature: f32,
        max_tokens: u32,
    ) -> Result<ChatResponse, crate::error::AppError>;

    /// Stream the final response from tool-calling conversation
    async fn chat_stream_raw(
        &self,
        messages: &[ApiMsg],
        temperature: f32,
        max_tokens: u32,
    ) -> Result<ChunkStream, crate::error::AppError>;
}
