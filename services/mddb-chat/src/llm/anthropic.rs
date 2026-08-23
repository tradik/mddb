use futures::StreamExt;
use reqwest::Client;
use serde::{Deserialize, Serialize};
use tracing::debug;

use crate::config::LlmConfig;
use crate::error::AppError;
use crate::llm::provider::{
    ApiMsg, ChatResponse, ChatTurn, ChunkStream, LlmProvider, TokenUsage, ToolCall,
    ToolCallFunction, ToolDef,
};

const ANTHROPIC_VERSION: &str = "2023-06-01";

pub struct AnthropicProvider {
    client: Client,
    config: LlmConfig,
}

// --- Request types ---

#[derive(Serialize)]
struct MessagesRequest {
    model: String,
    max_tokens: u32,
    #[serde(skip_serializing_if = "Option::is_none")]
    system: Option<String>,
    messages: Vec<serde_json::Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    tools: Option<Vec<AnthropicTool>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    temperature: Option<f32>,
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    stream: bool,
}

#[derive(Serialize)]
struct AnthropicTool {
    name: String,
    description: String,
    input_schema: serde_json::Value,
}

// --- Response types (non-streaming) ---

#[derive(Deserialize)]
struct MessagesResponse {
    content: Vec<ContentBlock>,
    #[allow(dead_code)]
    stop_reason: Option<String>,
    /// RAG-005. Optional because a budget must not depend on a field the
    /// provider is free to omit: a missing block counts as zero and is logged,
    /// rather than failing the turn over accounting.
    usage: Option<AnthropicUsage>,
}

#[derive(Deserialize)]
struct AnthropicUsage {
    #[serde(default)]
    input_tokens: u64,
    #[serde(default)]
    output_tokens: u64,
}

#[derive(Deserialize)]
#[serde(tag = "type")]
enum ContentBlock {
    #[serde(rename = "text")]
    Text { text: String },
    #[serde(rename = "tool_use")]
    ToolUse {
        id: String,
        name: String,
        input: serde_json::Value,
    },
}

// --- Streaming event types ---

#[derive(Deserialize)]
#[serde(tag = "type")]
enum StreamEvent {
    #[serde(rename = "message_start")]
    MessageStart {
        #[allow(dead_code)]
        message: serde_json::Value,
    },
    #[serde(rename = "content_block_start")]
    ContentBlockStart {
        // RUST-001: neither field is read — the stream is consumed for its
        // text deltas alone. They stay because this enum is the documentation
        // of Anthropic's SSE event shape, and a variant missing half its
        // fields is a worse reference than one carrying them.
        #[allow(dead_code)]
        index: usize,
        #[allow(dead_code)]
        content_block: serde_json::Value,
    },
    #[serde(rename = "content_block_delta")]
    ContentBlockDelta {
        #[allow(dead_code)]
        index: usize,
        delta: DeltaBlock,
    },
    #[serde(rename = "content_block_stop")]
    ContentBlockStop {
        #[allow(dead_code)]
        index: usize,
    },
    #[serde(rename = "message_delta")]
    MessageDelta {
        #[allow(dead_code)]
        delta: serde_json::Value,
    },
    #[serde(rename = "message_stop")]
    MessageStop,
    #[serde(rename = "ping")]
    Ping,
    #[serde(rename = "error")]
    Error { error: AnthropicError },
}

#[derive(Deserialize)]
#[serde(tag = "type")]
enum DeltaBlock {
    #[serde(rename = "text_delta")]
    TextDelta { text: String },
    #[serde(rename = "input_json_delta")]
    InputJsonDelta {
        #[allow(dead_code)]
        partial_json: String,
    },
}

#[derive(Deserialize)]
struct AnthropicError {
    message: String,
}

impl AnthropicProvider {
    pub fn new(config: LlmConfig) -> Self {
        let client = Client::new();
        Self { client, config }
    }

    fn api_url(&self) -> String {
        format!("{}/messages", self.config.api_url.trim_end_matches('/'))
    }

    fn request_builder(&self, req: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
        req.header("x-api-key", &self.config.api_key)
            .header("anthropic-version", ANTHROPIC_VERSION)
            .header("content-type", "application/json")
    }

    /// Extract system prompt from messages and return (system, filtered_messages)
    fn extract_system(messages: &[ApiMsg]) -> (Option<String>, Vec<&ApiMsg>) {
        let mut system = None;
        let mut filtered = Vec::new();
        for msg in messages {
            if msg.role == "system" {
                // Concatenate system messages
                let text = msg.content.clone().unwrap_or_default();
                system = Some(match system {
                    Some(existing) => format!("{}\n\n{}", existing, text),
                    None => text,
                });
            } else {
                filtered.push(msg);
            }
        }
        (system, filtered)
    }

    /// Convert ApiMsg list to Anthropic message format
    /// Handles tool_calls (assistant) and tool results (tool role)
    fn convert_messages(messages: &[&ApiMsg]) -> Vec<serde_json::Value> {
        let mut result = Vec::new();

        for msg in messages {
            match msg.role.as_str() {
                "user" => {
                    result.push(serde_json::json!({
                        "role": "user",
                        "content": msg.content.clone().unwrap_or_default(),
                    }));
                }
                "assistant" => {
                    if let Some(tool_calls) = &msg.tool_calls {
                        // Assistant message with tool_use blocks
                        let mut content: Vec<serde_json::Value> = Vec::new();

                        // Add text if present
                        if let Some(text) = &msg.content
                            && !text.is_empty()
                        {
                            content.push(serde_json::json!({
                                "type": "text",
                                "text": text,
                            }));
                        }

                        // Convert OpenAI-format tool_calls to Anthropic tool_use blocks
                        for tc in tool_calls {
                            let id = tc["id"].as_str().unwrap_or("");
                            let name = tc["function"]["name"].as_str().unwrap_or("");
                            let args_str = tc["function"]["arguments"].as_str().unwrap_or("{}");
                            let input: serde_json::Value =
                                serde_json::from_str(args_str).unwrap_or(serde_json::json!({}));

                            content.push(serde_json::json!({
                                "type": "tool_use",
                                "id": id,
                                "name": name,
                                "input": input,
                            }));
                        }

                        result.push(serde_json::json!({
                            "role": "assistant",
                            "content": content,
                        }));
                    } else {
                        result.push(serde_json::json!({
                            "role": "assistant",
                            "content": msg.content.clone().unwrap_or_default(),
                        }));
                    }
                }
                "tool" => {
                    // Anthropic expects tool_result as user message content block
                    let tool_result = serde_json::json!({
                        "type": "tool_result",
                        "tool_use_id": msg.tool_call_id.clone().unwrap_or_default(),
                        "content": msg.content.clone().unwrap_or_default(),
                    });

                    // Merge consecutive tool results into one user message
                    if let Some(last) = result.last_mut()
                        && last["role"] == "user"
                        && let Some(arr) = last["content"].as_array_mut()
                    {
                        // Already an array of tool_results, append
                        arr.push(tool_result);
                        continue;
                    }

                    result.push(serde_json::json!({
                        "role": "user",
                        "content": [tool_result],
                    }));
                }
                _ => {
                    // Skip unknown roles
                }
            }
        }

        result
    }

    /// Convert ToolDef (OpenAI format) to Anthropic tool format
    fn convert_tools(tools: &[ToolDef]) -> Vec<AnthropicTool> {
        tools
            .iter()
            .map(|t| AnthropicTool {
                name: t.function.name.clone(),
                description: t.function.description.clone(),
                input_schema: t.function.parameters.clone(),
            })
            .collect()
    }

    fn build_sse_stream(&self, response: reqwest::Response) -> ChunkStream {
        let byte_stream = response.bytes_stream();

        let stream = futures::stream::unfold(
            (byte_stream, String::new()),
            |(mut byte_stream, mut buffer)| async move {
                loop {
                    if let Some(newline_pos) = buffer.find('\n') {
                        let line = buffer[..newline_pos].trim().to_string();
                        buffer = buffer[newline_pos + 1..].to_string();

                        if line.is_empty() || !line.starts_with("data: ") {
                            // Skip event: lines and empty lines
                            continue;
                        }

                        let data = &line["data: ".len()..];

                        match serde_json::from_str::<StreamEvent>(data) {
                            Ok(event) => match event {
                                StreamEvent::ContentBlockDelta { delta, .. } => {
                                    if let DeltaBlock::TextDelta { text } = delta
                                        && !text.is_empty()
                                    {
                                        return Some((Ok(text), (byte_stream, buffer)));
                                    }
                                    continue;
                                }
                                StreamEvent::MessageStop => return None,
                                StreamEvent::Error { error } => {
                                    return Some((
                                        Err(AppError::LlmError(error.message)),
                                        (byte_stream, buffer),
                                    ));
                                }
                                _ => continue,
                            },
                            Err(_) => continue,
                        }
                    }

                    match byte_stream.next().await {
                        Some(Ok(bytes)) => {
                            buffer.push_str(&String::from_utf8_lossy(&bytes));
                        }
                        Some(Err(e)) => {
                            return Some((
                                Err(AppError::LlmError(e.to_string())),
                                (byte_stream, buffer),
                            ));
                        }
                        None => return None,
                    }
                }
            },
        );

        Box::pin(stream)
    }
}

#[async_trait::async_trait]
impl LlmProvider for AnthropicProvider {
    async fn chat_with_tools(
        &self,
        messages: &[ApiMsg],
        tools: &[ToolDef],
        temperature: f32,
        max_tokens: u32,
    ) -> Result<ChatTurn, AppError> {
        let (system, filtered) = Self::extract_system(messages);
        let converted = Self::convert_messages(&filtered);

        let anthropic_tools = if tools.is_empty() {
            None
        } else {
            Some(Self::convert_tools(tools))
        };

        let request = MessagesRequest {
            model: self.config.model.clone(),
            max_tokens,
            system,
            messages: converted,
            tools: anthropic_tools,
            temperature: Some(temperature),
            stream: false,
        };

        let req = self.client.post(self.api_url()).json(&request);
        let req = self.request_builder(req);

        let response = req
            .send()
            .await
            .map_err(|e| AppError::LlmError(e.to_string()))?;

        if !response.status().is_success() {
            let status = response.status();
            let body = response
                .text()
                .await
                .unwrap_or_else(|_| "unknown".to_string());
            return Err(AppError::LlmError(format!(
                "Anthropic API {status}: {body}"
            )));
        }

        let resp: MessagesResponse = response
            .json()
            .await
            .map_err(|e| AppError::LlmError(format!("failed to parse response: {e}")))?;

        // Check for tool_use blocks
        let mut tool_calls = Vec::new();
        let mut text_parts = Vec::new();

        for block in &resp.content {
            match block {
                ContentBlock::ToolUse { id, name, input } => {
                    tool_calls.push(ToolCall {
                        id: id.clone(),
                        function: ToolCallFunction {
                            name: name.clone(),
                            arguments: serde_json::to_string(input).unwrap_or_default(),
                        },
                    });
                }
                ContentBlock::Text { text } => {
                    text_parts.push(text.clone());
                }
            }
        }

        let usage = match &resp.usage {
            Some(u) => TokenUsage {
                input: u.input_tokens,
                output: u.output_tokens,
            },
            None => {
                debug!("Anthropic returned no usage block; this call counts as zero");
                TokenUsage::default()
            }
        };

        let response = if !tool_calls.is_empty() {
            debug!(count = tool_calls.len(), "Anthropic returned tool calls");
            ChatResponse::ToolCalls { tool_calls }
        } else {
            ChatResponse::Content(text_parts.join(""))
        };

        Ok(ChatTurn { response, usage })
    }

    async fn chat_stream_raw(
        &self,
        messages: &[ApiMsg],
        temperature: f32,
        max_tokens: u32,
    ) -> Result<ChunkStream, AppError> {
        let (system, filtered) = Self::extract_system(messages);
        let converted = Self::convert_messages(&filtered);

        let request = MessagesRequest {
            model: self.config.model.clone(),
            max_tokens,
            system,
            messages: converted,
            tools: None,
            temperature: Some(temperature),
            stream: true,
        };

        let req = self.client.post(self.api_url()).json(&request);
        let req = self.request_builder(req);

        let response = req
            .send()
            .await
            .map_err(|e| AppError::LlmError(e.to_string()))?;

        if !response.status().is_success() {
            let status = response.status();
            let body = response
                .text()
                .await
                .unwrap_or_else(|_| "unknown".to_string());
            return Err(AppError::LlmError(format!(
                "Anthropic API {status}: {body}"
            )));
        }

        Ok(self.build_sse_stream(response))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // RAG-005: the usage block is the whole measurement path, and until now
    // neither provider parsed it. These tests pin the shape each one sends,
    // because a field renamed upstream would otherwise show up as a session
    // that costs nothing.

    #[test]
    fn usage_is_read_from_the_response() {
        let body = r#"{
            "content": [{"type": "text", "text": "hello"}],
            "stop_reason": "end_turn",
            "usage": {"input_tokens": 1234, "output_tokens": 56}
        }"#;

        let parsed: MessagesResponse = serde_json::from_str(body).expect("parse");
        let usage = parsed.usage.expect("usage block");

        assert_eq!(usage.input_tokens, 1234);
        assert_eq!(usage.output_tokens, 56);
    }

    #[test]
    fn a_response_without_usage_still_parses() {
        // A budget must not depend on a field the provider is free to omit:
        // the turn succeeds and simply contributes nothing.
        let body = r#"{"content": [{"type": "text", "text": "hi"}]}"#;

        let parsed: MessagesResponse = serde_json::from_str(body).expect("parse");
        assert!(parsed.usage.is_none());
    }

    #[test]
    fn a_partial_usage_block_counts_what_it_reports() {
        let body = r#"{
            "content": [],
            "usage": {"output_tokens": 7}
        }"#;

        let parsed: MessagesResponse = serde_json::from_str(body).expect("parse");
        let usage = parsed.usage.expect("usage block");

        assert_eq!(usage.input_tokens, 0);
        assert_eq!(usage.output_tokens, 7);
    }
}
