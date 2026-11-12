//! The answer of a turn as it reaches the visitor: the content of a
//! tool-calling round, or the streamed answer after the last round — both
//! through the loop guard (#413).

use futures::StreamExt;
use tracing::error;

use crate::chat::loop_guard::report::{StopSite, report};
use crate::chat::loop_guard::{self, LoopDetected, LoopDetector, LoopGuardSettings, MARKER};
use crate::chat::outbound::{Sender, send_error, send_json};
use crate::error::AppError;
use crate::llm::provider::{ApiMsg, CallParams};
use crate::session::types::WsOutgoing;
use crate::state::AppState;

/// An answer and whether the loop guard cut it short.
#[derive(Debug, Clone, PartialEq)]
pub struct Reply {
    pub text: String,
    pub loop_stopped: bool,
}

impl Reply {
    fn whole(text: String) -> Self {
        Self {
            text,
            loop_stopped: false,
        }
    }
}

/// A non-streaming call's content, cut at the loop when it holds one.
///
/// The call is over by the time its content is read, so nothing is left to
/// cancel: what it cost is what the provider reports. What the visitor and
/// the next turn see is the text up to the loop and the marker.
pub fn guard_content(
    state: &AppState,
    site: &StopSite<'_>,
    guard: Option<LoopGuardSettings>,
    text: String,
) -> Reply {
    match loop_guard::check(guard, &text) {
        Some(found) => {
            report(state, site, &found);
            Reply {
                text: loop_guard::stopped_answer(&text, &found),
                loop_stopped: true,
            }
        }
        None => Reply::whole(text),
    }
}

/// Streams the model's answer to the visitor as it arrives. Stops at a
/// loop, at `max_response_length` or at the first stream error, each of which
/// the visitor is told about.
///
/// A loop drops the stream, which closes the connection to the model and so
/// cancels the request: the model stops generating what nobody will read.
pub async fn stream(
    state: &AppState,
    out: &mut Sender,
    (api_messages, params): (&[ApiMsg], &CallParams),
    guard: Option<LoopGuardSettings>,
    site: &StopSite<'_>,
) -> Result<Reply, AppError> {
    let mut stream = state
        .llm_provider
        .chat_stream_raw(api_messages, params)
        .await?;
    let mut detector = guard.map(LoopDetector::new);

    let mut full_response = String::new();
    while let Some(chunk) = stream.next().await {
        match chunk {
            Ok(text) => {
                let sent = full_response.len();
                full_response.push_str(&text);
                if let Some(found) = detector.as_mut().and_then(|d| d.push(&text)) {
                    drop(stream);
                    report(state, site, &found);
                    return Ok(stop(out, &full_response, sent, &found).await);
                }
                if full_response.len() > state.config.session.max_response_length {
                    let content = "\n\n[Response truncated]".to_string();
                    send_json(out, &WsOutgoing::Chunk { content }).await;
                    break;
                }
                send_json(out, &WsOutgoing::Chunk { content: text }).await;
            }
            Err(e) => {
                error!(error = %e, "LLM stream error");
                send_error(out, "error generating response").await;
                break;
            }
        }
    }
    Ok(Reply::whole(full_response))
}

/// Ends a streamed answer at a loop: the visitor gets whatever of the text
/// up to the loop it has not been sent yet, then the marker. What it was
/// already sent of the repeat stays on its screen — at most a window's worth
/// — while the answer kept for the session and the log ends at the loop.
async fn stop(out: &mut Sender, full: &str, sent: usize, found: &LoopDetected) -> Reply {
    let unsent = full
        .get(sent..loop_guard::cut_at(full, found))
        .unwrap_or_default();
    let content = format!("{unsent}\n\n{MARKER}");
    send_json(out, &WsOutgoing::Chunk { content }).await;
    Reply {
        text: loop_guard::stopped_answer(full, found),
        loop_stopped: true,
    }
}
