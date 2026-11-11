use serde_json::json;

use super::ask;
use crate::chat::testing::{ConfigToml, Harness};
use crate::session::manager::JoinResult;
use crate::webhook::test_support::WebhookSink;

#[tokio::test]
async fn unusable_messages_are_refused_before_the_model_sees_them() {
    let server = Harness::start(ConfigToml::default().security("max_message_length = 5")).await;
    let mut client = server.connect().await;
    let id = server.join(&mut client, "Ada").await;

    ask(&mut client, "   ").await;
    assert_eq!(
        client.recv_json().await["message"],
        "invalid message: empty message"
    );

    ask(&mut client, "too long").await;
    assert_eq!(client.recv_json().await["message"], "message too long");

    server.state.session_manager.remove_session(&id).await;
    ask(&mut client, "hi").await;
    assert_eq!(client.recv_json().await["message"], "session not found");

    assert!(server.llm.requests.lock().unwrap().is_empty());
}

#[tokio::test]
async fn a_refused_turn_fires_no_message_event() {
    let mut sink = WebhookSink::start().await;
    let server = Harness::start(
        ConfigToml::default()
            .scenarios(
                "[scenarios.assistant]\nname = \"assistant\"\nsystem_prompt = \"Brief.\"\nmax_turns = 1",
            )
            .webhook(&sink.url, &["message"]),
    )
    .await;
    server.llm.answer("one", 0, 0);
    let mut client = server.connect().await;
    server.join(&mut client, "Ada").await;

    ask(&mut client, "first").await;
    client.recv_answer().await;
    assert_eq!(sink.next().await.body["data"]["text"], "first");

    ask(&mut client, "second").await;
    assert_eq!(
        client.recv_json().await["message"],
        "this conversation is limited to 1 turns"
    );
    sink.assert_silent().await;
}

#[tokio::test]
async fn the_token_budget_refuses_once_spent() {
    let server =
        Harness::start(ConfigToml::default().security("max_tokens_per_session = 10")).await;
    server.llm.answer("costly", 6, 6);
    let mut client = server.connect().await;
    server.join(&mut client, "Ada").await;

    ask(&mut client, "first").await;
    client.recv_answer().await;

    ask(&mut client, "second").await;
    assert_eq!(
        client.recv_json().await["message"],
        "this conversation has used its token budget (12 tokens)"
    );
}

// #401: the server is up before mddbd is. A visitor is told to wait, and the
// refused turn is neither recorded nor reported.
#[tokio::test]
async fn a_message_before_mddbd_is_reachable_is_refused_politely() {
    let mut sink = crate::webhook::test_support::WebhookSink::start().await;
    let server =
        Harness::start_before_mddbd(ConfigToml::default().webhook(&sink.url, &["message"])).await;
    let mut client = server.connect().await;
    let id = server.join(&mut client, "Ada").await;

    ask(&mut client, "anyone there?").await;

    assert_eq!(
        client.recv_json().await["message"],
        "the knowledge base is not connected yet; try again shortly"
    );
    let turns = server
        .state
        .session_manager
        .get_session(&id)
        .expect("kept")
        .user_turns;
    assert_eq!(turns, 0, "a refused turn must not count");
    sink.assert_silent().await;
}

#[tokio::test]
async fn a_session_whose_scenario_is_gone_still_gets_an_answer() {
    // The config lost the scenario (an operator edit between restarts and a
    // resumed session): the handler falls back to a generic system prompt.
    let server = Harness::start(ConfigToml::default()).await;
    let joined = server
        .state
        .session_manager
        .join("Ghost".into(), "vanished".into())
        .await;
    server.llm.answer("still here", 0, 0);
    let mut client = server.connect().await;

    if let JoinResult::Admitted { session_id } = joined {
        // No socket ever held it; #402 resumes only what is detached.
        server.state.session_manager.detach(&session_id).await;
        client
            .send_json(json!({"type": "resume", "session_id": session_id}))
            .await;
        assert_eq!(client.recv_json().await["type"], "session");
        ask(&mut client, "hello?").await;
        assert_eq!(client.recv_answer().await.0, "still here");
    }

    // One request reached the model, so the branch above ran.
    let requests = server.llm.requests.lock().unwrap();
    assert_eq!(requests.len(), 1, "an empty server must admit");
    let system = requests[0][0].content.as_deref().unwrap();
    assert!(
        system.starts_with("You are a helpful assistant."),
        "got {system}"
    );
}
