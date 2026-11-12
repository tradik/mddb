use serde::Deserialize;
use std::collections::HashMap;
use std::path::Path;

#[derive(Debug, Deserialize, Clone)]
pub struct Config {
    pub server: ServerConfig,
    pub mddb: MddbConfig,
    pub llm: LlmConfig,
    pub session: SessionConfig,
    pub security: SecurityConfig,
    #[serde(default)]
    pub scenarios: HashMap<String, Scenario>,
    #[serde(default)]
    pub scenarios_source: ScenariosSourceConfig,
    #[serde(default)]
    pub webhooks: Vec<WebhookConfig>,
    #[serde(default)]
    pub identity: IdentityConfig,
}

/// The fewest bytes an `[identity] secret` may have, as docs/API.md asks of
/// the secret shared with the host.
pub const MIN_IDENTITY_SECRET_BYTES: usize = 32;

/// Signed joins (#405): the secret a host signs its users' join tokens with.
///
/// Absent — the default — keeps every join anonymous, as before 2.19.0.
#[derive(Debug, Deserialize, Clone, Default, PartialEq, Eq)]
pub struct IdentityConfig {
    /// HMAC-SHA256 key shared with the host, at least 32 bytes. Empty here
    /// means `MDDB_CHAT_IDENTITY_SECRET`, and empty there means no signed
    /// joins.
    #[serde(default)]
    pub secret: String,
    /// Refuse a `join` without a valid token. Needs a secret.
    #[serde(default)]
    pub required: bool,
}

impl IdentityConfig {
    /// Whether a token can be verified at all.
    pub fn enabled(&self) -> bool {
        !self.secret.is_empty()
    }

    fn validate(&self) -> Result<(), String> {
        if self.enabled() && self.secret.len() < MIN_IDENTITY_SECRET_BYTES {
            return Err(format!(
                "[identity] secret must be at least {MIN_IDENTITY_SECRET_BYTES} bytes"
            ));
        }
        if self.required && !self.enabled() {
            return Err("[identity] required = true needs a secret: set [identity] secret or MDDB_CHAT_IDENTITY_SECRET".to_string());
        }
        Ok(())
    }
}

/// Where scenarios come from besides this file (#403).
///
/// mddbd stores chat scenarios since 2.19.0 and mddb-chat reads them over the
/// gRPC link it already has. The `[scenarios.*]` tables stay as the bootstrap
/// and the fallback: they serve until mddbd is reached, and for every name
/// mddbd has no scenario of.
#[derive(Debug, Deserialize, Clone, PartialEq, Eq)]
pub struct ScenariosSourceConfig {
    /// Seconds between two looks at mddbd's scenario version; the scenarios
    /// are reloaded when it changed. `0` turns reading from mddbd off, which
    /// leaves the TOML scenarios as the only ones.
    #[serde(default = "default_scenarios_poll_seconds")]
    pub poll_seconds: u64,
}

impl Default for ScenariosSourceConfig {
    fn default() -> Self {
        Self {
            poll_seconds: default_scenarios_poll_seconds(),
        }
    }
}

#[derive(Debug, Deserialize, Clone)]
pub struct ServerConfig {
    #[serde(default = "default_host")]
    pub host: String,
    #[serde(default = "default_port")]
    pub port: u16,
    /// Origins allowed to call the chat API from a browser.
    ///
    /// `["*"]` — the default — allows any origin, which matches a service
    /// bound to loopback for development. A deployment reachable from a
    /// browser should list its own origins: with `*`, any page on the internet
    /// can open a session against this server using the visitor's network
    /// position.
    #[serde(default = "default_cors_origins")]
    pub cors_origins: Vec<String>,
}

impl ServerConfig {
    /// Whether the configured origins mean "any origin".
    ///
    /// TEST-001: `cors_origins` was parsed, defaulted and documented, and
    /// main.rs built its CORS layer with `allow_origin(Any)` regardless — an
    /// operator who listed their own origins still served
    /// `Access-Control-Allow-Origin: *`.
    pub fn allows_any_origin(&self) -> bool {
        self.cors_origins.is_empty() || self.cors_origins.iter().any(|o| o == "*")
    }

    /// The configured origins as header values, dropping any that cannot be
    /// one.
    ///
    /// A malformed entry is skipped rather than failing startup: refusing to
    /// boot over one bad line in a list would take the service down, and the
    /// remaining origins still describe a narrower policy than `*`.
    pub fn allowed_origins(&self) -> Vec<axum::http::HeaderValue> {
        self.cors_origins
            .iter()
            .filter(|o| *o != "*")
            .filter_map(|o| match o.parse::<axum::http::HeaderValue>() {
                Ok(value) => Some(value),
                Err(_) => {
                    tracing::warn!(origin = %o, "ignoring an unparseable CORS origin");
                    None
                }
            })
            .collect()
    }
}

#[derive(Debug, Deserialize, Clone)]
pub struct MddbConfig {
    #[serde(default = "default_grpc_addr")]
    pub grpc_addr: String,
    /// mddbd's HTTP API, where the service account logs in. Empty — the
    /// default — derives it from `grpc_addr` when that uses the default gRPC
    /// port 11024 (HTTP on 11023); any other port needs it set.
    #[serde(default)]
    pub http_url: String,
    #[serde(default = "default_collection")]
    pub default_collection: String,
    // RAG-001: absent in the TOML means "let the collection decide". The
    // serde defaults used to fill these in, which made a value the operator
    // never wrote indistinguishable from one they did — so a collection
    // profile could never take effect.
    #[serde(default)]
    pub search_top_k: Option<u32>,
    #[serde(default)]
    pub search_type: Option<String>,
    /// Characters of one search result the model gets to see (#398).
    ///
    /// Counted in characters, cut on a character boundary. 0 means the whole
    /// chunk. The default is the literal that used to live in tools.rs, so a
    /// config.toml without this key behaves as before.
    #[serde(default = "default_result_max_chars")]
    pub result_max_chars: usize,
    #[serde(default)]
    pub auth_username: String,
    #[serde(default)]
    pub auth_password: String,
    /// The longest wait between two attempts to reach mddbd at startup, in
    /// seconds (#401).
    ///
    /// mddb-chat starts serving at once and keeps trying in the background,
    /// waiting 1 s, 2 s, 4 s... up to this many seconds between attempts, for
    /// as long as it takes; `/health` answers 503 until the link is up. `0`
    /// turns retrying off: one attempt before the port is opened, and exit on
    /// failure — the behaviour before this setting existed.
    #[serde(default = "default_connect_retry_max_seconds")]
    pub connect_retry_max_seconds: u64,
}

/// mddbd's default ports, the only pair `http_url` is derived for.
const DEFAULT_GRPC_PORT: u16 = 11024;
const DEFAULT_HTTP_PORT: u16 = 11023;

impl MddbConfig {
    /// The base URL of mddbd's HTTP API, without a trailing slash.
    ///
    /// `http_url` when set. Otherwise `grpc_addr` with its port moved from
    /// 11024 to 11023 — and only that: the address is parsed, so a port or a
    /// host name that merely contains "11024" is not rewritten the way the
    /// old string replace rewrote it, and a non-default port is an error
    /// naming the setting rather than a login sent to the wrong place.
    pub fn http_base(&self) -> Result<String, String> {
        if !self.http_url.is_empty() {
            return Ok(self.http_url.trim_end_matches('/').to_string());
        }
        let required = || {
            format!(
                "[mddb] http_url is required: grpc_addr {:?} does not use the default port {DEFAULT_GRPC_PORT}, so the HTTP login URL cannot be derived from it",
                self.grpc_addr
            )
        };
        let uri: axum::http::Uri = self.grpc_addr.parse().map_err(|_| required())?;
        let (Some(scheme), Some(host), Some(DEFAULT_GRPC_PORT)) =
            (uri.scheme_str(), uri.host(), uri.port_u16())
        else {
            return Err(required());
        };
        let scheme = if scheme == "grpc" { "http" } else { scheme };
        Ok(format!("{scheme}://{host}:{DEFAULT_HTTP_PORT}"))
    }
}

#[derive(Debug, Deserialize, Clone)]
pub struct LlmConfig {
    #[serde(default = "default_llm_provider")]
    pub provider: String,
    pub api_url: String,
    #[serde(default)]
    pub api_key: String,
    pub model: String,
    #[serde(default = "default_max_tokens")]
    pub max_tokens: u32,
    #[serde(default = "default_temperature")]
    pub temperature: f32,
    /// Extra fields merged into every request to an OpenAI-compatible
    /// endpoint (#400), for parameters this server does not model — e.g.
    /// `extra_body = { reasoning_effort = "none" }` to stop a thinking model
    /// behind Ollama from spending `max_tokens` on reasoning.
    ///
    /// Any TOML value is passed through as JSON. Keys the request itself sets
    /// (`model`, `messages`, `max_tokens`, `temperature`, `stream`, `tools`)
    /// are ignored here: the request wins. Not used by the Anthropic provider.
    #[serde(default)]
    pub extra_body: serde_json::Map<String, serde_json::Value>,
    /// `[llm.loop_guard]` (#413): stops an answer caught in a repetition
    /// loop. On with conservative defaults when the table is absent.
    #[serde(default)]
    pub loop_guard: crate::chat::loop_guard::LoopGuardConfig,
}

#[derive(Debug, Deserialize, Clone)]
pub struct SessionConfig {
    #[serde(default = "default_max_concurrent")]
    pub max_concurrent: usize,
    #[serde(default = "default_queue_size")]
    pub queue_size: usize,
    #[serde(default = "default_max_history")]
    pub max_history_length: usize,
    #[serde(default = "default_session_ttl")]
    pub session_ttl_minutes: u64,
    #[serde(default = "default_max_response_length")]
    pub max_response_length: usize,
    #[serde(default = "default_name_max_chars")]
    pub name_max_chars: usize,
}

#[derive(Debug, Deserialize, Clone)]
pub struct SecurityConfig {
    #[serde(default = "default_rate_limit")]
    pub rate_limit_per_minute: u32,
    #[serde(default = "default_max_message_length")]
    pub max_message_length: usize,
    #[serde(default)]
    pub webhook_secret: String,
    /// Tokens one session may spend before it is refused (RAG-005).
    ///
    /// 0 (the default) means unlimited, which is the behaviour before this
    /// existed. `max_turns` caps how many turns a session takes and says
    /// nothing about what they cost; this is the limit a budget actually runs
    /// out in.
    #[serde(default)]
    pub max_tokens_per_session: u64,
    /// Addresses or CIDRs whose `X-Forwarded-For` is believed (SEC-014).
    ///
    /// Empty by default, which charges the rate limit to the TCP peer — right
    /// for a directly exposed server, wrong behind the reverse proxy this is
    /// documented to run behind, where every visitor arrives from the proxy
    /// and shares one bucket. Set this to the proxy's address or network to
    /// get per-visitor limits back.
    #[serde(default)]
    pub trusted_proxies: Vec<String>,
}

#[derive(Debug, Deserialize, Clone)]
pub struct Scenario {
    pub name: String,
    pub system_prompt: String,
    #[serde(default)]
    pub allowed_collections: Vec<String>,
    #[serde(default)]
    pub temperature: Option<f32>,
    /// How many user messages this scenario accepts in one session.
    ///
    /// `None` means no limit. A limit caps both the conversation's length and
    /// the LLM spend a single visitor can cause.
    #[serde(default)]
    pub max_turns: Option<usize>,
    /// Days a signed user's conversation in this scenario is kept after its
    /// last message (#405). `None` leaves it to mddbd's default
    /// (`MDDB_CHAT_RETENTION_DAYS`, 30).
    #[serde(default)]
    pub retention_days: Option<u32>,
}

#[derive(Debug, Deserialize, Clone)]
pub struct WebhookConfig {
    pub url: String,
    /// Which events this URL receives; see `webhook::events::ALL`.
    ///
    /// Absent means the three events that existed before #399, so a config
    /// written for an older server does not start receiving per-message
    /// traffic because the server learned new events.
    #[serde(default = "crate::webhook::events::legacy_events")]
    pub events: Vec<String>,
    #[serde(default)]
    pub headers: HashMap<String, String>,
}

impl Config {
    pub fn load(path: &Path) -> Result<Self, Box<dyn std::error::Error>> {
        let content = std::fs::read_to_string(path)?;
        let mut config: Config = toml::from_str(&content)?;

        // Override api_key from env if empty
        if config.llm.api_key.is_empty()
            && let Ok(key) = std::env::var("MDDB_CHAT_LLM_API_KEY")
        {
            config.llm.api_key = key;
        }

        // Override webhook secret from env if empty
        if config.security.webhook_secret.is_empty()
            && let Ok(secret) = std::env::var("MDDB_CHAT_WEBHOOK_SECRET")
        {
            config.security.webhook_secret = secret;
        }

        // Override MDDB auth credentials from env if empty (SEC-007): keeps the
        // real password out of a committed config.toml — the env value wins.
        if config.mddb.auth_username.is_empty()
            && let Ok(user) = std::env::var("MDDB_CHAT_AUTH_USERNAME")
        {
            config.mddb.auth_username = user;
        }
        if config.mddb.auth_password.is_empty()
            && let Ok(pass) = std::env::var("MDDB_CHAT_AUTH_PASSWORD")
        {
            config.mddb.auth_password = pass;
        }

        // #405: the secret shared with the host, kept out of the TOML like
        // the other secrets.
        if config.identity.secret.is_empty()
            && let Ok(secret) = std::env::var("MDDB_CHAT_IDENTITY_SECRET")
        {
            config.identity.secret = secret;
        }
        config.validate()?;

        // Ensure default scenario exists
        if !config.scenarios.contains_key("assistant") {
            config.scenarios.insert(
                "assistant".to_string(),
                Scenario {
                    name: "Documentation Assistant".to_string(),
                    system_prompt: "You are a helpful documentation assistant. Answer questions based on the provided context. Be concise and accurate.".to_string(),
                    allowed_collections: vec![],
                    temperature: None,
                    max_turns: None,
                    retention_days: None,
                },
            );
        }

        Ok(config)
    }

    /// Settings that cannot work, refused at startup rather than at the
    /// first visitor: an `[identity]` that cannot verify, and credentials
    /// whose login URL cannot be found.
    pub fn validate(&self) -> Result<(), String> {
        self.identity.validate()?;
        self.llm.loop_guard.validate()?;
        if !self.mddb.auth_username.is_empty() {
            self.mddb.http_base()?;
        }
        Ok(())
    }

    pub fn get_scenario(&self, name: &str) -> Option<&Scenario> {
        self.scenarios.get(name)
    }
}

fn default_host() -> String {
    "0.0.0.0".to_string()
}
fn default_port() -> u16 {
    11030
}
fn default_cors_origins() -> Vec<String> {
    vec!["*".to_string()]
}
fn default_grpc_addr() -> String {
    "http://localhost:11024".to_string()
}
fn default_connect_retry_max_seconds() -> u64 {
    30
}
fn default_scenarios_poll_seconds() -> u64 {
    10
}
fn default_collection() -> String {
    "docs".to_string()
}
fn default_result_max_chars() -> usize {
    800
}
/// Built-in fallbacks, used only when neither the TOML nor the collection's
/// retrieval profile says anything.
pub const FALLBACK_SEARCH_TOP_K: u32 = 5;
pub const FALLBACK_SEARCH_TYPE: &str = "hybrid";
fn default_llm_provider() -> String {
    "openai".to_string()
}
fn default_max_tokens() -> u32 {
    1024
}
fn default_temperature() -> f32 {
    0.7
}
fn default_max_concurrent() -> usize {
    2
}
fn default_queue_size() -> usize {
    10
}
fn default_max_history() -> usize {
    50
}
fn default_session_ttl() -> u64 {
    1440
}
fn default_max_response_length() -> usize {
    4096
}
fn default_name_max_chars() -> usize {
    50
}
fn default_rate_limit() -> u32 {
    30
}
fn default_max_message_length() -> usize {
    2000
}

#[cfg(test)]
#[path = "config_tests.rs"]
mod identity_tests;

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    // Minimal valid config with empty MDDB auth — exercises the SEC-007 env override.
    const MINIMAL: &str = r#"
[server]
[mddb]
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
"#;

    fn write_temp(name: &str, body: &str) -> std::path::PathBuf {
        let mut p = std::env::temp_dir();
        let fname = format!("mddb-chat-cfg-{}-{}.toml", std::process::id(), name);
        p.push(fname);
        let mut f = std::fs::File::create(&p).unwrap();
        f.write_all(body.as_bytes()).unwrap();
        p
    }

    // One sequential test: env vars are process-global, so we must not run the
    // two cases concurrently. set_var/remove_var are unsafe in edition 2024.
    #[test]
    fn mddb_auth_env_override_precedence() {
        // 1) Empty config auth -> env wins (SEC-007). The LLM key and the
        //    webhook secret follow the same rule, checked in the same test for
        //    the same process-global reason.
        let empty = write_temp("empty", MINIMAL);
        unsafe {
            std::env::set_var("MDDB_CHAT_AUTH_USERNAME", "envuser");
            std::env::set_var("MDDB_CHAT_AUTH_PASSWORD", "envpass");
            std::env::set_var("MDDB_CHAT_LLM_API_KEY", "envkey");
            std::env::set_var("MDDB_CHAT_WEBHOOK_SECRET", "envsecret");
        }
        let cfg = Config::load(&empty).unwrap();
        assert_eq!(cfg.mddb.auth_username, "envuser");
        assert_eq!(cfg.mddb.auth_password, "envpass");
        assert_eq!(cfg.llm.api_key, "envkey");
        assert_eq!(cfg.security.webhook_secret, "envsecret");
        unsafe {
            std::env::remove_var("MDDB_CHAT_LLM_API_KEY");
            std::env::remove_var("MDDB_CHAT_WEBHOOK_SECRET");
        }

        // 2) Non-empty config auth -> config wins over env.
        let with_auth = write_temp(
            "withauth",
            r#"
[server]
[mddb]
auth_username = "fileuser"
auth_password = "filepass"
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
"#,
        );
        let cfg2 = Config::load(&with_auth).unwrap();
        assert_eq!(cfg2.mddb.auth_username, "fileuser");
        assert_eq!(cfg2.mddb.auth_password, "filepass");

        unsafe {
            std::env::remove_var("MDDB_CHAT_AUTH_USERNAME");
            std::env::remove_var("MDDB_CHAT_AUTH_PASSWORD");
        }
        std::fs::remove_file(&empty).ok();
        std::fs::remove_file(&with_auth).ok();
    }

    // TEST-001. Two fields were parsed, defaulted, documented — and never
    // read. A configuration that has no effect is worse than one that is
    // missing: the operator believes the setting is in force.

    fn config_from(body: &str, name: &str) -> Config {
        Config::load(&write_temp(name, body)).expect("the fixture should parse")
    }

    #[test]
    fn cors_defaults_to_any_origin() {
        let cfg = config_from(MINIMAL, "cors-default");

        assert_eq!(cfg.server.cors_origins, vec!["*".to_string()]);
        assert!(
            cfg.server.allows_any_origin(),
            "the default must be recognisable as the wildcard it is"
        );
        assert!(cfg.server.allowed_origins().is_empty());
    }

    #[test]
    fn configured_origins_narrow_the_policy() {
        let cfg = config_from(
            r#"
[server]
cors_origins = ["https://docs.example.test", "https://app.example.test"]
[mddb]
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
"#,
            "cors-listed",
        );

        assert!(
            !cfg.server.allows_any_origin(),
            "a server with listed origins still served Access-Control-Allow-Origin: *"
        );

        let origins: Vec<String> = cfg
            .server
            .allowed_origins()
            .iter()
            .map(|v| v.to_str().unwrap().to_string())
            .collect();
        assert_eq!(
            origins,
            vec![
                "https://docs.example.test".to_string(),
                "https://app.example.test".to_string()
            ]
        );
    }

    // A wildcard alongside real origins still means "any": the widest entry
    // wins, and pretending otherwise would advertise a policy the layer does
    // not apply.
    #[test]
    fn a_wildcard_among_named_origins_still_means_any() {
        let cfg = config_from(
            r#"
[server]
cors_origins = ["https://docs.example.test", "*"]
[mddb]
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
"#,
            "cors-mixed",
        );

        assert!(cfg.server.allows_any_origin());
    }

    // An empty list is not "deny everything": it is a list that says nothing,
    // and a CORS layer with no origins would refuse every browser.
    #[test]
    fn an_empty_origin_list_is_treated_as_any() {
        let cfg = config_from(
            r#"
[server]
cors_origins = []
[mddb]
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
"#,
            "cors-empty",
        );

        assert!(cfg.server.allows_any_origin());
    }

    // Refusing to boot over one bad line would take the service down; the
    // remaining origins still describe a narrower policy than `*`.
    #[test]
    fn an_unparseable_origin_is_skipped_not_fatal() {
        let cfg = config_from(
            r#"
[server]
cors_origins = ["https://good.example.test", "bad\u0007origin"]
[mddb]
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
"#,
            "cors-bad",
        );

        let origins = cfg.server.allowed_origins();
        assert_eq!(
            origins.len(),
            1,
            "the good origin did not survive the bad one"
        );
        assert_eq!(origins[0].to_str().unwrap(), "https://good.example.test");
    }

    #[test]
    fn a_scenario_turn_limit_survives_the_toml() {
        let cfg = config_from(
            r#"
[server]
[mddb]
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
[scenarios.demo]
name = "demo"
system_prompt = "be brief"
max_turns = 5
"#,
            "turns",
        );

        let scenario = cfg.get_scenario("demo").expect("the scenario should load");
        assert_eq!(scenario.max_turns, Some(5));
    }

    // #400: a table of arbitrary values, passed through to the LLM endpoint.
    #[test]
    fn extra_body_is_empty_unless_configured() {
        let cfg = config_from(MINIMAL, "extra-default");
        assert!(cfg.llm.extra_body.is_empty());
    }

    #[test]
    fn extra_body_keeps_every_kind_of_toml_value_as_json() {
        let extra = r#"extra_body = { reasoning_effort = "none", seed = 7, top_p = 0.5, think = false, stop = ["a"], options = { num_ctx = 8192 } }"#;
        let cfg = config_from(
            &MINIMAL.replace("[session]", &format!("{extra}\n[session]")),
            "extra-set",
        );

        assert_eq!(
            serde_json::Value::Object(cfg.llm.extra_body),
            serde_json::json!({
                "reasoning_effort": "none",
                "seed": 7,
                "top_p": 0.5,
                "think": false,
                "stop": ["a"],
                "options": {"num_ctx": 8192},
            })
        );
    }

    // #401: an old config.toml has no such key and must get the retry.
    #[test]
    fn connecting_to_mddbd_is_retried_by_default() {
        let cfg = config_from(MINIMAL, "retry-default");
        assert_eq!(cfg.mddb.connect_retry_max_seconds, 30);
    }

    #[test]
    fn retrying_can_be_turned_off_for_fail_fast_startup() {
        let cfg = config_from(
            &MINIMAL.replace("[mddb]", "[mddb]\nconnect_retry_max_seconds = 0"),
            "retry-off",
        );
        assert_eq!(cfg.mddb.connect_retry_max_seconds, 0);
    }

    // #403: an old config.toml has no [scenarios_source] and gets the poll.
    #[test]
    fn scenarios_are_polled_from_mddbd_every_ten_seconds_by_default() {
        let cfg = config_from(MINIMAL, "scenarios-source-default");
        assert_eq!(cfg.scenarios_source.poll_seconds, 10);
        assert_eq!(cfg.scenarios_source, ScenariosSourceConfig::default());
    }

    #[test]
    fn the_scenario_poll_interval_is_read_from_the_toml() {
        let with_section = format!("{MINIMAL}\n[scenarios_source]\npoll_seconds = 0\n");
        assert_eq!(
            config_from(&with_section, "scenarios-source-off")
                .scenarios_source
                .poll_seconds,
            0
        );

        // The section without the key still means the default.
        let empty_section = format!("{MINIMAL}\n[scenarios_source]\n");
        assert_eq!(
            config_from(&empty_section, "scenarios-source-empty")
                .scenarios_source
                .poll_seconds,
            10
        );
    }

    #[test]
    fn an_unknown_scenario_is_not_invented() {
        let cfg = config_from(MINIMAL, "unknown-scenario");
        assert!(cfg.get_scenario("does-not-exist").is_none());
    }

    // #398: the per-result limit used to be a literal 800 in tools.rs. An old
    // config.toml that never heard of the key must keep loading with the same
    // number, and a new one may say otherwise.
    #[test]
    fn result_max_chars_defaults_to_the_old_literal() {
        let cfg = config_from(MINIMAL, "result-default");
        assert_eq!(cfg.mddb.result_max_chars, 800);
    }

    #[test]
    fn result_max_chars_is_read_from_the_toml() {
        let cfg = config_from(
            r#"
[server]
[mddb]
result_max_chars = 2000
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
"#,
            "result-set",
        );
        assert_eq!(cfg.mddb.result_max_chars, 2000);
    }

    // #399: a webhook written before the per-message events existed names no
    // events, and must keep getting exactly what it got.
    #[test]
    fn a_webhook_without_events_receives_the_three_original_ones() {
        let cfg = config_from(
            r#"
[server]
[mddb]
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
[[webhooks]]
url = "https://example.test/hook"
"#,
            "webhook-default-events",
        );

        assert_eq!(
            cfg.webhooks[0].events,
            vec!["session.start", "session.end", "queue.full"]
        );
    }

    #[test]
    fn a_webhook_that_lists_events_gets_only_those() {
        let cfg = config_from(
            r#"
[server]
[mddb]
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
[[webhooks]]
url = "https://example.test/hook"
events = ["message", "answer", "feedback"]
"#,
            "webhook-listed-events",
        );

        assert_eq!(
            cfg.webhooks[0].events,
            vec!["message", "answer", "feedback"]
        );
    }
}
