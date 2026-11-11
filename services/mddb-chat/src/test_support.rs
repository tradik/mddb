//! Shared fixtures for tests that need a whole `AppState`.
//!
//! Compiled only for tests. The state is built the way `main` builds it, with
//! two differences: the LLM provider is whatever the test hands in, and mddbd
//! is a local stub that speaks HTTP/2 but knows no routes — enough for the
//! gRPC client to connect, and every call to it fails the way a real outage
//! does.

use std::net::SocketAddr;
use std::sync::Arc;

use axum::Router;
use tokio::net::TcpListener;

use crate::config::Config;
use crate::grpc::client::MddbClient;
use crate::grpc::link::MddbLink;
use crate::llm::provider::LlmProvider;
use crate::security::client_ip::TrustedProxies;
use crate::security::rate_limiter::RateLimiter;
use crate::security::sanitizer::Sanitizer;
use crate::session::manager::SessionManager;
use crate::state::AppState;
use crate::webhook::dispatcher::WebhookDispatcher;

/// A plain HTTP server with no routes, bound to a free loopback port.
///
/// axum serves HTTP/2 with prior knowledge, which is what a tonic channel
/// needs to consider itself connected.
pub async fn stub_http_server() -> SocketAddr {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind a loopback port");
    let addr = listener.local_addr().expect("local addr");
    tokio::spawn(async move { axum::serve(listener, Router::new()).await });
    addr
}

/// A configuration a test can edit before building its state.
///
/// Two scenarios: `assistant` without limits and `support` capped at two
/// turns. Generous rate limit, no token budget, no webhooks. Parsed from
/// TOML, so every key the test does not care about has its real default.
pub fn test_config(grpc_addr: &str) -> Config {
    let toml = format!(
        r#"
[server]
host = "127.0.0.1"
port = 0
[mddb]
grpc_addr = "{grpc_addr}"
[llm]
api_url = "http://127.0.0.1:1/v1"
model = "test-model"
max_tokens = 64
[session]
max_concurrent = 2
queue_size = 2
session_ttl_minutes = 30
name_max_chars = 40
[security]
rate_limit_per_minute = 1000
[scenarios.assistant]
name = "Assistant"
system_prompt = "You are terse."
allowed_collections = ["docs"]
[scenarios.support]
name = "Support"
system_prompt = "You are kind."
temperature = 0.2
max_turns = 2
"#
    );
    toml::from_str(&toml).expect("the test configuration parses")
}

/// Builds the state `main` would build from `config`, around `llm`, with the
/// link to mddbd already up.
pub async fn test_state(config: Config, llm: Arc<dyn LlmProvider>) -> Arc<AppState> {
    let client = MddbClient::connect(config.mddb.clone())
        .await
        .expect("connect to the stub mddbd");
    test_state_with_link(config, llm, MddbLink::connected(client))
}

/// The same state around a link the test controls — an empty one is a server
/// whose mddbd has not come up yet (#401).
pub fn test_state_with_link(
    config: Config,
    llm: Arc<dyn LlmProvider>,
    mddb: MddbLink,
) -> Arc<AppState> {
    Arc::new(AppState {
        session_manager: SessionManager::new(config.session.clone()),
        mddb,
        llm_provider: llm,
        webhook_dispatcher: WebhookDispatcher::new(
            config.webhooks.clone(),
            config.security.webhook_secret.clone(),
        ),
        rate_limiter: RateLimiter::new(config.security.rate_limit_per_minute),
        sanitizer: Sanitizer::new(config.security.clone()),
        trusted_proxies: TrustedProxies::parse(&config.security.trusted_proxies),
        config,
    })
}
