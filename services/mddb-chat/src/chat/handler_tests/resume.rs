//! Disconnect, detach and resume (#402).
//!
//! A session used to be removed the moment its WebSocket closed, so the
//! `resume` every reconnecting widget sends always got "session not found or
//! expired" — a dropped idle socket, a mobile network or a page reload lost
//! the conversation the model sees.

use serde_json::json;

use super::{ask, detached, resume};
use crate::chat::testing::{ConfigToml, Harness};
use crate::webhook::test_support::WebhookSink;

const TWO_SCENARIOS: &str = r#"
[scenarios.assistant]
name = "assistant"
system_prompt = "Be brief."
[scenarios.support]
name = "support"
system_prompt = "Be kind."
"#;

#[tokio::test]
async fn closing_the_socket_detaches_the_session_and_resume_brings_it_back() {
    let mut sink = WebhookSink::start().await;
    let server = Harness::start(
        ConfigToml::default()
            .scenarios(TWO_SCENARIOS)
            .webhook(&sink.url, &["session.end"]),
    )
    .await;
    server.llm.answer("Hello Ada.", 10, 5);
    server.llm.answer("Still here.", 10, 5);

    let mut first = server.connect().await;
    first
        .send_json(json!({"type": "join", "name": "Ada", "scenario": "support"}))
        .await;
    let id = first.recv_json().await["id"]
        .as_str()
        .expect("id")
        .to_string();
    ask(&mut first, "remember me").await;
    first.recv_answer().await;

    first.send_close().await;
    detached(&server, 1).await;
    assert_eq!(server.state.session_manager.attached_count(), 0);
    // The conversation may well continue: no session.end for a disconnect.
    sink.assert_silent().await;

    let mut second = server.connect().await;
    let frame = resume(&mut second, &id).await;
    assert_eq!(frame["type"], "session", "got {frame}");
    assert_eq!(frame["id"], id);
    assert_eq!(
        frame["scenario"], "support",
        "resume must say which conversation the client is back in"
    );

    // The model is shown the turn from before the reconnect.
    ask(&mut second, "do you?").await;
    second.recv_answer().await;
    let shown: Vec<String> = server
        .llm
        .requests
        .lock()
        .expect("requests lock")
        .last()
        .expect("a second model call")
        .iter()
        .filter_map(|m| m.content.clone())
        .collect();
    assert!(
        shown.iter().any(|m| m == "remember me") && shown.iter().any(|m| m == "Hello Ada."),
        "the history did not survive the reconnect: {shown:?}"
    );

    // `end` is still what ends it, and that does fire the event.
    second.send_json(json!({"type": "end"})).await;
    assert_eq!(second.recv_json().await["type"], "ended");
    assert_eq!(sink.next().await.body["data"]["session_id"], id);
    assert_eq!(server.state.session_manager.detached_count(), 0);
}

#[tokio::test]
async fn a_protocol_error_detaches_the_session_too() {
    let server = Harness::start(ConfigToml::default()).await;
    let mut client = server.connect().await;
    let id = server.join(&mut client, "Ada").await;

    client.send_garbage().await;

    detached(&server, 1).await;
    let mut again = server.connect().await;
    assert_eq!(resume(&mut again, &id).await["type"], "session");
}

#[tokio::test]
async fn resume_is_refused_for_an_unknown_or_still_attached_session() {
    let server = Harness::start(ConfigToml::default()).await;
    let mut owner = server.connect().await;
    let id = server.join(&mut owner, "Ada").await;
    let mut other = server.connect().await;

    // The widget rejoins when it reads this phrase.
    assert_eq!(
        resume(&mut other, "no-such-session").await["message"],
        "session not found or expired"
    );
    // Two sockets never share a session: the second would be someone who
    // copied the id while the owner is still talking.
    assert_eq!(
        resume(&mut other, &id).await["message"],
        "session is open in another connection"
    );
    assert_eq!(server.state.session_manager.attached_count(), 1);
}

#[tokio::test]
async fn resuming_the_session_a_socket_already_holds_is_harmless() {
    let server = Harness::start(ConfigToml::default()).await;
    let mut client = server.connect().await;
    let id = server.join(&mut client, "Ada").await;

    let frame = resume(&mut client, &id).await;
    assert_eq!(frame["type"], "session", "got {frame}");
    assert_eq!(frame["scenario"], "assistant");

    // Unless the TTL sweep took it from under the socket in the meantime.
    server.state.session_manager.remove_session(&id).await;
    assert_eq!(
        resume(&mut client, &id).await["message"],
        "session not found or expired"
    );
    client
        .send_json(json!({"type": "message", "content": "hi"}))
        .await;
    assert_eq!(client.recv_json().await["message"], "must join first");
}

#[tokio::test]
async fn joining_or_resuming_while_holding_a_session_detaches_the_old_one() {
    let server = Harness::start(ConfigToml::default()).await;
    let mut first = server.connect().await;
    let old = server.join(&mut first, "Ada").await;
    first.send_close().await;
    detached(&server, 1).await;

    // A second join on one socket: the session it replaces must not stay
    // attached to nothing, holding a seat nobody can resume.
    let mut client = server.connect().await;
    let newer = server.join(&mut client, "Ada").await;
    let third = server.join(&mut client, "Ada").await;
    assert_ne!(newer, third);
    assert_eq!(server.state.session_manager.attached_count(), 1);
    assert_eq!(server.state.session_manager.detached_count(), 2);

    assert_eq!(resume(&mut client, &old).await["id"], old);
    assert_eq!(server.state.session_manager.attached_count(), 1);
    assert_eq!(server.state.session_manager.detached_count(), 2);
    assert!(
        !server
            .state
            .session_manager
            .get_session(&third)
            .expect("kept")
            .attached
    );
}

#[tokio::test]
async fn a_disconnect_frees_the_seat_and_a_late_resume_waits_its_turn() {
    // Detached sessions cost memory, not a model: the next in line is seated
    // the moment the socket drops, not when the TTL runs out.
    let server =
        Harness::start(ConfigToml::default().session("max_concurrent = 1\nqueue_size = 1")).await;
    let mut ada = server.connect().await;
    let ada_id = server.join(&mut ada, "Ada").await;
    let mut bob = server.connect().await;
    bob.send_json(json!({"type": "join", "name": "Bob"})).await;
    assert_eq!(bob.recv_json().await["type"], "queued");

    ada.send_close().await;

    assert_eq!(bob.recv_json().await["type"], "session");
    assert_eq!(server.state.session_manager.attached_count(), 1);

    // Ada comes back while Bob has the only seat. Her session is kept.
    let mut ada = server.connect().await;
    assert_eq!(
        resume(&mut ada, &ada_id).await["message"],
        "server is at capacity; try resuming again shortly"
    );
    bob.send_json(json!({"type": "end"})).await;
    assert_eq!(bob.recv_json().await["type"], "ended");
    assert_eq!(resume(&mut ada, &ada_id).await["type"], "session");
}

#[tokio::test]
async fn resume_is_charged_to_the_rate_limit() {
    let server = Harness::start(ConfigToml::default().security("rate_limit_per_minute = 1")).await;
    let mut client = server.connect().await;

    assert_eq!(
        resume(&mut client, "x").await["message"],
        "session not found or expired"
    );
    // A reconnect storm must not be a way around the per-address budget.
    assert_eq!(resume(&mut client, "x").await["message"], "rate limited");
}
