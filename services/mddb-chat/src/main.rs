use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use axum::Router;
use axum::routing::get;
use tower_http::cors::{Any, CorsLayer};
use tower_http::trace::TraceLayer;
use tracing::info;

mod chat;
mod config;
mod error;
mod grpc;
mod llm;
mod routes;
mod security;
mod session;
mod state;
mod webhook;

use config::Config;
use grpc::client::MddbClient;
use llm::anthropic::AnthropicProvider;
use llm::openai::OpenAiProvider;
use llm::provider::LlmProvider;
use security::rate_limiter::RateLimiter;
use security::sanitizer::Sanitizer;
use session::manager::SessionManager;
use state::AppState;
use webhook::dispatcher::WebhookDispatcher;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Initialize tracing
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "mddb_chat=info,tower_http=info".into()),
        )
        .init();

    // Load config
    let config_path = std::env::var("MDDB_CHAT_CONFIG")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from("config.toml"));

    let config = Config::load(&config_path)?;
    info!(
        host = config.server.host,
        port = config.server.port,
        grpc_addr = config.mddb.grpc_addr,
        llm_provider = config.llm.provider,
        llm_model = config.llm.model,
        max_concurrent = config.session.max_concurrent,
        "starting mddb-chat"
    );

    // Initialize components
    let mddb_client = MddbClient::connect(config.mddb.clone()).await?;
    let session_manager = SessionManager::new(config.session.clone());
    let llm_provider: Arc<dyn LlmProvider> = match config.llm.provider.as_str() {
        "anthropic" | "claude" => Arc::new(AnthropicProvider::new(config.llm.clone())),
        // "openai" and anything else (ollama, bielik, groq, mistral, openrouter, etc.)
        // all use OpenAI-compatible API format
        _ => Arc::new(OpenAiProvider::new(config.llm.clone())),
    };
    let webhook_dispatcher = WebhookDispatcher::new(
        config.webhooks.clone(),
        config.security.webhook_secret.clone(),
    );
    let rate_limiter = RateLimiter::new(config.security.rate_limit_per_minute);
    let sanitizer = Sanitizer::new(config.security.clone());

    let state = Arc::new(AppState {
        config: config.clone(),
        session_manager,
        mddb_client,
        llm_provider,
        webhook_dispatcher,
        rate_limiter,
        sanitizer,
    });

    // Session cleanup task
    let cleanup_state = state.clone();
    tokio::spawn(async move {
        let mut interval = tokio::time::interval(Duration::from_secs(60));
        loop {
            interval.tick().await;
            cleanup_state.session_manager.cleanup_expired().await;
            cleanup_state.rate_limiter.cleanup();
        }
    });

    // CORS from the configuration, not a hardcoded wildcard.
    //
    // TEST-001: this used to be `allow_origin(Any)` unconditionally, so
    // `server.cors_origins` was read from the TOML, defaulted, and then had no
    // effect — an operator who listed their own origins still served
    // `Access-Control-Allow-Origin: *` to every page on the internet.
    let cors = if config.server.allows_any_origin() {
        tracing::warn!(
            "CORS allows any origin; set server.cors_origins for a deployment \
             a browser can reach"
        );
        CorsLayer::new()
            .allow_origin(Any)
            .allow_methods(Any)
            .allow_headers(Any)
    } else {
        let origins = config.server.allowed_origins();
        tracing::info!(
            count = origins.len(),
            "CORS restricted to configured origins"
        );
        CorsLayer::new()
            .allow_origin(origins)
            .allow_methods(Any)
            .allow_headers(Any)
    };

    // Build router
    let app = Router::new()
        .route("/health", get(routes::health::health))
        .route("/config", get(routes::health::config_info))
        .route("/ws", get(routes::ws::ws_handler))
        .layer(cors)
        .layer(TraceLayer::new_for_http())
        .with_state(state);

    let addr = SocketAddr::new(
        config.server.host.parse().unwrap_or([0, 0, 0, 0].into()),
        config.server.port,
    );

    info!("listening on {addr}");

    let listener = tokio::net::TcpListener::bind(addr).await?;
    axum::serve(
        listener,
        app.into_make_service_with_connect_info::<SocketAddr>(),
    )
    .await?;

    Ok(())
}
