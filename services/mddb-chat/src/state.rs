use std::sync::Arc;

use crate::config::Config;
use crate::grpc::client::MddbClient;
use crate::llm::provider::LlmProvider;
use crate::security::client_ip::TrustedProxies;
use crate::security::rate_limiter::RateLimiter;
use crate::security::sanitizer::Sanitizer;
use crate::session::manager::SessionManager;
use crate::webhook::dispatcher::WebhookDispatcher;

pub struct AppState {
    pub config: Config,
    pub session_manager: SessionManager,
    pub mddb_client: MddbClient,
    pub llm_provider: Arc<dyn LlmProvider>,
    pub webhook_dispatcher: WebhookDispatcher,
    pub rate_limiter: RateLimiter,
    pub sanitizer: Sanitizer,
    /// Parsed once at startup (SEC-014); see security/client_ip.rs.
    pub trusted_proxies: TrustedProxies,
}
