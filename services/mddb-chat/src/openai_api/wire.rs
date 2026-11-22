//! What the OpenAI-compatible API (#448) reads and writes: the request
//! body of `POST /v1/chat/completions`, the JSON it answers with, and its
//! errors in the `{"error": {"message", "type", "code"}}` shape such clients
//! read.
//!
//! Fields this server does not use — `temperature`, `top_p`, `tools`,
//! `tool_choice`, `user`, ... — are accepted and ignored, so a client that
//! always sends them still works; README.md says which ones and why.

use axum::Json;
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use serde::Deserialize;
use serde_json::{Value, json};

use crate::error::AppError;
use crate::llm::provider::TokenUsage;

/// What `owned_by` says of every model: a scenario is served by mddb.
pub const OWNER: &str = "mddb";

/// The body of `POST /v1/chat/completions`, as far as it is read.
#[derive(Debug, Deserialize)]
pub struct CompletionRequest {
    pub model: String,
    pub messages: Vec<RequestMessage>,
    #[serde(default)]
    pub stream: bool,
    #[serde(default)]
    pub max_tokens: Option<u32>,
    /// The newer name of `max_tokens`; wins when both are sent.
    #[serde(default)]
    pub max_completion_tokens: Option<u32>,
    #[serde(default)]
    pub stream_options: Option<StreamOptions>,
}

impl CompletionRequest {
    /// The answer length the client asked for, if any.
    pub fn max_tokens(&self) -> Option<u32> {
        self.max_completion_tokens.or(self.max_tokens)
    }

    /// Whether a streamed answer ends with a usage frame.
    pub fn include_usage(&self) -> bool {
        self.stream_options
            .as_ref()
            .is_some_and(|o| o.include_usage)
    }
}

#[derive(Debug, Default, Deserialize)]
pub struct StreamOptions {
    #[serde(default)]
    pub include_usage: bool,
}

/// One message of the conversation the client sends.
#[derive(Debug, Deserialize)]
pub struct RequestMessage {
    pub role: String,
    /// Absent or `null` for an assistant message that only called tools.
    #[serde(default)]
    pub content: Option<Content>,
}

/// A message's content: a string, or a list of typed parts.
#[derive(Debug, Deserialize)]
#[serde(untagged)]
pub enum Content {
    Text(String),
    Parts(Vec<Part>),
}

#[derive(Debug, Deserialize)]
pub struct Part {
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub text: Option<String>,
}

/// One entry of `GET /v1/models`: a scenario, by name, with its label.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Model {
    pub id: String,
    pub label: String,
}

/// The body of `GET /v1/models`. `name` is not in the OpenAI schema; clients
/// that know it (Open WebUI does) show it instead of the id.
pub fn model_list(models: &[Model], created: u64) -> Value {
    let data: Vec<Value> = models
        .iter()
        .map(|m| {
            json!({
                "id": m.id,
                "object": "model",
                "created": created,
                "owned_by": OWNER,
                "name": m.label,
            })
        })
        .collect();
    json!({ "object": "list", "data": data })
}

/// The `usage` object of a completion.
pub fn usage(spent: TokenUsage) -> Value {
    json!({
        "prompt_tokens": spent.input,
        "completion_tokens": spent.output,
        "total_tokens": spent.total(),
    })
}

/// Which completion a response or chunk belongs to.
#[derive(Debug, Clone)]
pub struct Completion {
    pub id: String,
    pub created: u64,
    pub model: String,
}

impl Completion {
    /// The whole answer, for a request without `stream`.
    pub fn whole(&self, text: &str, spent: TokenUsage) -> Value {
        json!({
            "id": self.id,
            "object": "chat.completion",
            "created": self.created,
            "model": self.model,
            "choices": [{
                "index": 0,
                "message": { "role": "assistant", "content": text },
                "finish_reason": "stop",
            }],
            "usage": usage(spent),
        })
    }

    /// One frame of a streamed answer.
    pub fn chunk(&self, delta: Value, finish_reason: Option<&str>) -> Value {
        json!({
            "id": self.id,
            "object": "chat.completion.chunk",
            "created": self.created,
            "model": self.model,
            "choices": [{ "index": 0, "delta": delta, "finish_reason": finish_reason }],
        })
    }

    /// The frame `stream_options.include_usage` asks for: no choices, the
    /// cost of the whole turn.
    pub fn usage_chunk(&self, spent: TokenUsage) -> Value {
        json!({
            "id": self.id,
            "object": "chat.completion.chunk",
            "created": self.created,
            "model": self.model,
            "choices": [],
            "usage": usage(spent),
        })
    }
}

/// A refusal in the shape OpenAI-compatible clients read.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ApiError {
    pub status: StatusCode,
    pub message: String,
    pub code: Option<&'static str>,
}

impl ApiError {
    pub fn new(status: StatusCode, message: impl Into<String>) -> Self {
        Self {
            status,
            message: message.into(),
            code: None,
        }
    }

    pub fn bad_request(message: impl Into<String>) -> Self {
        Self::new(StatusCode::BAD_REQUEST, message)
    }

    pub fn unauthorized(message: impl Into<String>) -> Self {
        Self::new(StatusCode::UNAUTHORIZED, message)
    }

    /// The same answer for a model that does not exist and one the key may
    /// not use, so a key cannot probe for scenario names.
    pub fn model_not_found(model: &str) -> Self {
        Self {
            code: Some("model_not_found"),
            ..Self::new(
                StatusCode::NOT_FOUND,
                format!("The model '{model}' does not exist or you do not have access to it."),
            )
        }
    }

    /// The `type` OpenAI's errors carry, from the status.
    pub fn kind(&self) -> &'static str {
        match self.status {
            StatusCode::BAD_REQUEST | StatusCode::NOT_FOUND => "invalid_request_error",
            StatusCode::UNAUTHORIZED => "authentication_error",
            StatusCode::FORBIDDEN => "permission_error",
            StatusCode::TOO_MANY_REQUESTS => "rate_limit_error",
            _ => "api_error",
        }
    }

    /// The `{"error": ...}` object, also sent as a frame when a streamed
    /// answer fails half way.
    pub fn body(&self) -> Value {
        json!({ "error": { "message": self.message, "type": self.kind(), "code": self.code } })
    }
}

impl From<AppError> for ApiError {
    fn from(error: AppError) -> Self {
        Self::new(error.status(), error.to_string())
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        (self.status, Json(self.body())).into_response()
    }
}

#[cfg(test)]
#[path = "wire_tests.rs"]
mod tests;
