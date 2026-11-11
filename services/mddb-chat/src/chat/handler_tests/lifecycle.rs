use serde_json::json;

use crate::chat::testing::ws_client::PONG;
use crate::chat::testing::{ConfigToml, Harness};
use crate::webhook::test_support::WebhookSink;

#[tokio::test]
async fn joining_and_ending_fire_the_session_events() {
    let mut sink = WebhookSink::start().await;
    let server =
        Harness::start(ConfigToml::default().webhook(&sink.url, &["session.start", "session.end"]))
            .await;
    let mut client = server.connect().await;

    let id = server.join(&mut client, "Ada").await;
    let start = sink.next().await;
    assert_eq!(start.event, "session.start");
    assert_eq!(start.body["data"]["session_id"], id);
    assert_eq!(start.body["data"]["user_name"], "Ada");
    assert_eq!(start.body["data"]["scenario"], "assistant");

    client.send_json(json!({"type": "end"})).await;
    assert_eq!(client.recv_json().await["type"], "ended");
    let end = sink.next().await;
    assert_eq!(end.event, "session.end");
    assert_eq!(end.body["data"]["session_id"], id);
    assert_eq!(server.state.session_manager.active_count(), 0);
}

#[tokio::test]
async fn a_bad_join_is_refused_with_a_reason() {
    let server = Harness::start(ConfigToml::default()).await;
    let mut client = server.connect().await;

    client
        .send_json(json!({"type": "join", "name": "Ada", "scenario": "nope"}))
        .await;
    let frame = client.recv_json().await;
    assert_eq!(frame["type"], "error");
    assert_eq!(frame["message"], "invalid message: unknown scenario: nope");

    client
        .send_json(json!({"type": "join", "name": "   "}))
        .await;
    assert_eq!(
        client.recv_json().await["message"],
        "invalid message: name is required"
    );

    client
        .send_json(json!({"type": "message", "content": "hi"}))
        .await;
    assert_eq!(client.recv_json().await["message"], "must join first");

    client.send_text("this is not json").await;
    let frame = client.recv_json().await;
    assert!(
        frame["message"]
            .as_str()
            .unwrap()
            .starts_with("invalid message:"),
        "got {frame}"
    );
}

#[tokio::test]
async fn pings_are_answered_and_binary_frames_are_ignored() {
    let server = Harness::start(ConfigToml::default()).await;
    let mut client = server.connect().await;

    client.send_ping().await;
    assert_eq!(client.recv_opcode().await, Some(PONG));

    // A protocol-level pong is not an answer to the next request: the
    // application-level `pong` still follows it.
    client.send_ping().await;
    client.send_binary(b"\x00\x01").await;
    client.send_json(json!({"type": "ping"})).await;
    assert_eq!(client.recv_json().await["type"], "pong");

    client.send_json(json!({"type": "end"})).await;
    assert_eq!(client.recv_json().await["type"], "ended");

    // A visitor who never joined leaves nothing behind when they close.
    client.send_close().await;
    while client.recv_opcode().await.is_some() {}
    assert_eq!(server.state.session_manager.active_count(), 0);
}

#[tokio::test]
async fn closing_the_socket_ends_the_session() {
    let mut sink = WebhookSink::start().await;
    let server = Harness::start(ConfigToml::default().webhook(&sink.url, &["session.end"])).await;
    let mut client = server.connect().await;
    let id = server.join(&mut client, "Ada").await;

    client.send_close().await;

    let end = sink.next().await;
    assert_eq!(end.body["data"]["session_id"], id);
    assert_eq!(end.body["data"]["user_name"], "Ada");
    assert_eq!(server.state.session_manager.active_count(), 0);
}

#[tokio::test]
async fn a_protocol_error_ends_the_session_too() {
    let mut sink = WebhookSink::start().await;
    let server = Harness::start(ConfigToml::default().webhook(&sink.url, &["session.end"])).await;
    let mut client = server.connect().await;
    let id = server.join(&mut client, "Ada").await;

    client.send_garbage().await;

    assert_eq!(sink.next().await.body["data"]["session_id"], id);
    assert_eq!(server.state.session_manager.active_count(), 0);
}

#[tokio::test]
async fn resume_returns_to_an_existing_session() {
    let server = Harness::start(ConfigToml::default()).await;
    let mut first = server.connect().await;
    let id = server.join(&mut first, "Ada").await;

    let mut second = server.connect().await;
    second
        .send_json(json!({"type": "resume", "session_id": id}))
        .await;
    let frame = second.recv_json().await;
    assert_eq!(frame["type"], "session", "got {frame}");
    assert_eq!(frame["id"], id);

    second
        .send_json(json!({"type": "resume", "session_id": "no-such-session"}))
        .await;
    assert_eq!(
        second.recv_json().await["message"],
        "session not found or expired"
    );
}

#[tokio::test]
async fn a_second_visitor_waits_in_the_queue_until_the_first_leaves() {
    let mut sink = WebhookSink::start().await;
    let server = Harness::start(
        ConfigToml::default()
            .session("max_concurrent = 1\nqueue_size = 1")
            .webhook(&sink.url, &["queue.full", "session.start"]),
    )
    .await;

    let mut ada = server.connect().await;
    server.join(&mut ada, "Ada").await;
    assert_eq!(sink.next().await.event, "session.start");

    let mut bob = server.connect().await;
    bob.send_json(json!({"type": "join", "name": "Bob"})).await;
    let queued = bob.recv_json().await;
    assert_eq!(queued["type"], "queued", "got {queued}");
    assert_eq!(queued["position"], 1);

    let mut cy = server.connect().await;
    cy.send_json(json!({"type": "join", "name": "Cy"})).await;
    assert_eq!(cy.recv_json().await["message"], "session queue is full");
    let full = sink.next().await;
    assert_eq!(full.event, "queue.full");
    assert_eq!(full.body["data"]["queue_size"], 1);
    assert_eq!(full.body["data"]["active_sessions"], 1);

    ada.send_json(json!({"type": "end"})).await;
    assert_eq!(ada.recv_json().await["type"], "ended");

    let admitted = bob.recv_json().await;
    assert_eq!(admitted["type"], "session", "got {admitted}");
    assert_eq!(admitted["scenario"], "assistant");
    let start = sink.next().await;
    assert_eq!(start.body["data"]["user_name"], "Bob");
    assert_eq!(start.body["data"]["session_id"], admitted["id"]);
}

#[tokio::test]
async fn the_rate_limit_applies_to_joins_and_messages_alike() {
    let server = Harness::start(ConfigToml::default().security("rate_limit_per_minute = 1")).await;
    let mut client = server.connect().await;
    server.join(&mut client, "Ada").await;

    client
        .send_json(json!({"type": "message", "content": "hi"}))
        .await;
    assert_eq!(
        client.recv_json().await["message"],
        "rate limited — please slow down"
    );

    let mut other = server.connect().await;
    other
        .send_json(json!({"type": "join", "name": "Bob"}))
        .await;
    assert_eq!(other.recv_json().await["message"], "rate limited");
}
