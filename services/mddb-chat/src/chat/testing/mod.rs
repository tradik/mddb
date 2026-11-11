//! The chat server under test: a real axum server on loopback running
//! `handle_ws`, a scripted model, and an mddbd stand-in that accepts the gRPC
//! connection and closes it, so every lookup fails fast the way an unreachable
//! mddbd would.

pub mod llm;
pub mod ws_client;

use std::net::SocketAddr;
use std::sync::Arc;

use axum::Router;
use axum::extract::State;
use axum::extract::ws::WebSocketUpgrade;
use axum::response::IntoResponse;
use axum::routing::get;

use crate::chat::handler::handle_ws;
use crate::config::Config;
use crate::grpc::client::MddbClient;
use crate::grpc::link::MddbLink;
use crate::security::client_ip::TrustedProxies;
use crate::security::rate_limiter::RateLimiter;
use crate::security::sanitizer::Sanitizer;
use crate::session::manager::SessionManager;
use crate::state::AppState;
use crate::webhook::dispatcher::WebhookDispatcher;

pub use llm::ScriptedLlm;
pub use ws_client::WsClient;

/// The TOML for a server. Each section is a string the test may replace, so a
/// test states only what it cares about.
pub struct ConfigToml {
    pub session: String,
    pub security: String,
    pub scenarios: String,
    pub webhooks: String,
}

impl Default for ConfigToml {
    fn default() -> Self {
        Self {
            session: String::new(),
            security: String::new(),
            scenarios: r#"
[scenarios.assistant]
name = "assistant"
system_prompt = "Be brief."
allowed_collections = ["docs", "guides"]
"#
            .to_string(),
            webhooks: String::new(),
        }
    }
}

impl ConfigToml {
    /// One webhook subscribed to `events`.
    pub fn webhook(mut self, url: &str, events: &[&str]) -> Self {
        let list = events
            .iter()
            .map(|e| format!("\"{e}\""))
            .collect::<Vec<_>>()
            .join(", ");
        self.webhooks = format!("[[webhooks]]\nurl = \"{url}\"\nevents = [{list}]\n");
        self
    }

    pub fn session(mut self, toml: &str) -> Self {
        self.session = toml.to_string();
        self
    }

    pub fn security(mut self, toml: &str) -> Self {
        self.security = toml.to_string();
        self
    }

    pub fn scenarios(mut self, toml: &str) -> Self {
        self.scenarios = toml.to_string();
        self
    }

    fn render(&self) -> String {
        format!(
            "[server]\n[mddb]\n[llm]\nprovider = \"scripted\"\napi_url = \"http://llm.test\"\n\
             model = \"test-model\"\n[session]\n{}\n[security]\nwebhook_secret = \"hook-secret\"\n{}\n{}\n{}",
            self.session, self.security, self.scenarios, self.webhooks
        )
    }
}

pub struct Harness {
    pub state: Arc<AppState>,
    pub llm: Arc<ScriptedLlm>,
    addr: SocketAddr,
}

impl Harness {
    pub async fn start(config: ConfigToml) -> Self {
        let mut config: Config = toml::from_str(&config.render()).expect("the test TOML parses");
        config.mddb.grpc_addr = format!("http://{}", unreachable_mddbd().await);
        let client = MddbClient::connect(config.mddb.clone())
            .await
            .expect("the stand-in accepts the connection");
        Self::start_with_link(config, MddbLink::connected(client)).await
    }

    /// A server whose mddbd has not been reached yet (#401): the link is
    /// empty, as it is between startup and the first successful connect.
    pub async fn start_before_mddbd(config: ConfigToml) -> Self {
        let config: Config = toml::from_str(&config.render()).expect("the test TOML parses");
        Self::start_with_link(config, MddbLink::default()).await
    }

    async fn start_with_link(config: Config, mddb: MddbLink) -> Self {
        crate::webhook::test_support::enable_logs();
        let llm = Arc::new(ScriptedLlm::default());
        let state = Arc::new(AppState {
            session_manager: SessionManager::new(config.session.clone()),
            mddb,
            llm_provider: llm.clone(),
            webhook_dispatcher: WebhookDispatcher::new(
                config.webhooks.clone(),
                config.security.webhook_secret.clone(),
            ),
            rate_limiter: RateLimiter::new(config.security.rate_limit_per_minute),
            sanitizer: Sanitizer::new(config.security.clone()),
            trusted_proxies: TrustedProxies::parse(&[]),
            config,
        });

        let app = Router::new()
            .route("/ws", get(upgrade))
            .with_state(state.clone());
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind a loopback port");
        let addr = listener.local_addr().expect("local addr");
        tokio::spawn(async move { axum::serve(listener, app).await });

        Self { state, llm, addr }
    }

    pub async fn connect(&self) -> WsClient {
        WsClient::connect(self.addr, "/ws").await
    }

    /// Joins as `name` and returns the session id the server assigned.
    pub async fn join(&self, client: &mut WsClient, name: &str) -> String {
        client
            .send_json(serde_json::json!({"type": "join", "name": name}))
            .await;
        let frame = client.recv_json().await;
        assert_eq!(frame["type"], "session", "join failed: {frame}");
        frame["id"].as_str().expect("a session id").to_string()
    }
}

async fn upgrade(ws: WebSocketUpgrade, State(state): State<Arc<AppState>>) -> impl IntoResponse {
    ws.on_upgrade(move |socket| handle_ws(socket, state, "127.0.0.1".to_string()))
}

/// A listener that accepts and immediately closes: `MddbClient::connect`
/// succeeds (tonic connects lazily enough) and every RPC fails at once.
async fn unreachable_mddbd() -> SocketAddr {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind the stand-in");
    let addr = listener.local_addr().expect("local addr");
    tokio::spawn(async move {
        loop {
            drop(listener.accept().await);
        }
    });
    addr
}
