//! A scenario as mddbd stores it, reduced to what mddb-chat applies (#403).

use std::sync::Arc;

use tracing::warn;

use crate::chat::mcp::{McpServers, limits_of};
use crate::grpc::client::proto::{ChatMcpServerStateProto, ChatScenarioProto};

/// One scenario read from mddbd, with the prompt mddbd composed for it.
///
/// mddbd writes "not set" as a zero or an empty string; here that becomes
/// `None`, so the difference between "0" and "nothing" is decided once, at
/// the edge. Style and off-topic behaviour are not here: they are sentences
/// inside the composed prompt. Skills are names only until the skill store
/// exists (#404) and are ignored.
#[derive(Debug, Clone, PartialEq)]
pub struct StoredScenario {
    pub enabled: bool,
    /// What a scenario picker shows: the description, or the name without one.
    pub label: String,
    /// The system prompt as mddbd composed it when the scenario was loaded.
    pub prompt: String,
    pub allowed_collections: Vec<String>,
    pub model: Option<String>,
    pub temperature: Option<f32>,
    pub max_tokens: Option<u32>,
    pub extra_body: serde_json::Map<String, serde_json::Value>,
    pub max_turns: Option<usize>,
    /// Tokens per session; 0 means the scenario sets no budget.
    pub token_budget: u64,
    /// Messages per session per minute; 0 means no limit.
    pub messages_per_minute: u32,
    /// The external MCP servers (MCP-005): their states as mddbd reported
    /// them at the load — a turn asks again and uses these only when that
    /// fails — and each one's timeout and result size.
    pub mcp: Arc<McpServers>,
}

impl StoredScenario {
    pub fn from_proto(scenario: ChatScenarioProto, prompt: String) -> Self {
        let llm = scenario.llm.unwrap_or_default();
        let label = match scenario.description.trim() {
            "" => scenario.name.clone(),
            description => description.to_string(),
        };
        Self {
            mcp: Arc::new(McpServers {
                states: Vec::new(),
                limits: limits_of(&scenario.mcp_servers),
            }),
            enabled: scenario.enabled,
            label,
            prompt,
            allowed_collections: scenario.allowed_collections,
            extra_body: parse_extra_body(&scenario.name, &llm.extra_body_json),
            model: Some(llm.model).filter(|model| !model.is_empty()),
            // 0 is a temperature, so mddbd says separately whether it is set.
            temperature: llm.temperature_set.then_some(llm.temperature as f32),
            max_tokens: positive(llm.max_tokens),
            max_turns: positive(scenario.max_turns),
            token_budget: positive(scenario.token_budget_per_session).unwrap_or(0),
            messages_per_minute: scenario
                .rate_limit
                .and_then(|limit| positive(limit.messages_per_minute))
                .unwrap_or(0),
        }
    }

    /// The same scenario with the servers' states of its preview (MCP-005).
    pub fn with_mcp_states(self, states: Vec<ChatMcpServerStateProto>) -> Self {
        let limits = self.mcp.limits.clone();
        Self {
            mcp: Arc::new(McpServers { states, limits }),
            ..self
        }
    }
}

/// `value` in the type the server counts in, or `None` for mddbd's "not set"
/// (0) and for anything that cannot be a count.
fn positive<N, T>(value: N) -> Option<T>
where
    N: PartialOrd + Default,
    T: TryFrom<N>,
{
    if value > N::default() {
        T::try_from(value).ok()
    } else {
        None
    }
}

/// The scenario's extra request fields. mddbd validates that this is a JSON
/// object; anything else is dropped with a warning rather than failing the
/// whole reload over one field.
fn parse_extra_body(
    scenario: &str,
    extra_body_json: &str,
) -> serde_json::Map<String, serde_json::Value> {
    if extra_body_json.trim().is_empty() {
        return serde_json::Map::new();
    }
    match serde_json::from_str(extra_body_json) {
        Ok(serde_json::Value::Object(body)) => body,
        _ => {
            warn!(
                scenario,
                "the scenario's llm.extraBody is not a JSON object; ignored"
            );
            serde_json::Map::new()
        }
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;
    use crate::grpc::client::proto::{ChatLlmProto, ChatRateLimitProto};
    use crate::grpc::testing::scenario;

    fn stored(scenario: ChatScenarioProto) -> StoredScenario {
        StoredScenario::from_proto(scenario, "Composed.".to_string())
    }

    #[test]
    fn a_stored_scenario_keeps_what_it_sets_and_leaves_the_rest_unset() {
        let full = stored(ChatScenarioProto {
            description: " Support desk ".to_string(),
            llm: Some(ChatLlmProto {
                model: "gpt-5-mini".to_string(),
                temperature: 0.0,
                temperature_set: true,
                max_tokens: 800,
                extra_body_json: r#"{"reasoning_effort":"low"}"#.to_string(),
            }),
            max_turns: 12,
            token_budget_per_session: 50_000,
            rate_limit: Some(ChatRateLimitProto {
                messages_per_minute: 20,
            }),
            skills: vec!["triage".to_string()],
            ..scenario("support", &["docs", "runbooks"])
        });

        assert!(full.enabled);
        assert_eq!(full.label, "Support desk");
        assert_eq!(full.prompt, "Composed.");
        assert_eq!(full.allowed_collections, vec!["docs", "runbooks"]);
        assert_eq!(full.model.as_deref(), Some("gpt-5-mini"));
        assert_eq!(full.temperature, Some(0.0), "0 is a temperature");
        assert_eq!(full.max_tokens, Some(800));
        assert_eq!(full.extra_body.get("reasoning_effort"), Some(&json!("low")));
        assert_eq!(full.max_turns, Some(12));
        assert_eq!(full.token_budget, 50_000);
        assert_eq!(full.messages_per_minute, 20);

        let bare = stored(scenario("bare", &[]));
        assert_eq!(bare.label, "bare", "no description: the name is the label");
        assert_eq!(bare.model, None);
        assert_eq!(bare.temperature, None);
        assert_eq!(bare.max_tokens, None);
        assert!(bare.extra_body.is_empty());
        assert_eq!(bare.max_turns, None);
        assert_eq!((bare.token_budget, bare.messages_per_minute), (0, 0));
    }

    #[test]
    fn nonsense_from_mddbd_counts_as_not_set() {
        let odd = stored(ChatScenarioProto {
            llm: Some(ChatLlmProto {
                // Sent without the flag: not a temperature.
                temperature: 1.5,
                max_tokens: -4,
                extra_body_json: "[1, 2]".to_string(),
                ..Default::default()
            }),
            max_turns: -1,
            token_budget_per_session: -9,
            rate_limit: Some(ChatRateLimitProto {
                messages_per_minute: -2,
            }),
            ..scenario("odd", &[])
        });

        assert_eq!(odd.temperature, None);
        assert_eq!(odd.max_tokens, None);
        assert!(odd.extra_body.is_empty(), "an array is not an object");
        assert_eq!(odd.max_turns, None);
        assert_eq!((odd.token_budget, odd.messages_per_minute), (0, 0));

        let broken = stored(ChatScenarioProto {
            llm: Some(ChatLlmProto {
                extra_body_json: "{not json".to_string(),
                ..Default::default()
            }),
            ..scenario("broken", &[])
        });
        assert!(broken.extra_body.is_empty());
    }
}
