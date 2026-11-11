//! #403: every field of a scenario stored in mddbd is applied — the allowed
//! collections, the LLM parameters and the three limits.

use serde_json::json;

use super::{ask, collect};
use crate::chat::testing::{ConfigToml, Harness};
use crate::grpc::client::proto::{ChatLlmProto, ChatRateLimitProto, ChatScenarioProto};
use crate::grpc::testing::{FakeMddbd, scenario};
use crate::llm::provider::CallParams;
use crate::webhook::test_support::WebhookSink;

/// A server whose mddbd stores `support` as given.
async fn serving(config: ConfigToml, support: ChatScenarioProto) -> (Harness, FakeMddbd) {
    let mddbd = FakeMddbd::start().await;
    mddbd.put_scenario(support, "You are Ada.");
    let server = Harness::start_with_mddbd(config, &mddbd).await;
    (server, mddbd)
}

#[tokio::test]
async fn allowed_collections_bound_what_the_tools_may_read() {
    let (server, mddbd) = serving(ConfigToml::default(), scenario("support", &["kb", "faq"])).await;
    mddbd.set_hits(&[("returns", "Thirty days.", 0.9)]);
    server
        .llm
        .tool_call("search_docs", r#"{"query":"salaries","collection":"hr"}"#)
        .tool_call("search_docs", r#"{"query":"returns","collection":"faq"}"#)
        .tool_call("search_docs", r#"{"query":"returns"}"#)
        .answer("done", 0, 0);
    let mut client = server.connect().await;
    server.join_scenario(&mut client, "Ada", "support").await;

    ask(&mut client, "how do returns work?").await;
    client.recv_answer().await;

    // The schema offers exactly the scenario's collections...
    let tools = server.llm.tools.lock().unwrap();
    let collection = &tools[0][0].function.parameters["properties"]["collection"];
    assert_eq!(collection["enum"], json!(["kb", "faq"]));
    assert_eq!(collection["default"], "kb");

    // ...and a call outside them never reaches mddbd.
    let requests = server.llm.requests.lock().unwrap();
    let tool_results: Vec<&str> = requests[3]
        .iter()
        .filter(|m| m.role == "tool")
        .filter_map(|m| m.content.as_deref())
        .collect();
    assert_eq!(
        tool_results[0],
        "Error: collection 'hr' is not available in this scenario. Available collections: kb, faq."
    );
    assert!(tool_results[1].contains("Thirty days."));
    assert!(tool_results[2].contains("Thirty days."));
    assert_eq!(mddbd.count("HybridSearch"), 2);
}

#[tokio::test]
async fn llm_settings_override_the_llm_section_for_that_scenario() {
    let mut sink = WebhookSink::start().await;
    let support = ChatScenarioProto {
        llm: Some(ChatLlmProto {
            model: "gpt-5-mini".to_string(),
            temperature: 0.0,
            temperature_set: true,
            max_tokens: 800,
            extra_body_json: r#"{"reasoning_effort":"low"}"#.to_string(),
        }),
        ..scenario("support", &["kb"])
    };
    let (server, _mddbd) = serving(
        ConfigToml::default().webhook(&sink.url, &["answer"]),
        support,
    )
    .await;
    server.llm.answer("one", 0, 0).answer("two", 0, 0);

    let mut client = server.connect().await;
    server.join_scenario(&mut client, "Ada", "support").await;
    ask(&mut client, "hi").await;
    client.recv_answer().await;
    assert_eq!(
        collect(&mut sink, 1).await["answer"].body["data"]["model"],
        "gpt-5-mini"
    );

    // The TOML scenario next to it still runs on [llm].
    let mut other = server.connect().await;
    server.join(&mut other, "Grace").await;
    ask(&mut other, "hi").await;
    other.recv_answer().await;
    assert_eq!(
        collect(&mut sink, 1).await["answer"].body["data"]["model"],
        "test-model"
    );

    let params = server.llm.params.lock().unwrap();
    assert_eq!(
        params[0],
        CallParams {
            model: Some("gpt-5-mini".to_string()),
            temperature: 0.0,
            max_tokens: 800,
            extra_body: json!({"reasoning_effort": "low"})
                .as_object()
                .cloned()
                .unwrap(),
        }
    );
    assert_eq!(params[1], CallParams::new(0.7, 1024));
}

#[tokio::test]
async fn a_scenario_that_sets_nothing_runs_on_the_servers_own_settings() {
    // The TOML scenario of the same name caps the session at one turn; the
    // stored one replaces it whole, so that cap is gone with it.
    let toml = "[scenarios.support]\nname = \"s\"\nsystem_prompt = \"p\"\nmax_turns = 1\ntemperature = 0.1";
    let (server, _mddbd) = serving(
        ConfigToml::default().scenarios(toml),
        scenario("support", &[]),
    )
    .await;
    server.llm.answer("one", 0, 0).answer("two", 0, 0);
    let mut client = server.connect().await;
    server.join_scenario(&mut client, "Ada", "support").await;

    ask(&mut client, "first").await;
    client.recv_answer().await;
    ask(&mut client, "second").await;
    client.recv_answer().await;

    let params = server.llm.params.lock().unwrap();
    assert_eq!(params[1], CallParams::new(0.7, 1024));
    let tools = server.llm.tools.lock().unwrap();
    let collection = &tools[0][0].function.parameters["properties"]["collection"];
    assert_eq!(
        collection["enum"],
        json!(["docs"]),
        "the default collection"
    );
}

#[tokio::test]
async fn max_turns_and_the_token_budget_come_from_the_scenario() {
    let support = ChatScenarioProto {
        max_turns: 2,
        token_budget_per_session: 10,
        ..scenario("support", &["kb"])
    };
    // The scenario's budget wins over the server-wide one.
    let config = ConfigToml::default().security("max_tokens_per_session = 100000");
    let (server, mddbd) = serving(config, support.clone()).await;
    server.llm.answer("costly", 6, 6).answer("again", 1, 1);
    let mut client = server.connect().await;
    server.join_scenario(&mut client, "Ada", "support").await;

    ask(&mut client, "first").await;
    client.recv_answer().await;
    ask(&mut client, "second").await;
    assert_eq!(
        client.recv_json().await["message"],
        "this conversation has used its token budget (12 tokens)"
    );

    // With the budget lifted in mddbd the turn limit is what stops it.
    mddbd.put_scenario(
        ChatScenarioProto {
            token_budget_per_session: 0,
            ..support
        },
        "You are Ada.",
    );
    server.sync().await;
    ask(&mut client, "second").await;
    client.recv_answer().await;
    ask(&mut client, "third").await;
    assert_eq!(
        client.recv_json().await["message"],
        "this conversation is limited to 2 turns"
    );
}

#[tokio::test]
async fn the_scenario_rate_limit_is_counted_per_session() {
    let support = ChatScenarioProto {
        rate_limit: Some(ChatRateLimitProto {
            messages_per_minute: 1,
        }),
        ..scenario("support", &["kb"])
    };
    let (server, _mddbd) = serving(ConfigToml::default(), support).await;
    server.llm.answer("one", 0, 0).answer("other", 0, 0);
    let mut client = server.connect().await;
    let id = server.join_scenario(&mut client, "Ada", "support").await;

    ask(&mut client, "first").await;
    client.recv_answer().await;
    ask(&mut client, "second").await;
    assert_eq!(
        client.recv_json().await["message"],
        "this conversation is limited to 1 messages per minute"
    );
    let turns = server
        .state
        .session_manager
        .get_session(&id)
        .expect("kept")
        .user_turns;
    assert_eq!(turns, 1, "a refused message must not count as a turn");

    // Another visitor in the same scenario has a pace of their own.
    let mut other = server.connect().await;
    server.join_scenario(&mut other, "Grace", "support").await;
    ask(&mut other, "first").await;
    assert_eq!(other.recv_answer().await.0, "other");
}
