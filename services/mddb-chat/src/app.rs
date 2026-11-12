//! Assembling the server: state, routes, background work, shutdown.
//!
//! Everything `main` does besides reading the configuration lives here, in
//! pieces a test can call.

use std::future::Future;
use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

use axum::Router;
use axum::routing::get;
use tokio::net::TcpListener;
use tower_http::cors::{Any, CorsLayer};
use tower_http::trace::TraceLayer;

use crate::chat::features::FeatureFlags;
use crate::chat::scenario::{ScenarioStore, sync};
use crate::config::{Config, MddbConfig, ServerConfig};
use crate::error::AppError;
use crate::grpc::client::MddbClient;
use crate::grpc::link::{MddbLink, connect_with_retry};
use crate::llm::anthropic::AnthropicProvider;
use crate::llm::openai::OpenAiProvider;
use crate::llm::provider::LlmProvider;
use crate::metrics::Metrics;
use crate::routes;
use crate::security::client_ip::TrustedProxies;
use crate::security::rate_limiter::RateLimiter;
use crate::security::sanitizer::Sanitizer;
use crate::session::manager::SessionManager;
use crate::state::AppState;
use crate::webhook::dispatcher::WebhookDispatcher;
use crate::webhook::types::WebhookPayload;

/// How often expired sessions and idle rate-limit budgets are swept.
const MAINTENANCE_INTERVAL: Duration = Duration::from_secs(60);

/// Opens the link to mddbd the way the configuration asks for (#401).
///
/// With retrying on — the default — this returns at once with an empty link
/// and a background task fills it when mddbd answers. With
/// `connect_retry_max_seconds = 0` it makes one attempt and fails on error,
/// which is how every version before this one started.
pub async fn connect_mddb(config: &MddbConfig) -> Result<MddbLink, AppError> {
    if config.connect_retry_max_seconds == 0 {
        let client = MddbClient::connect(config.clone()).await?;
        return Ok(MddbLink::connected(client));
    }
    let link = MddbLink::default();
    tokio::spawn(connect_with_retry(config.clone(), link.clone()));
    Ok(link)
}

/// Picks the LLM provider: Anthropic's own API, or the OpenAI-compatible one
/// everything else speaks (ollama, bielik, groq, mistral, openrouter...).
pub fn llm_provider(config: &Config) -> Arc<dyn LlmProvider> {
    match config.llm.provider.as_str() {
        "anthropic" | "claude" => Arc::new(AnthropicProvider::new(config.llm.clone())),
        _ => Arc::new(OpenAiProvider::new(config.llm.clone())),
    }
}

pub async fn build_state(config: Config) -> Result<Arc<AppState>, AppError> {
    let mddb = connect_mddb(&config.mddb).await?;

    // SEC-014: parsed once, not per connection. An empty list charges the rate
    // limit to the TCP peer, which is right for a directly exposed server and
    // wrong behind the reverse proxy this is documented to run behind.
    let trusted_proxies = TrustedProxies::parse(&config.security.trusted_proxies);
    if trusted_proxies.is_empty() {
        tracing::info!("no trusted proxies configured; rate limits are charged to the TCP peer");
    }

    // #403: with mddbd already reached — the fail-fast startup — its scenarios
    // are in place before the first visitor is. Otherwise the poll loads them
    // when the link comes up, and the TOML scenarios serve until then.
    let scenarios = ScenarioStore::default();
    let features = FeatureFlags::default();
    if config.scenarios_source.poll_seconds > 0 {
        sync::sync_once(&mddb, &scenarios, &features).await;
    }

    Ok(Arc::new(AppState {
        session_manager: SessionManager::new(config.session.clone()),
        mddb,
        scenarios,
        features,
        llm_provider: llm_provider(&config),
        webhook_dispatcher: WebhookDispatcher::new(
            config.webhooks.clone(),
            config.security.webhook_secret.clone(),
        ),
        rate_limiter: RateLimiter::new(config.security.rate_limit_per_minute),
        sanitizer: Sanitizer::new(config.security.clone()),
        trusted_proxies,
        metrics: Metrics::default(),
        config,
    }))
}

/// One maintenance pass: reap expired sessions, announce each, and trim the
/// rate limiter.
///
/// #402: a session that times out ended just as surely as one the visitor
/// closed, so `session.end` fires here rather than on the disconnect that
/// merely detached it. Returns how many sessions were reaped.
pub async fn run_maintenance(state: &AppState) -> usize {
    let expired = state.session_manager.cleanup_expired().await;
    for session in &expired {
        state
            .webhook_dispatcher
            .dispatch(WebhookPayload::session_end(
                &session.id,
                &session.name,
                &session.scenario,
            ));
        tracing::info!(session_id = session.id, "session expired");
    }
    state.rate_limiter.cleanup();
    expired.len()
}

/// Starts following mddbd's scenarios (#403): a look at the store's version
/// every `[scenarios_source] poll_seconds`, and a reload when it moved.
/// Returns whether the poll runs; `poll_seconds = 0` leaves the TOML
/// scenarios as the only ones.
pub fn spawn_scenario_poll(state: &AppState) -> bool {
    let poll_seconds = state.config.scenarios_source.poll_seconds;
    if poll_seconds == 0 {
        tracing::info!("scenarios_source.poll_seconds is 0; scenarios come from the TOML only");
        return false;
    }
    tokio::spawn(sync::poll(
        state.mddb.clone(),
        state.scenarios.clone(),
        state.features.clone(),
        Duration::from_secs(poll_seconds),
    ));
    true
}

/// The server's background work: the scenario poll, and the sweep of expired
/// sessions and idle rate-limit budgets.
pub fn spawn_maintenance(state: Arc<AppState>) {
    spawn_scenario_poll(&state);
    tokio::spawn(async move {
        let mut interval = tokio::time::interval(MAINTENANCE_INTERVAL);
        loop {
            interval.tick().await;
            run_maintenance(&state).await;
        }
    });
}

/// CORS from the configuration, not a hardcoded wildcard.
///
/// TEST-001: this used to be `allow_origin(Any)` unconditionally, so
/// `server.cors_origins` was read from the TOML, defaulted, and then had no
/// effect — an operator who listed their own origins still served
/// `Access-Control-Allow-Origin: *` to every page on the internet.
pub fn cors_layer(server: &ServerConfig) -> CorsLayer {
    let layer = CorsLayer::new().allow_methods(Any).allow_headers(Any);
    if server.allows_any_origin() {
        tracing::warn!(
            "CORS allows any origin; set server.cors_origins for a deployment \
             a browser can reach"
        );
        return layer.allow_origin(Any);
    }
    let origins = server.allowed_origins();
    let count = origins.len();
    tracing::info!(count, "CORS restricted to configured origins");
    layer.allow_origin(origins)
}

pub fn router(state: Arc<AppState>) -> Router {
    let cors = cors_layer(&state.config.server);
    Router::new()
        .route("/health", get(routes::health::health))
        .route("/config", get(routes::health::config_info))
        .route("/metrics", get(routes::metrics::metrics))
        .route("/ws", get(routes::ws::ws_handler))
        .layer(cors)
        .layer(TraceLayer::new_for_http())
        .with_state(state)
}

/// Where the configuration says to listen; an unparseable host means all
/// interfaces, as it always has.
pub fn listen_addr(server: &ServerConfig) -> SocketAddr {
    SocketAddr::new(
        server.host.parse().unwrap_or([0, 0, 0, 0].into()),
        server.port,
    )
}

/// Serves `app` until `shutdown` resolves.
pub async fn serve(
    listener: TcpListener,
    app: Router,
    shutdown: impl Future<Output = ()> + Send + 'static,
) -> std::io::Result<()> {
    axum::serve(
        listener,
        app.into_make_service_with_connect_info::<SocketAddr>(),
    )
    .with_graceful_shutdown(shutdown)
    .await
}

/// Resolves on SIGTERM or Ctrl-C.
///
/// SIGTERM is what Kubernetes and `docker stop` send. Handling it lets the
/// process finish its accept loop and exit 0 instead of being killed.
pub async fn shutdown_signal() {
    let mut term = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
        .expect("install the SIGTERM handler");
    tokio::select! {
        _ = term.recv() => tracing::info!("SIGTERM received; shutting down"),
        _ = tokio::signal::ctrl_c() => tracing::info!("interrupt received; shutting down"),
    }
}
