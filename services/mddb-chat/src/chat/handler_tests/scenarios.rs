//! #403: where a scenario comes from — mddbd first, the TOML as bootstrap and
//! fallback — and how an edit in mddbd reaches a running server.

use serde_json::json;

use super::{ask, prompt_of};
use crate::chat::scenario::sync::Sync;
use crate::chat::testing::{ConfigToml, Harness};
use crate::grpc::client::proto::{ChatScenarioProto, CollectionConfigProto};
use crate::grpc::testing::{FakeMddbd, scenario};

/// A fake mddbd holding one enabled scenario, `support`, over `kb`.
async fn mddbd_with_support() -> FakeMddbd {
    let mddbd = FakeMddbd::start().await;
    mddbd.put_scenario(scenario("support", &["kb"]), "You are Ada.");
    mddbd
}

async fn join_error(server: &Harness, scenario: &str) -> String {
    let mut client = server.connect().await;
    client
        .send_json(json!({"type": "join", "name": "Ada", "scenario": scenario}))
        .await;
    let frame = client.recv_json().await;
    assert_eq!(frame["type"], "error", "the join was not refused: {frame}");
    frame["message"].as_str().expect("a message").to_string()
}

#[tokio::test]
async fn a_scenario_stored_in_mddbd_is_served_with_the_prompt_mddbd_composed() {
    let mddbd = mddbd_with_support().await;
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    server.llm.answer("hello", 0, 0);
    let mut client = server.connect().await;

    server.join_scenario(&mut client, "Ada", "support").await;
    ask(&mut client, "hi").await;
    client.recv_answer().await;

    assert_eq!(prompt_of(&server, 0), "You are Ada.");
    assert_eq!(
        mddbd.count("GetCollectionConfig"),
        0,
        "the composed prompt already carries the collections' prompts"
    );
}

#[tokio::test]
async fn mddbd_wins_over_the_toml_scenario_of_the_same_name() {
    let mddbd = FakeMddbd::start().await;
    mddbd.put_scenario(scenario("assistant", &["kb"]), "From mddbd.");
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    server.llm.answer("hello", 0, 0);
    let mut client = server.connect().await;

    server.join(&mut client, "Ada").await;
    ask(&mut client, "hi").await;
    client.recv_answer().await;

    assert_eq!(prompt_of(&server, 0), "From mddbd.");
}

#[tokio::test]
async fn the_toml_scenario_serves_a_name_mddbd_does_not_have() {
    let mddbd = mddbd_with_support().await;
    mddbd.set_collection(
        "docs",
        CollectionConfigProto {
            response_prompt: "Answer in steps.".to_string(),
            ..Default::default()
        },
    );
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    server.llm.answer("hello", 0, 0);
    let mut client = server.connect().await;

    server.join(&mut client, "Ada").await;
    ask(&mut client, "hi").await;
    client.recv_answer().await;

    // Composed here, as before: the TOML prompt, then the collection's.
    assert_eq!(prompt_of(&server, 0), "Be brief.\n\nAnswer in steps.");
    assert_eq!(mddbd.count("GetChatScenarioPrompt"), 1, "one per reload");
}

#[tokio::test]
async fn before_mddbd_is_reached_the_toml_scenarios_can_be_joined() {
    let server = Harness::start_before_mddbd(ConfigToml::default()).await;
    let mut client = server.connect().await;

    server.join(&mut client, "Ada").await;

    assert_eq!(server.sync().await, Sync::NotConnected);
    assert_eq!(server.state.scenarios.source(), "toml");
}

#[tokio::test]
async fn a_disabled_scenario_is_refused_at_join_and_the_toml_does_not_stand_in() {
    let mddbd = FakeMddbd::start().await;
    mddbd.put_scenario(
        ChatScenarioProto {
            enabled: false,
            ..scenario("assistant", &[])
        },
        "Off.",
    );
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;

    assert_eq!(
        join_error(&server, "assistant").await,
        "scenario is disabled"
    );
    assert_eq!(
        join_error(&server, "nobody").await,
        "invalid message: unknown scenario: nobody"
    );
}

#[tokio::test]
async fn an_edit_in_mddbd_reaches_an_open_session_at_its_next_turn() {
    let mddbd = mddbd_with_support().await;
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    server.llm.answer("first", 0, 0).answer("second", 0, 0);
    let mut client = server.connect().await;
    let id = server.join_scenario(&mut client, "Ada", "support").await;
    ask(&mut client, "one").await;
    client.recv_answer().await;

    assert_eq!(server.sync().await, Sync::Unchanged);
    mddbd.put_scenario(scenario("support", &["kb"]), "You are Grace.");
    assert_eq!(server.sync().await, Sync::Reloaded(1));

    // No restart, no new join: the same socket, the same session.
    ask(&mut client, "two").await;
    client.recv_answer().await;

    assert_eq!(prompt_of(&server, 0), "You are Ada.");
    assert_eq!(prompt_of(&server, 1), "You are Grace.");
    let requests = server.llm.requests.lock().unwrap();
    assert_eq!(requests[1].len(), 4, "the conversation was kept");
    assert_eq!(server.state.session_manager.attached_count(), 1);
    assert!(server.state.session_manager.get_session(&id).is_some());
}

#[tokio::test]
async fn a_scenario_switched_off_under_an_open_session_refuses_its_next_message() {
    let mddbd = mddbd_with_support().await;
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    let mut client = server.connect().await;
    server.join_scenario(&mut client, "Ada", "support").await;

    mddbd.put_scenario(
        ChatScenarioProto {
            enabled: false,
            ..scenario("support", &["kb"])
        },
        "You are Ada.",
    );
    server.sync().await;
    ask(&mut client, "still there?").await;

    assert_eq!(client.recv_json().await["message"], "scenario is disabled");
    assert!(server.llm.requests.lock().unwrap().is_empty());
}

#[tokio::test]
async fn a_scenario_deleted_from_mddbd_falls_back_to_the_toml_one() {
    let mddbd = FakeMddbd::start().await;
    mddbd.put_scenario(scenario("assistant", &["kb"]), "From mddbd.");
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    server.llm.answer("hello", 0, 0);
    let mut client = server.connect().await;
    server.join(&mut client, "Ada").await;

    mddbd.delete_scenario("assistant");
    assert_eq!(server.sync().await, Sync::Reloaded(0));
    ask(&mut client, "hi").await;
    client.recv_answer().await;

    assert_eq!(prompt_of(&server, 0), "Be brief.");
    assert_eq!(server.state.scenarios.source(), "mddbd");
    assert_eq!(server.state.scenarios.version(), Some(2));
}

// A collection's responsePrompt is part of the composed prompt, and editing
// it does not move the scenario version.
#[tokio::test]
async fn the_composed_prompt_is_asked_for_on_every_turn() {
    let mddbd = mddbd_with_support().await;
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    server.llm.answer("first", 0, 0).answer("second", 0, 0);
    let mut client = server.connect().await;
    server.join_scenario(&mut client, "Ada", "support").await;

    mddbd.set_prompt("support", Some("You are Ada.\n\nCite the key."));
    ask(&mut client, "one").await;
    client.recv_answer().await;
    assert_eq!(prompt_of(&server, 0), "You are Ada.\n\nCite the key.");

    // The lookup fails: the prompt of the last reload serves.
    mddbd.set_prompt("support", None);
    ask(&mut client, "two").await;
    client.recv_answer().await;
    assert_eq!(prompt_of(&server, 1), "You are Ada.");
}

#[tokio::test]
async fn a_failed_reload_keeps_the_scenarios_already_loaded() {
    let mddbd = mddbd_with_support().await;
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;

    // mddbd is away.
    mddbd.fail_with(Some(tonic::Code::Unavailable));
    assert_eq!(server.sync().await, Sync::Failed);
    mddbd.fail_with(None);

    // A reload that cannot fetch one prompt is not applied in part.
    mddbd.put_scenario(scenario("sales", &[]), "You sell.");
    mddbd.set_prompt("support", None);
    assert_eq!(server.sync().await, Sync::Failed);

    let scenarios = &server.state.scenarios;
    assert_eq!(scenarios.version(), Some(1));
    assert!(scenarios.get("support").is_some());
    assert!(scenarios.get("sales").is_none());
}

#[tokio::test]
async fn an_mddbd_older_than_scenarios_leaves_the_toml_in_charge() {
    let mddbd = FakeMddbd::start().await;
    mddbd.fail_with(Some(tonic::Code::Unimplemented));
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    let mut client = server.connect().await;

    assert_eq!(server.sync().await, Sync::Failed);
    server.join(&mut client, "Ada").await;

    assert_eq!(server.state.scenarios.source(), "toml");
    assert_eq!(server.state.scenarios.version(), None);
}
