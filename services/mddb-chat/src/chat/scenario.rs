//! The scenario a turn runs under, and where it comes from (#403).
//!
//! A scenario is looked up by name in two places: the scenarios mddbd stores
//! (`store.rs`, kept fresh by `sync.rs`) and the `[scenarios.*]` tables of the
//! TOML. mddbd wins; the TOML is the bootstrap and the fallback. Whichever one
//! answers, the rest of the server sees the same thing: an [`ActiveScenario`],
//! every setting already settled against the `[llm]`, `[mddb]` and
//! `[security]` defaults.

mod store;
mod stored;
pub mod sync;

#[cfg(test)]
mod tests;

use std::collections::BTreeMap;
use std::sync::Arc;

use crate::chat::mcp::McpServers;
use crate::config::{Config, Scenario};
use crate::llm::provider::CallParams;

pub use store::ScenarioStore;
pub use stored::StoredScenario;

/// The prompt of a session whose scenario no longer exists anywhere.
const GENERIC_PROMPT: &str = "You are a helpful assistant.";

/// Join the operator's instruction with the collection's (RAG-002).
///
/// Order is deliberate: the scenario prompt is the operator's policy — who the
/// assistant is and what it may say — while the collection prompt is about the
/// shape of its data. Policy first, so a collection cannot talk its way past it
/// by opening with "ignore your previous instructions"; a later line arguing
/// with an earlier one is a much weaker position than the reverse.
///
/// Used for TOML scenarios only: a scenario stored in mddbd arrives with its
/// prompt already composed by mddbd.
pub fn compose_system_prompt(operator_prompt: &str, collection_prompt: &str) -> String {
    let operator = operator_prompt.trim();
    let collection = collection_prompt.trim();

    match (operator.is_empty(), collection.is_empty()) {
        (_, true) => operator.to_string(),
        (true, false) => collection.to_string(),
        (false, false) => format!("{operator}\n\n{collection}"),
    }
}

/// One scenario as a turn applies it: a snapshot, taken when the turn starts
/// and kept until it ends, so a reload in between changes the next turn and
/// not this one.
#[derive(Debug, Clone, PartialEq)]
pub struct ActiveScenario {
    pub system_prompt: String,
    /// Whether mddbd composed the prompt. Such a prompt already carries the
    /// collections' `responsePrompt`s; a TOML one gets its collection's
    /// appended by [`compose_system_prompt`].
    pub composed: bool,
    /// Searched when the model names no collection: the first allowed one.
    pub default_collection: String,
    /// Every collection the tools may read; never empty.
    pub allowed_collections: Vec<String>,
    /// How the model is called: `[llm]` with the scenario's overrides.
    pub llm: CallParams,
    /// User messages one session may send; `None` means no limit.
    pub max_turns: Option<usize>,
    /// Tokens one session may spend; 0 means no limit.
    pub token_budget: u64,
    /// Messages one session may send per minute; 0 means no limit.
    pub messages_per_minute: u32,
    /// Days a signed user's stored conversation is kept (#405); 0 leaves it
    /// to mddbd's default. Only a TOML scenario sets it.
    pub retention_days: u32,
    /// The external MCP servers' states of the last reload and their limits
    /// (MCP-005); empty for a TOML scenario, which has none.
    pub mcp: Arc<McpServers>,
}

impl ActiveScenario {
    /// The settings every scenario starts from: the `[llm]` call, the
    /// `[security]` token budget, and `allowed` or — when that is empty —
    /// the default collection alone.
    fn over(config: &Config, system_prompt: &str, allowed: &[String]) -> Self {
        let mut allowed_collections = allowed.to_vec();
        if allowed_collections.is_empty() {
            allowed_collections.push(config.mddb.default_collection.clone());
        }
        Self {
            system_prompt: system_prompt.to_string(),
            composed: false,
            default_collection: allowed_collections[0].clone(),
            allowed_collections,
            llm: CallParams::new(config.llm.temperature, config.llm.max_tokens),
            max_turns: None,
            token_budget: config.security.max_tokens_per_session,
            messages_per_minute: 0,
            retention_days: 0,
            mcp: Arc::default(),
        }
    }

    /// What a session falls back to when its scenario is gone from both
    /// mddbd and the TOML: a generic prompt over the default collection.
    pub fn generic(config: &Config) -> Self {
        Self::over(config, GENERIC_PROMPT, &[])
    }

    fn from_toml(config: &Config, scenario: &Scenario) -> Self {
        let mut active = Self::over(
            config,
            &scenario.system_prompt,
            &scenario.allowed_collections,
        );
        if let Some(temperature) = scenario.temperature {
            active.llm.temperature = temperature;
        }
        active.max_turns = scenario.max_turns;
        active.retention_days = scenario.retention_days.unwrap_or(0);
        active
    }

    /// A scenario stored in mddbd. What it leaves unset — a 0 or an empty
    /// value there — keeps the server's own setting.
    fn from_stored(config: &Config, stored: &StoredScenario) -> Self {
        let mut active = Self::over(config, &stored.prompt, &stored.allowed_collections);
        active.composed = true;
        active.llm.model = stored.model.clone();
        active.llm.extra_body = stored.extra_body.clone();
        if let Some(temperature) = stored.temperature {
            active.llm.temperature = temperature;
        }
        if let Some(max_tokens) = stored.max_tokens {
            active.llm.max_tokens = max_tokens;
        }
        active.max_turns = stored.max_turns;
        if stored.token_budget > 0 {
            active.token_budget = stored.token_budget;
        }
        active.messages_per_minute = stored.messages_per_minute;
        active.mcp = stored.mcp.clone();
        active
    }

    /// Whether a session that has already sent `turns_taken` user messages
    /// may send another.
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

/// What a scenario name leads to.
#[derive(Debug, Clone, PartialEq)]
pub enum Resolved {
    Active(ActiveScenario),
    /// mddbd has it with `enabled: false`. A TOML scenario of the same name
    /// does not stand in: switching a scenario off must switch it off.
    Disabled,
    Unknown,
}

impl Resolved {
    /// The scenario when it may be served, `None` when disabled or unknown.
    pub fn active(self) -> Option<ActiveScenario> {
        match self {
            Resolved::Active(scenario) => Some(scenario),
            Resolved::Disabled | Resolved::Unknown => None,
        }
    }
}

/// Looks `name` up: mddbd's scenario if it has one, else the TOML's.
pub fn resolve(config: &Config, store: &ScenarioStore, name: &str) -> Resolved {
    if let Some(stored) = store.get(name) {
        return match stored.enabled {
            true => Resolved::Active(ActiveScenario::from_stored(config, &stored)),
            false => Resolved::Disabled,
        };
    }
    match config.get_scenario(name) {
        Some(scenario) => Resolved::Active(ActiveScenario::from_toml(config, scenario)),
        None => Resolved::Unknown,
    }
}

/// Every scenario a visitor may join, by name, with the label to show for it.
///
/// The TOML scenarios, overlaid by mddbd's: an enabled one adds or replaces
/// its name, a disabled one removes it.
pub fn catalog(config: &Config, store: &ScenarioStore) -> BTreeMap<String, String> {
    let mut catalog: BTreeMap<String, String> = config
        .scenarios
        .iter()
        .map(|(key, scenario)| (key.clone(), scenario.name.clone()))
        .collect();
    for (name, stored) in store.all() {
        match stored.enabled {
            true => catalog.insert(name, stored.label.clone()),
            false => catalog.remove(&name),
        };
    }
    catalog
}
