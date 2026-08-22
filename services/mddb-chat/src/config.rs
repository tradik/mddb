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
    #[serde(default = "default_cors_origins")]
    pub cors_origins: Vec<String>,
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
    #[serde(default = "default_true")]
    pub stream: bool,
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
}

#[derive(Debug, Deserialize, Clone)]
pub struct Scenario {
    pub name: String,
    pub system_prompt: String,
    #[serde(default)]
    pub allowed_collections: Vec<String>,
    #[serde(default)]
    pub temperature: Option<f32>,
    #[serde(default)]
    pub max_turns: Option<usize>,
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
        if config.llm.api_key.is_empty() {
            if let Ok(key) = std::env::var("MDDB_CHAT_LLM_API_KEY") {
                config.llm.api_key = key;
            }
        }

        // Override webhook secret from env if empty
        if config.security.webhook_secret.is_empty() {
            if let Ok(secret) = std::env::var("MDDB_CHAT_WEBHOOK_SECRET") {
                config.security.webhook_secret = secret;
            }
        }

        // Override MDDB auth credentials from env if empty (SEC-007): keeps the
        // real password out of a committed config.toml — the env value wins.
        if config.mddb.auth_username.is_empty() {
            if let Ok(user) = std::env::var("MDDB_CHAT_AUTH_USERNAME") {
                config.mddb.auth_username = user;
            }
        }
        if config.mddb.auth_password.is_empty() {
            if let Ok(pass) = std::env::var("MDDB_CHAT_AUTH_PASSWORD") {
                config.mddb.auth_password = pass;
            }
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

fn default_host() -> String { "0.0.0.0".to_string() }
fn default_port() -> u16 { 11030 }
fn default_cors_origins() -> Vec<String> { vec!["*".to_string()] }
fn default_grpc_addr() -> String { "http://localhost:11024".to_string() }
fn default_collection() -> String { "docs".to_string() }
/// Built-in fallbacks, used only when neither the TOML nor the collection's
/// retrieval profile says anything.
pub const FALLBACK_SEARCH_TOP_K: u32 = 5;
pub const FALLBACK_SEARCH_TYPE: &str = "hybrid";
fn default_llm_provider() -> String { "openai".to_string() }
fn default_max_tokens() -> u32 { 1024 }
fn default_temperature() -> f32 { 0.7 }
fn default_true() -> bool { true }
fn default_max_concurrent() -> usize { 2 }
fn default_queue_size() -> usize { 10 }
fn default_max_history() -> usize { 50 }
fn default_session_ttl() -> u64 { 1440 }
fn default_max_response_length() -> usize { 4096 }
fn default_name_max_chars() -> usize { 50 }
fn default_rate_limit() -> u32 { 30 }
fn default_max_message_length() -> usize { 2000 }

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
}
