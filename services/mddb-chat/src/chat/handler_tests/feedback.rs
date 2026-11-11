use serde_json::json;

use super::collect;
use crate::chat::testing::{ConfigToml, Harness};
use crate::webhook::test_support::WebhookSink;

#[tokio::test]
async fn feedback_is_forwarded_with_the_answer_it_rates() {
    let mut sink = WebhookSink::start().await;
    let server = Harness::start(ConfigToml::default().webhook(&sink.url, &["feedback"])).await;
    let mut client = server.connect().await;
    let id = server.join(&mut client, "Ada").await;

    client
        .send_json(json!({
            "type": "feedback", "rating": "down",
            "question": "q", "answer": "a", "message_id": "ans-1"
        }))
        .await;
    let down = sink.next().await;
    assert_eq!(down.event, "feedback");
    assert_eq!(down.body["data"]["session_id"], id);
    assert_eq!(down.body["data"]["scenario"], "assistant");
    assert_eq!(down.body["data"]["message_id"], "ans-1");
    assert_eq!(down.body["data"]["rating"], "down");

    // A widget built before message ids: the event fires with a null id.
    client
        .send_json(json!({"type": "feedback", "rating": "up", "question": "q", "answer": "a"}))
        .await;
    let up = sink.next().await;
    assert!(up.body["data"]["message_id"].is_null());
    assert_eq!(up.body["data"]["rating"], "up");
}

#[tokio::test]
async fn feedback_without_a_session_is_not_forwarded() {
    // Nothing to attribute it to, so nothing a host app could analyse.
    let mut sink = WebhookSink::start().await;
    let server = Harness::start(ConfigToml::default().webhook(&sink.url, &["feedback"])).await;
    let mut client = server.connect().await;

    client
        .send_json(json!({"type": "feedback", "rating": "down", "question": "q", "answer": "a"}))
        .await;
    client.send_json(json!({"type": "ping"})).await;
    assert_eq!(client.recv_json().await["type"], "pong");

    sink.assert_silent().await;
    assert!(collect(&mut sink, 0).await.is_empty());
}
