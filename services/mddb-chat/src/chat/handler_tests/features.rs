//! GO-049: mddb-chat follows mddbd's `chat` module switch. Off, joins are
//! refused, the scenarios are not reloaded, and /health says so; an mddbd
//! without the switches leaves everything as it was.

use axum::Json;
use axum::extract::State;
use serde_json::{Value, json};

use crate::chat::scenario::sync::Sync;
use crate::chat::testing::{ConfigToml, Harness};
use crate::grpc::testing::{FakeFeatures, FakeMddbd, scenario};
use crate::routes::health::health;

const CHAT_OFF: FakeFeatures = FakeFeatures::List {
    chat: false,
    chat_external_mcp: false,
};

async fn health_body(server: &Harness) -> Value {
    let (_, Json(body)) = health(State(server.state.clone())).await;
    body
}

/// The frame a join of `assistant` is answered with.
async fn join_answer(server: &Harness) -> Value {
    let mut client = server.connect().await;
    client
        .send_json(json!({"type": "join", "name": "Ada", "scenario": "assistant"}))
        .await;
    client.recv_json().await
}

#[tokio::test]
async fn an_mddbd_without_the_switches_leaves_the_chat_on() {
    let mddbd = FakeMddbd::start().await;
    mddbd.put_scenario(scenario("support", &["kb"]), "You are Ada.");
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;

    assert_eq!(mddbd.count("ListFeatures"), 1);
    assert_eq!(server.state.scenarios.version(), Some(1));
    let body = health_body(&server).await;
    assert_eq!(body["chat_enabled"], true);
    assert_eq!(body["chat_external_mcp_enabled"], false);
    assert_eq!(join_answer(&server).await["type"], "session");
}

#[tokio::test]
async fn chat_switched_off_refuses_joins_and_skips_the_scenarios_until_back_on() {
    let mddbd = FakeMddbd::start().await;
    mddbd.put_scenario(scenario("support", &["kb"]), "You are Ada.");
    mddbd.set_features(FakeFeatures::List {
        chat: true,
        chat_external_mcp: true,
    });
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    assert_eq!(
        health_body(&server).await["chat_external_mcp_enabled"],
        true
    );

    mddbd.set_features(CHAT_OFF);
    mddbd.put_scenario(scenario("support", &["kb"]), "You are Grace.");
    assert_eq!(server.sync().await, Sync::ChatDisabled);

    assert_eq!(mddbd.count("ListChatScenarios"), 1, "not asked while off");
    assert_eq!(server.state.scenarios.version(), Some(1), "the cache stays");
    assert_eq!(health_body(&server).await["chat_enabled"], false);
    let frame = join_answer(&server).await;
    assert_eq!(frame["type"], "error");
    assert_eq!(frame["message"], "chat is disabled on this server");

    mddbd.set_features(FakeFeatures::List {
        chat: true,
        chat_external_mcp: false,
    });
    assert_eq!(server.sync().await, Sync::Reloaded(1));
    assert_eq!(server.state.scenarios.version(), Some(2));
    assert_eq!(health_body(&server).await["chat_enabled"], true);
    assert_eq!(join_answer(&server).await["type"], "session");
}

// A failed read keeps the switches last read, either way round.
#[tokio::test]
async fn a_failed_read_keeps_the_last_known_switches() {
    let mddbd = FakeMddbd::start().await;
    mddbd.put_scenario(scenario("support", &["kb"]), "You are Ada.");
    mddbd.set_features(CHAT_OFF);
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    assert!(!server.state.features.chat());

    mddbd.set_features(FakeFeatures::Failing(tonic::Code::Internal));
    assert_eq!(server.sync().await, Sync::ChatDisabled);
    assert!(!server.state.features.chat(), "still off");

    mddbd.set_features(FakeFeatures::List {
        chat: true,
        chat_external_mcp: false,
    });
    assert_eq!(server.sync().await, Sync::Reloaded(1));
    mddbd.set_features(FakeFeatures::Failing(tonic::Code::Unavailable));
    assert_eq!(server.sync().await, Sync::Unchanged);
    assert!(server.state.features.chat(), "still on");
}
