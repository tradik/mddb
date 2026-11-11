//! End-to-end tests of `handle_ws` over a real WebSocket, with the model
//! scripted and mddbd unreachable. Split by what they exercise: the session
//! lifecycle, and a message's way through the tool loop.

mod feedback;
mod lifecycle;
mod limits;
mod message;
mod resume;
mod streaming;

use std::collections::HashMap;

use serde_json::json;

use crate::chat::testing::{Harness, WsClient};
use crate::webhook::test_support::{Delivery, WebhookSink};

/// The next `n` deliveries keyed by event name. Each dispatch posts from its
/// own task, so two events fired back to back may arrive in either order.
pub async fn collect(sink: &mut WebhookSink, n: usize) -> HashMap<String, Delivery> {
    let mut seen = HashMap::new();
    for _ in 0..n {
        let delivery = sink.next().await;
        seen.insert(delivery.event.clone(), delivery);
    }
    seen
}

pub async fn ask(client: &mut WsClient, text: &str) {
    client
        .send_json(json!({"type": "message", "content": text}))
        .await;
}

pub const SEARCH: &str = r#"{"query":"export","collection":"guides"}"#;

pub fn five_tool_rounds(server: &Harness) {
    for _ in 0..5 {
        server.llm.tool_call("search_docs", SEARCH);
    }
}

/// Waits until the server has noticed a closed socket and detached `n`
/// sessions. The close is processed on the server's own task, so a test that
/// looked at once would race it.
pub async fn detached(server: &Harness, n: usize) {
    let sessions = &server.state.session_manager;
    for _ in 0..400 {
        if sessions.detached_count() == n {
            break;
        }
        tokio::time::sleep(std::time::Duration::from_millis(5)).await;
    }
    assert_eq!(sessions.detached_count(), n, "detached sessions");
}

pub async fn resume(client: &mut WsClient, session_id: &str) -> serde_json::Value {
    client
        .send_json(json!({"type": "resume", "session_id": session_id}))
        .await;
    client.recv_json().await
}
