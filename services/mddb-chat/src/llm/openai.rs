use futures::StreamExt;
use reqwest::Client;
use serde::{Deserialize, Serialize};

use crate::config::LlmConfig;
use crate::error::AppError;
use crate::llm::provider::{
    ApiMsg, ChatResponse, ChatTurn, ChunkStream, LlmProvider, TokenUsage, ToolCall, ToolDef,
};
use tracing::debug;

pub struct OpenAiProvider {
    client: Client,
    config: LlmConfig,
}

// --- Request types ---

#[derive(Serialize)]
struct ChatRequest {
    model: String,
    messages: Vec<serde_json::Value>,
    max_tokens: u32,
    temperature: f32,
    stream: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    tools: Option<Vec<ToolDef>>,
}

// --- Response types (non-streaming) ---

#[derive(Deserialize)]
struct ChatCompletion {
    choices: Vec<CompletionChoice>,
    /// RAG-005. Optional for the same reason as Anthropic's: a budget must not
    /// depend on a field the provider may omit.
    usage: Option<OpenAiUsage>,
}

#[derive(Deserialize)]
struct OpenAiUsage {
    #[serde(default)]
    prompt_tokens: u64,
    #[serde(default)]
    completion_tokens: u64,
}

#[derive(Deserialize)]
struct CompletionChoice {
    message: CompletionMessage,
}

#[derive(Deserialize)]
struct CompletionMessage {
    content: Option<String>,
    tool_calls: Option<Vec<ToolCall>>,
}

// --- Response types (streaming) ---

#[derive(Deserialize)]
struct StreamChunk {
    choices: Vec<StreamChoice>,
}

#[derive(Deserialize)]
struct StreamChoice {
    delta: StreamDelta,
    finish_reason: Option<String>,
}

#[derive(Deserialize)]
struct StreamDelta {
    content: Option<String>,
}

impl OpenAiProvider {
    pub fn new(config: LlmConfig) -> Self {
        let client = Client::new();
        Self { client, config }
    }

    fn api_url(&self) -> String {
        format!(
            "{}/chat/completions",
            self.config.api_url.trim_end_matches('/')
        )
    }

    fn auth_header(&self, req: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
        if !self.config.api_key.is_empty() {
            req.header("Authorization", format!("Bearer {}", self.config.api_key))
        } else {
            req
        }
    }

    /// Convert ApiMsg to JSON values (preserves tool_calls and tool_call_id)
    fn api_messages(messages: &[ApiMsg]) -> Vec<serde_json::Value> {
        messages
            .iter()
            .map(|m| {
                let mut obj = serde_json::json!({ "role": m.role });
                if let Some(content) = &m.content {
                    obj["content"] = serde_json::Value::String(content.clone());
                }
                if let Some(tool_calls) = &m.tool_calls {
                    obj["tool_calls"] = serde_json::Value::Array(tool_calls.clone());
                }
                if let Some(tool_call_id) = &m.tool_call_id {
                    obj["tool_call_id"] = serde_json::Value::String(tool_call_id.clone());
                }
                obj
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
                            continue;
                        }

                        let data = &line["data: ".len()..];
                        if data == "[DONE]" {
                            return None;
                        }

                        match serde_json::from_str::<StreamChunk>(data) {
                            Ok(chunk) => {
                                if let Some(choice) = chunk.choices.first() {
                                    if let Some(content) = &choice.delta.content
                                        && !content.is_empty()
                                    {
                                        return Some((Ok(content.clone()), (byte_stream, buffer)));
                                    }
                                    if choice.finish_reason.is_some() {
                                        return None;
                                    }
                                }
                                continue;
                            }
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
impl LlmProvider for OpenAiProvider {
    async fn chat_with_tools(
        &self,
        messages: &[ApiMsg],
        tools: &[ToolDef],
        temperature: f32,
        max_tokens: u32,
    ) -> Result<ChatTurn, AppError> {
        let request = ChatRequest {
            model: self.config.model.clone(),
            messages: Self::api_messages(messages),
            max_tokens,
            temperature,
            stream: false,
            tools: if tools.is_empty() {
                None
            } else {
                Some(tools.to_vec())
            },
        };

        let req = self.client.post(self.api_url()).json(&request);
        let req = self.auth_header(req);

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
            return Err(AppError::LlmError(format!("LLM API {status}: {body}")));
        }

        let completion: ChatCompletion = response
            .json()
            .await
            .map_err(|e| AppError::LlmError(format!("failed to parse response: {e}")))?;

        let usage = match &completion.usage {
            Some(u) => TokenUsage {
                input: u.prompt_tokens,
                output: u.completion_tokens,
            },
            None => {
                debug!("OpenAI returned no usage block; this call counts as zero");
                TokenUsage::default()
            }
        };

        let choice = completion
            .choices
            .into_iter()
            .next()
            .ok_or_else(|| AppError::LlmError("no choices in response".to_string()))?;

        let response = match choice.message.tool_calls {
            Some(tool_calls) if !tool_calls.is_empty() => ChatResponse::ToolCalls { tool_calls },
            _ => ChatResponse::Content(choice.message.content.unwrap_or_default()),
        };

        Ok(ChatTurn { response, usage })
    }

    async fn chat_stream_raw(
        &self,
        messages: &[ApiMsg],
        temperature: f32,
        max_tokens: u32,
    ) -> Result<ChunkStream, AppError> {
        let request = ChatRequest {
            model: self.config.model.clone(),
            messages: Self::api_messages(messages),
            max_tokens,
            temperature,
            stream: true,
            tools: None,
        };

        let req = self.client.post(self.api_url()).json(&request);
        let req = self.auth_header(req);

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
            return Err(AppError::LlmError(format!("LLM API {status}: {body}")));
        }

        Ok(self.build_sse_stream(response))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // RAG-005. OpenAI names the same two numbers differently from Anthropic,
    // which is exactly the kind of difference that goes unnoticed until a
    // budget silently stops counting on one provider.

    #[test]
    fn usage_is_read_from_the_response() {
        let body = r#"{
            "choices": [{"message": {"content": "hello"}}],
            "usage": {"prompt_tokens": 900, "completion_tokens": 100, "total_tokens": 1000}
        }"#;

        let parsed: ChatCompletion = serde_json::from_str(body).expect("parse");
        let usage = parsed.usage.expect("usage block");

        assert_eq!(usage.prompt_tokens, 900);
        assert_eq!(usage.completion_tokens, 100);
    }

    #[test]
    fn a_response_without_usage_still_parses() {
        let body = r#"{"choices": [{"message": {"content": "hi"}}]}"#;

        let parsed: ChatCompletion = serde_json::from_str(body).expect("parse");
        assert!(parsed.usage.is_none());
    }

    #[test]
    fn a_partial_usage_block_counts_what_it_reports() {
        let body = r#"{"choices": [], "usage": {"prompt_tokens": 42}}"#;

        let parsed: ChatCompletion = serde_json::from_str(body).expect("parse");
        let usage = parsed.usage.expect("usage block");

        assert_eq!(usage.prompt_tokens, 42);
        assert_eq!(usage.completion_tokens, 0);
    }
}
