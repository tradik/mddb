//! What one scenario lets its tools do (#396).

use tracing::warn;

use crate::chat::scenario;
use crate::config::Config;

/// The bounds a scenario puts on its tools.
///
/// #396: the `collection` argument came from the model and went straight to
/// the database, so a prompt-injected or confused model could read anything
/// the service account could. Every tool call now resolves its collection
/// through this, and the schema advertises the same set as an `enum`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ToolPolicy {
    /// Searched when the model names no collection.
    pub default_collection: String,
    /// Every collection the model may name; always contains the default.
    pub allowed_collections: Vec<String>,
    /// Characters of one search result; 0 means the whole chunk (#398).
    pub result_max_chars: usize,
}

impl ToolPolicy {
    /// The policy for a scenario. A scenario with an empty
    /// `allowed_collections` — or an unknown scenario — allows only the
    /// default collection.
    pub fn for_scenario(config: &Config, scenario_name: &str) -> Self {
        let default_collection = scenario::get_collection(config, scenario_name);
        let mut allowed_collections = config
            .get_scenario(scenario_name)
            .map(|s| s.allowed_collections.clone())
            .unwrap_or_default();
        if allowed_collections.is_empty() {
            allowed_collections.push(default_collection.clone());
        }
        Self {
            default_collection,
            allowed_collections,
            result_max_chars: config.mddb.result_max_chars,
        }
    }

    /// The collection a tool call may search, or the refusal to hand back to
    /// the model. Logged at warn: a model asking for a collection it was never
    /// offered is worth an operator's attention.
    pub fn resolve<'a>(
        &'a self,
        tool: &str,
        requested: Option<&'a str>,
    ) -> Result<&'a str, String> {
        let Some(collection) = requested else {
            return Ok(&self.default_collection);
        };
        if self.allowed_collections.iter().any(|c| c == collection) {
            return Ok(collection);
        }
        warn!(
            tool,
            collection,
            allowed = ?self.allowed_collections,
            "tool call refused: collection not allowed in this scenario"
        );
        Err(format!(
            "Error: collection '{collection}' is not available in this scenario. \
             Available collections: {}.",
            self.allowed_collections.join(", ")
        ))
    }

    /// The JSON-schema fragment for a tool's `collection` argument: the
    /// allowed set as an `enum`, so a well-behaved model never asks for more.
    pub fn collection_schema(&self, what: &str) -> serde_json::Value {
        serde_json::json!({
            "type": "string",
            "enum": self.allowed_collections,
            "description": format!("{what} (default: {})", self.default_collection),
            "default": self.default_collection,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::ToolPolicy;
    use crate::config::Config;

    const BASE: &str = r#"
[server]
[mddb]
default_collection = "docs"
result_max_chars = 300
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
"#;

    fn config(scenario: &str) -> Config {
        toml::from_str(&format!("{BASE}\n{scenario}")).expect("the fixture should parse")
    }

    fn policy(allowed: &[&str]) -> ToolPolicy {
        ToolPolicy {
            default_collection: allowed[0].into(),
            allowed_collections: allowed.iter().map(|c| c.to_string()).collect(),
            result_max_chars: 300,
        }
    }

    #[test]
    fn a_scenario_with_collections_allows_exactly_those() {
        let cfg = config(
            "[scenarios.support]\nname = \"s\"\nsystem_prompt = \"p\"\nallowed_collections = [\"faq\", \"docs\"]\n",
        );
        assert_eq!(
            ToolPolicy::for_scenario(&cfg, "support"),
            policy(&["faq", "docs"])
        );
    }

    #[test]
    fn a_scenario_without_collections_allows_only_the_default() {
        let cfg = config("[scenarios.plain]\nname = \"s\"\nsystem_prompt = \"p\"\n");
        assert_eq!(ToolPolicy::for_scenario(&cfg, "plain"), policy(&["docs"]));
    }

    #[test]
    fn an_unknown_scenario_allows_only_the_default() {
        let cfg = config("");
        assert_eq!(ToolPolicy::for_scenario(&cfg, "missing"), policy(&["docs"]));
    }

    #[test]
    fn no_collection_means_the_default() {
        assert_eq!(
            policy(&["docs", "faq"]).resolve("search_docs", None),
            Ok("docs")
        );
    }

    #[test]
    fn an_allowed_collection_is_passed_through() {
        assert_eq!(
            policy(&["docs", "faq"]).resolve("search_docs", Some("faq")),
            Ok("faq")
        );
    }

    #[test]
    fn any_other_collection_is_refused_with_the_allowed_set() {
        assert_eq!(
            policy(&["docs", "faq"]).resolve("get_document", Some("hr")),
            Err(
                "Error: collection 'hr' is not available in this scenario. Available collections: docs, faq."
                    .to_string()
            )
        );
    }

    #[test]
    fn the_schema_fragment_lists_the_allowed_set_as_an_enum() {
        let got = policy(&["docs", "faq"]).collection_schema("Where to look");
        assert_eq!(
            got,
            serde_json::json!({
                "type": "string",
                "enum": ["docs", "faq"],
                "description": "Where to look (default: docs)",
                "default": "docs",
            })
        );
    }
}
