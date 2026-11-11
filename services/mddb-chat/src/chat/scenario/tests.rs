//! The scenario lookup without a server: which source wins, and what each
//! one turns into for a turn (#403).

use std::collections::HashMap;

use serde_json::json;

use super::*;
use crate::grpc::client::proto::{ChatLlmProto, ChatRateLimitProto, ChatScenarioProto};
use crate::grpc::testing::scenario;
use crate::test_support::test_config;

fn config() -> Config {
    test_config("http://127.0.0.1:1")
}

fn stored(scenario: ChatScenarioProto) -> StoredScenario {
    StoredScenario::from_proto(scenario, "Composed.".to_string())
}

fn store_of(scenarios: Vec<ChatScenarioProto>) -> ScenarioStore {
    let store = ScenarioStore::default();
    let scenarios: HashMap<String, StoredScenario> = scenarios
        .into_iter()
        .map(|s| (s.name.clone(), stored(s)))
        .collect();
    store.replace(7, scenarios);
    store
}

fn active(config: &Config, store: &ScenarioStore, name: &str) -> ActiveScenario {
    match resolve(config, store, name) {
        Resolved::Active(active) => active,
        other => panic!("wanted an active scenario, got {other:?}"),
    }
}

#[test]
fn collection_prompt_follows_the_operator_prompt() {
    let got = compose_system_prompt("You are terse.", "Answer in numbered steps.");
    assert_eq!(got, "You are terse.\n\nAnswer in numbered steps.");
}

#[test]
fn a_collection_without_a_prompt_changes_nothing() {
    assert_eq!(
        compose_system_prompt("You are terse.", ""),
        "You are terse."
    );
    assert_eq!(
        compose_system_prompt("You are terse.", "   "),
        "You are terse."
    );
}

#[test]
fn a_collection_prompt_alone_is_used_as_is() {
    assert_eq!(
        compose_system_prompt("", "Answer in steps."),
        "Answer in steps."
    );
    assert_eq!(compose_system_prompt("", ""), "");
}

#[test]
fn surrounding_whitespace_does_not_leak_into_the_prompt() {
    let got = compose_system_prompt("  operator  ", "\n collection \n");
    assert_eq!(got, "operator\n\ncollection");
}

#[test]
fn a_new_store_is_empty_and_says_toml() {
    let store = ScenarioStore::default();

    assert_eq!(store.source(), "toml");
    assert_eq!(store.version(), None);
    assert!(store.get("support").is_none());
    assert!(store.all().is_empty());
}

#[test]
fn a_loaded_store_says_mddbd_even_when_mddbd_has_no_scenarios() {
    let store = store_of(vec![]);

    assert_eq!(store.source(), "mddbd");
    assert_eq!(store.version(), Some(7));
}

#[test]
fn a_toml_scenario_resolves_against_the_server_settings() {
    let config = config();
    let store = ScenarioStore::default();

    let support = active(&config, &store, "support");
    assert_eq!(support.system_prompt, "You are kind.");
    assert!(!support.composed);
    assert_eq!(support.allowed_collections, vec!["docs"], "the default");
    assert_eq!(support.default_collection, "docs");
    assert_eq!(support.llm, CallParams::new(0.2, 64));
    assert_eq!(support.max_turns, Some(2));
    assert_eq!((support.token_budget, support.messages_per_minute), (0, 0));

    let assistant = active(&config, &store, "assistant");
    assert_eq!(assistant.llm, CallParams::new(0.7, 64), "the [llm] default");
    assert_eq!(assistant.max_turns, None);

    assert_eq!(resolve(&config, &store, "nobody"), Resolved::Unknown);
}

#[test]
fn a_stored_scenario_wins_and_overrides_only_what_it_sets() {
    let mut config = config();
    config.security.max_tokens_per_session = 1000;
    let store = store_of(vec![
        ChatScenarioProto {
            llm: Some(ChatLlmProto {
                model: "big".to_string(),
                temperature: 0.0,
                temperature_set: true,
                max_tokens: 800,
                extra_body_json: r#"{"seed":7}"#.to_string(),
            }),
            max_turns: 12,
            token_budget_per_session: 50,
            rate_limit: Some(ChatRateLimitProto {
                messages_per_minute: 20,
            }),
            ..scenario("support", &["kb", "faq"])
        },
        scenario("plain", &[]),
        ChatScenarioProto {
            enabled: false,
            ..scenario("assistant", &[])
        },
    ]);

    let support = active(&config, &store, "support");
    assert_eq!(support.system_prompt, "Composed.");
    assert!(support.composed);
    assert_eq!(support.allowed_collections, vec!["kb", "faq"]);
    assert_eq!(support.default_collection, "kb");
    assert_eq!(support.llm.model.as_deref(), Some("big"));
    assert_eq!(
        (support.llm.temperature, support.llm.max_tokens),
        (0.0, 800)
    );
    assert_eq!(support.llm.extra_body.get("seed"), Some(&json!(7)));
    assert_eq!(support.max_turns, Some(12), "not the TOML's 2");
    assert_eq!(support.token_budget, 50);
    assert_eq!(support.messages_per_minute, 20);

    let plain = active(&config, &store, "plain");
    assert_eq!(plain.llm, CallParams::new(0.7, 64));
    assert_eq!(plain.allowed_collections, vec!["docs"]);
    assert_eq!(plain.max_turns, None);
    assert_eq!(plain.token_budget, 1000, "the [security] budget");

    // Switched off in mddbd: the TOML scenario of that name does not serve.
    assert_eq!(resolve(&config, &store, "assistant"), Resolved::Disabled);
}

#[test]
fn a_turn_limit_is_reached_after_that_many_turns() {
    let mut scenario = ActiveScenario::generic(&config());
    assert_eq!(scenario.system_prompt, "You are a helpful assistant.");
    assert!(scenario.allows_another_turn(0));
    assert!(scenario.allows_another_turn(10_000));

    // Turns already taken, so the third message is the last one allowed.
    scenario.max_turns = Some(3);
    assert!(scenario.allows_another_turn(2));
    assert!(!scenario.allows_another_turn(3));
    assert!(!scenario.allows_another_turn(99));

    scenario.max_turns = Some(0);
    assert!(!scenario.allows_another_turn(0));
}

#[test]
fn the_catalog_is_the_toml_overlaid_by_mddbd() {
    let config = config();
    let labels = |store: &ScenarioStore| -> Vec<(String, String)> {
        catalog(&config, store).into_iter().collect()
    };
    let pair = |name: &str, label: &str| (name.to_string(), label.to_string());

    assert_eq!(
        labels(&ScenarioStore::default()),
        vec![pair("assistant", "Assistant"), pair("support", "Support")]
    );

    let store = store_of(vec![
        ChatScenarioProto {
            description: "Support desk".to_string(),
            ..scenario("support", &[])
        },
        scenario("sales", &[]),
        ChatScenarioProto {
            enabled: false,
            ..scenario("assistant", &[])
        },
        ChatScenarioProto {
            enabled: false,
            ..scenario("draft", &[])
        },
    ]);
    assert_eq!(
        labels(&store),
        vec![pair("sales", "sales"), pair("support", "Support desk")]
    );
}
