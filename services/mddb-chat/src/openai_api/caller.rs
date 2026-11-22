//! Who calls the OpenAI-compatible API (#448), and which scenarios it may use.
//!
//! The key a client sends as `Authorization: Bearer` is an mddb credential —
//! an `mddb_live_…` API key or a JWT — and mddbd is asked about it on every
//! request, so a revoked key or a changed permission applies at once:
//!
//! 1. `GET /v1/auth/me` proves the key. mddbd registers that route only with
//!    its authentication on, so a 404 means there is nothing to check a key
//!    against, and the API refuses rather than answer anyone.
//! 2. `GET /v1/chat/scenarios` with the same key lists the scenarios mddbd
//!    lets that caller read — every collection the scenario allows (its
//!    `chatScenarioReadable` rule).
//!
//! The models are those names that mddb-chat also serves: enabled in the
//! scenarios it loaded from mddbd. A `[scenarios.*]` table of the TOML is
//! not one: mddbd holds no access rule for it.

use std::sync::Arc;
use std::time::Duration;

use axum::http::{HeaderMap, StatusCode, header};
use serde::Deserialize;

use crate::chat::scenario::{self, ActiveScenario, StoredScenario};
use crate::error::AppError;
use crate::openai_api::wire::{ApiError, Model};
use crate::state::AppState;

/// How long mddbd may take to answer one of the two questions.
const MDDBD_TIMEOUT: Duration = Duration::from_secs(10);

/// The key of `Authorization: Bearer <key>`, if the request has one.
pub fn bearer(headers: &HeaderMap) -> Option<&str> {
    headers
        .get(header::AUTHORIZATION)?
        .to_str()
        .ok()?
        .strip_prefix("Bearer ")
        .map(str::trim)
        .filter(|key| !key.is_empty())
}

#[derive(Deserialize)]
struct ScenarioList {
    #[serde(default)]
    scenarios: Vec<ScenarioName>,
}

#[derive(Deserialize)]
struct ScenarioName {
    name: String,
}

/// The models `key` may use, in name order.
pub async fn models(state: &AppState, key: &str) -> Result<Vec<Model>, ApiError> {
    let mut models: Vec<Model> = readable(state, key)
        .await?
        .into_iter()
        .filter_map(|id| {
            let label = served(state, &id)?.label.clone();
            Some(Model { id, label })
        })
        .collect();
    models.sort_by(|a, b| a.id.cmp(&b.id));
    Ok(models)
}

/// The scenario `model` names, when `key` may use it. A model that does not
/// exist and one the key may not use are refused alike.
pub async fn scenario(
    state: &AppState,
    key: &str,
    model: &str,
) -> Result<ActiveScenario, ApiError> {
    let readable = readable(state, key).await?;
    readable
        .iter()
        .any(|name| name == model)
        .then(|| served(state, model))
        .flatten()
        .and_then(|_| scenario::resolve(&state.config, &state.scenarios, model).active())
        .ok_or_else(|| ApiError::model_not_found(model))
}

/// mddbd's scenario of that name, when mddb-chat serves it: loaded, and
/// enabled.
fn served(state: &AppState, name: &str) -> Option<Arc<StoredScenario>> {
    state.scenarios.get(name).filter(|stored| stored.enabled)
}

/// The names of the scenarios mddbd lets `key` read.
async fn readable(state: &AppState, key: &str) -> Result<Vec<String>, ApiError> {
    // GO-049: the chat module off in mddbd switches this door off too.
    if !state.features.chat() {
        return Err(AppError::ChatDisabled.into());
    }
    let base = state.config.mddb.http_base().map_err(AppError::Internal)?;
    let client = reqwest::Client::builder()
        .timeout(MDDBD_TIMEOUT)
        .build()
        .unwrap_or_default();

    let me = ask(&client, &format!("{base}/v1/auth/me"), key).await?;
    if me.status() == reqwest::StatusCode::NOT_FOUND {
        return Err(ApiError::new(
            StatusCode::SERVICE_UNAVAILABLE,
            "mddbd authentication is off, so no API key can be checked; \
             the OpenAI-compatible API needs MDDB_AUTH_ENABLED=true",
        ));
    }
    accepted(&me)?;

    let listed = ask(&client, &format!("{base}/v1/chat/scenarios"), key).await?;
    accepted(&listed)?;
    let listed: ScenarioList = listed
        .json()
        .await
        .map_err(|e| upstream(&format!("mddbd's scenario list did not parse: {e}")))?;
    Ok(listed.scenarios.into_iter().map(|s| s.name).collect())
}

/// One GET to mddbd with the caller's key.
async fn ask(
    client: &reqwest::Client,
    url: &str,
    key: &str,
) -> Result<reqwest::Response, ApiError> {
    client
        .get(url)
        .bearer_auth(key)
        .send()
        .await
        .map_err(|e| upstream(&format!("mddbd did not answer: {e}")))
}

/// A 2xx passes; 401 or 403 is a key mddbd does not accept; anything else
/// is mddbd's failure, not the caller's.
fn accepted(response: &reqwest::Response) -> Result<(), ApiError> {
    let status = response.status();
    if status.is_success() {
        return Ok(());
    }
    if status == reqwest::StatusCode::UNAUTHORIZED || status == reqwest::StatusCode::FORBIDDEN {
        return Err(ApiError::unauthorized("invalid API key"));
    }
    Err(upstream(&format!("mddbd answered {status}")))
}

fn upstream(message: &str) -> ApiError {
    ApiError::new(StatusCode::BAD_GATEWAY, message)
}

#[cfg(test)]
#[path = "caller_tests.rs"]
mod tests;
