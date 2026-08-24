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
    pub webhooks: Vec<WebhookConfig>,
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
    #[serde(default)]
    pub auth_username: String,
    #[serde(default)]
    pub auth_password: String,
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
}

impl Scenario {
    /// Whether a session that has already sent `turns_taken` user messages may
    /// send another.
    ///
    /// TEST-001: `max_turns` was parsed from the TOML and never read, so a
    /// scenario that declared a limit did not have one — an operator capping a
    /// public demo at ten turns was serving an unbounded one.
    pub fn allows_another_turn(&self, turns_taken: usize) -> bool {
        match self.max_turns {
            Some(max) => turns_taken < max,
            None => true,
        }
    }
}

#[derive(Debug, Deserialize, Clone)]
pub struct WebhookConfig {
    pub url: String,
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
                },
            );
        }

        Ok(config)
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
fn default_collection() -> String {
    "docs".to_string()
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
        // 1) Empty config auth -> env wins (SEC-007).
        let empty = write_temp("empty", MINIMAL);
        unsafe {
            std::env::set_var("MDDB_CHAT_AUTH_USERNAME", "envuser");
            std::env::set_var("MDDB_CHAT_AUTH_PASSWORD", "envpass");
        }
        let cfg = Config::load(&empty).unwrap();
        assert_eq!(cfg.mddb.auth_username, "envuser");
        assert_eq!(cfg.mddb.auth_password, "envpass");

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
    fn a_scenario_without_a_turn_limit_never_runs_out() {
        let scenario = Scenario {
            name: "default".into(),
            system_prompt: "be helpful".into(),
            allowed_collections: vec![],
            temperature: None,
            max_turns: None,
        };

        assert!(scenario.allows_another_turn(0));
        assert!(scenario.allows_another_turn(10_000));
    }

    #[test]
    fn a_turn_limit_is_reached_after_that_many_turns() {
        let scenario = Scenario {
            name: "demo".into(),
            system_prompt: "be brief".into(),
            allowed_collections: vec![],
            temperature: None,
            max_turns: Some(3),
        };

        // Turns already taken, so the third message is the last one allowed.
        assert!(scenario.allows_another_turn(0));
        assert!(scenario.allows_another_turn(2));
        assert!(!scenario.allows_another_turn(3));
        assert!(!scenario.allows_another_turn(99));
    }

    #[test]
    fn a_zero_turn_limit_refuses_the_first_message() {
        let scenario = Scenario {
            name: "closed".into(),
            system_prompt: "".into(),
            allowed_collections: vec![],
            temperature: None,
            max_turns: Some(0),
        };

        assert!(!scenario.allows_another_turn(0));
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
        assert!(!scenario.allows_another_turn(5));
    }

    #[test]
    fn an_unknown_scenario_is_not_invented() {
        let cfg = config_from(MINIMAL, "unknown-scenario");
        assert!(cfg.get_scenario("does-not-exist").is_none());
    }
}
