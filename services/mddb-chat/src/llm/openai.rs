use futures::StreamExt;
use reqwest::Client;
use serde::Deserialize;

use crate::config::LlmConfig;
use crate::error::AppError;
use crate::llm::provider::{
    ApiMsg, CallParams, ChatResponse, ChatTurn, ChunkStream, LlmProvider, TokenUsage, ToolCall,
    ToolDef,
};
use tracing::debug;

pub struct OpenAiProvider {
    client: Client,
    config: LlmConfig,
}

/// How much of a model's reasoning is quoted in the debug log.
const REASONING_PREVIEW_CHARS: usize = 400;

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
    /// Where a thinking model puts its chain of thought (#400): `reasoning`
    /// on Ollama and OpenRouter, `reasoning_content` on DeepSeek and vLLM.
    /// Read only to explain an empty answer in the log — never shown to the
    /// visitor.
    #[serde(default)]
    reasoning: Option<String>,
    #[serde(default)]
    reasoning_content: Option<String>,
    tool_calls: Option<Vec<ToolCall>>,
}

impl CompletionMessage {
    /// The answer for the visitor: the content, and only the content.
    ///
    /// A thinking model that spends `max_tokens` on reasoning returns an
    /// empty `content` next to a full `reasoning`. That used to look like a
    /// model that said nothing; it is now explained at debug level, with the
    /// setting that fixes it.
    fn into_answer(self) -> String {
        let answer = self.content.unwrap_or_default();
        let reasoning = self
            .reasoning
            .or(self.reasoning_content)
            .unwrap_or_default();
        if answer.is_empty() && !reasoning.is_empty() {
            let reasoning_chars = reasoning.chars().count();
            let reasoning = preview(&reasoning);
            debug!(
                reasoning_chars,
                reasoning,
                "the model returned reasoning and no content; raise [llm] max_tokens \
                 or set [llm] extra_body = {{ reasoning_effort = \"none\" }}"
            );
        }
        answer
    }
}

/// The first [`REASONING_PREVIEW_CHARS`] characters of `text`, marked when
/// cut.
fn preview(text: &str) -> String {
    match text.char_indices().nth(REASONING_PREVIEW_CHARS) {
        Some((cut, _)) => format!("{}…", &text[..cut]),
        None => text.to_string(),
    }
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

    /// The JSON body of one request.
    ///
    /// Starts from `[llm] extra_body`, lays the scenario's extra body over it
    /// key by key (#403), and writes the request's own fields over both
    /// (#400), so extra_body can add a parameter this server does not model
    /// but cannot swap the model or drop the conversation.
    fn request_body(
        &self,
        messages: &[ApiMsg],
        tools: &[ToolDef],
        params: &CallParams,
        stream: bool,
    ) -> serde_json::Value {
        let mut body = self.config.extra_body.clone();
        body.extend(params.extra_body.clone());
        let model = params.model_or(&self.config.model);
        body.insert("model".into(), model.into());
        body.insert("messages".into(), Self::api_messages(messages).into());
        body.insert("max_tokens".into(), params.max_tokens.into());
        body.insert("temperature".into(), params.temperature.into());
        body.insert("stream".into(), stream.into());
        // No tools means no key at all — which also discards an extra_body
        // `tools`, since that one belongs to the request.
        body.remove("tools");
        if !tools.is_empty() {
            body.insert("tools".into(), serde_json::json!(tools));
        }
        serde_json::Value::Object(body)
    }

    /// Posts `body` and returns the response if the endpoint accepted it.
    async fn post(&self, body: &serde_json::Value) -> Result<reqwest::Response, AppError> {
        let req = self.client.post(self.api_url()).json(body);
        let response = self
            .auth_header(req)
            .send()
            .await
            .map_err(|e| AppError::LlmError(e.to_string()))?;

        if !response.status().is_success() {
            let status = response.status();
            let body = response.text().await.unwrap_or_default();
            return Err(AppError::LlmError(format!("LLM API {status}: {body}")));
        }
        Ok(response)
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
        params: &CallParams,
    ) -> Result<ChatTurn, AppError> {
        let body = self.request_body(messages, tools, params, false);
        let response = self.post(&body).await?;

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
            _ => ChatResponse::Content(choice.message.into_answer()),
        };

        Ok(ChatTurn { response, usage })
    }

    async fn chat_stream_raw(
        &self,
        messages: &[ApiMsg],
        params: &CallParams,
    ) -> Result<ChunkStream, AppError> {
        let body = self.request_body(messages, &[], params, true);
        let response = self.post(&body).await?;
        Ok(self.build_sse_stream(response))
    }
}

#[cfg(test)]
#[path = "openai_tests.rs"]
mod tests;
