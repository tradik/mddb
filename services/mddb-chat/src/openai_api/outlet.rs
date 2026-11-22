//! Where the OpenAI-compatible API (#448) sends a turn's answer: into one
//! JSON response, or out as `chat.completion.chunk` frames.

use serde_json::{Value, json};
use tokio::sync::mpsc;

use crate::chat::outbound::Outlet;
use crate::openai_api::wire::Completion;

/// The answer of a request without `stream`, gathered whole.
#[derive(Debug, Default)]
pub struct Collected {
    pub text: String,
    /// The error the turn reported after it started, if any.
    pub error: Option<String>,
}

#[async_trait::async_trait]
impl Outlet for Collected {
    async fn chunk(&mut self, content: String) {
        self.text.push_str(&content);
    }

    async fn error(&mut self, message: &str) {
        self.error = Some(message.to_string());
    }

    async fn done(&mut self, _message_id: String) {}
}

/// The answer of a streamed request: each piece a frame on `frames`, the
/// first one with the assistant's role. The closing frames — the finish
/// reason, the usage, `[DONE]` — are the handler's, which knows what the
/// turn cost.
pub struct Frames {
    pub frames: mpsc::Sender<Value>,
    pub completion: Completion,
    started: bool,
}

impl Frames {
    pub fn new(frames: mpsc::Sender<Value>, completion: Completion) -> Self {
        Self {
            frames,
            completion,
            started: false,
        }
    }

    /// Sends one frame. A failed send means the client has gone; the turn
    /// runs to its end anyway, so it is logged and charged like any other.
    pub async fn send(&self, frame: Value) {
        let _ = self.frames.send(frame).await;
    }
}

#[async_trait::async_trait]
impl Outlet for Frames {
    async fn chunk(&mut self, content: String) {
        let delta = match self.started {
            false => json!({ "role": "assistant", "content": content }),
            true => json!({ "content": content }),
        };
        self.started = true;
        self.send(self.completion.chunk(delta, None)).await;
    }

    async fn error(&mut self, message: &str) {
        let error = json!({ "error": { "message": message, "type": "api_error", "code": null } });
        self.send(error).await;
    }

    async fn done(&mut self, _message_id: String) {}
}

#[cfg(test)]
#[path = "outlet_tests.rs"]
mod tests;
