//! The OpenAI-compatible API (#448): every enabled chat scenario the caller
//! may read is a model, so a client that speaks OpenAI's chat completions —
//! such as Open WebUI's OpenAI API connection — can use one.
//!
//! - `GET /v1/models` lists them (`caller.rs` says who may use which).
//! - `POST /v1/chat/completions` answers with the scenario's turn — its
//!   composed prompt, collections, tools, LLM overrides and loop guard —
//!   through the same `chat::turn::run` the widget uses, whole or streamed.
//!
//! Stateless: the client sends the conversation every time and no session is
//! made (`conversation.rs`). Off unless `[openai_api] enabled`; then the
//! routes do not exist and answer 404 like any other unknown path.

mod caller;
mod conversation;
mod limits;
mod outlet;
mod wire;

use std::convert::Infallible;
use std::sync::Arc;
use std::time::Instant;

use axum::Router;
use axum::body::Bytes;
use axum::extract::State;
use axum::http::HeaderMap;
use axum::response::sse::{Event, Sse};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use futures::{Stream, StreamExt};
use serde_json::Value;
use tokio::sync::mpsc;
use tokio_stream::wrappers::ReceiverStream;

use crate::chat::scenario::ActiveScenario;
use crate::chat::turn::{self, Turn};
use crate::error::AppError;
use crate::grpc::client::MddbClient;
use crate::identity;
use crate::state::AppState;

pub use limits::KeyPaces;
use outlet::{Collected, Frames};
use wire::{ApiError, Completion, CompletionRequest};

/// Frames a streamed answer may have waiting for a slow client.
const FRAME_BUFFER: usize = 64;

/// The routes, for `app::router` to merge when the API is on.
pub fn routes() -> Router<Arc<AppState>> {
    Router::new()
        .route("/v1/models", get(models))
        .route("/v1/chat/completions", post(completions))
}

/// `GET /v1/models`.
async fn models(State(state): State<Arc<AppState>>, headers: HeaderMap) -> Response {
    let Some(key) = caller::bearer(&headers) else {
        return missing_key().into_response();
    };
    match caller::models(&state, key).await {
        Ok(models) => axum::Json(wire::model_list(&models, identity::now())).into_response(),
        Err(e) => e.into_response(),
    }
}

/// `POST /v1/chat/completions`.
async fn completions(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    match accept(&state, &headers, &body).await {
        Ok(accepted) if accepted.request.stream => stream(state, accepted).into_response(),
        Ok(accepted) => whole(&state, accepted).await,
        Err(e) => e.into_response(),
    }
}

/// A request that passed every check before the model is asked.
struct Accepted {
    request: CompletionRequest,
    scenario: ActiveScenario,
    asked: conversation::Asked,
    client: MddbClient,
    completion: Completion,
}

/// The key, the body, the model, the limits: everything that is refused with
/// a status before the answer starts.
async fn accept(state: &AppState, headers: &HeaderMap, body: &[u8]) -> Result<Accepted, ApiError> {
    let key = caller::bearer(headers).ok_or_else(missing_key)?;
    let request: CompletionRequest = serde_json::from_slice(body)
        .map_err(|e| ApiError::bad_request(format!("invalid request body: {e}")))?;
    // #401: the server answers before mddbd does; nothing can be checked yet.
    let client = state.mddb.client().await.ok_or(AppError::MddbUnavailable)?;
    let mut scenario = caller::scenario(state, key, &request.model).await?;
    // #448: the clients of one tool share its address, so both limits are
    // charged to the key, not to where the request came from.
    let fingerprint = limits::fingerprint(key);
    if !state.rate_limiter.check(&format!("key:{fingerprint}")) {
        return Err(AppError::RateLimited.into());
    }
    let per_minute = scenario.messages_per_minute;
    if !state
        .key_paces
        .admit(&fingerprint, &request.model, per_minute, Instant::now())
    {
        return Err(AppError::SessionRateLimited(per_minute).into());
    }
    // The scenario's own length is the ceiling; a client may only ask for less.
    if let Some(asked) = request.max_tokens() {
        scenario.llm.max_tokens = scenario.llm.max_tokens.min(asked.max(1));
    }
    let asked = conversation::read(state, &scenario, &request.messages)?;
    let completion = Completion {
        id: format!("chatcmpl-{}", uuid::Uuid::new_v4()),
        created: identity::now(),
        model: request.model.clone(),
    };
    Ok(Accepted {
        request,
        scenario,
        asked,
        client,
        completion,
    })
}

/// Runs the turn into `out`. The webhooks and the turn log name the
/// completion's id as the session, since there is no other.
async fn answer(
    state: &AppState,
    out: &mut dyn crate::chat::outbound::Outlet,
    accepted: Accepted,
) -> Result<turn::Outcome, AppError> {
    let Accepted {
        request,
        scenario,
        asked,
        mut client,
        completion,
    } = accepted;
    let turn = Turn {
        session_id: &completion.id,
        scenario_name: &request.model,
        scenario,
        history: asked.history,
        stored: None,
        question: &asked.question,
    };
    turn::run(state, out, &mut client, turn).await
}

/// The answer as one `chat.completion`.
async fn whole(state: &AppState, accepted: Accepted) -> Response {
    let completion = accepted.completion.clone();
    let mut out = Collected::default();
    match answer(state, &mut out, accepted).await {
        Ok(_) if out.text.is_empty() && out.error.is_some() => {
            let message = out.error.unwrap_or_default();
            ApiError::from(AppError::LlmError(message)).into_response()
        }
        Ok(outcome) => axum::Json(completion.whole(&out.text, outcome.spent)).into_response(),
        Err(e) => ApiError::from(e).into_response(),
    }
}

/// The answer as `text/event-stream`: one frame per piece, then the finish
/// reason, the usage when `stream_options.include_usage` asks for it, and
/// `[DONE]`. A turn that fails after the stream has started ends in an
/// `{"error": ...}` frame instead of the finish reason.
fn stream(
    state: Arc<AppState>,
    accepted: Accepted,
) -> Sse<impl Stream<Item = Result<Event, Infallible>>> {
    let (tx, rx) = mpsc::channel::<Value>(FRAME_BUFFER);
    let completion = accepted.completion.clone();
    let include_usage = accepted.request.include_usage();
    tokio::spawn(async move {
        let mut out = Frames::new(tx, completion.clone());
        match answer(&state, &mut out, accepted).await {
            Ok(outcome) => {
                let finish = completion.chunk(serde_json::json!({}), Some("stop"));
                out.send(finish).await;
                if include_usage {
                    out.send(completion.usage_chunk(outcome.spent)).await;
                }
            }
            Err(e) => out.send(ApiError::from(e).body()).await,
        }
    });
    let frames = ReceiverStream::new(rx).map(|frame| Ok(Event::default().data(frame.to_string())));
    let done = futures::stream::once(async { Ok(Event::default().data("[DONE]")) });
    Sse::new(frames.chain(done))
}

fn missing_key() -> ApiError {
    ApiError::unauthorized("an API key is required: Authorization: Bearer <key>")
}

#[cfg(test)]
mod tests;
