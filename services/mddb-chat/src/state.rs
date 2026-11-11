use std::sync::Arc;

use crate::chat::scenario::ScenarioStore;
use crate::config::Config;
use crate::grpc::link::MddbLink;
use crate::llm::provider::LlmProvider;
use crate::security::client_ip::TrustedProxies;
use crate::security::rate_limiter::RateLimiter;
use crate::security::sanitizer::Sanitizer;
use crate::session::manager::SessionManager;
use crate::webhook::dispatcher::WebhookDispatcher;

pub struct AppState {
    pub config: Config,
    pub session_manager: SessionManager,
    /// Empty until mddbd has been reached (#401); see grpc/link.rs.
    pub mddb: MddbLink,
    /// The scenarios last read from mddbd (#403); empty until then, and the
    /// TOML ones in `config` fill in for every name it does not have.
    pub scenarios: ScenarioStore,
    pub llm_provider: Arc<dyn LlmProvider>,
    pub webhook_dispatcher: WebhookDispatcher,
    pub rate_limiter: RateLimiter,
    pub sanitizer: Sanitizer,
    /// Parsed once at startup (SEC-014); see security/client_ip.rs.
    pub trusted_proxies: TrustedProxies,
}
