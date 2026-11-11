//! #405: what comes back of a signed user's conversation — on a new socket,
//! by id, and only ever their own.

use serde_json::json;

use super::ask;
use super::signed::{config, join_with, server, signed_join, token};
use crate::chat::testing::Harness;
use crate::grpc::testing::FakeMddbd;

#[tokio::test]
async fn a_new_socket_resumes_the_users_conversation_with_its_history() {
    let (mddbd, server) = server().await;
    server.llm.answer("First answer.", 0, 0);
    server.llm.answer("Second answer.", 0, 0);

    let mut first = server.connect().await;
    let opened = signed_join(&mut first, "user-42", json!({})).await;
    ask(&mut first, "First question?").await;
    first.recv_answer().await;
    first.send_close().await;
    drop(first);

    // Another socket — as from another pod, which has nothing in memory.
    let mut second = server.connect().await;
    let resumed = signed_join(&mut second, "user-42", json!({})).await;
    assert_eq!(resumed["conversation_id"], opened["conversation_id"]);
    assert_ne!(
        resumed["id"], opened["id"],
        "a new session for a new socket"
    );

    let history = second.recv_json().await;
    assert_eq!(history["type"], "history");
    assert_eq!(
        history["messages"],
        json!([
            {"role": "user", "content": "First question?"},
            {"role": "assistant", "content": "First answer."},
        ])
    );

    ask(&mut second, "Second question?").await;
    second.recv_answer().await;

    let requests = server.llm.requests.lock().unwrap();
    let seen: Vec<(&str, &str)> = requests[1]
        .iter()
        .skip(1)
        .map(|m| (m.role.as_str(), m.content.as_deref().unwrap_or("")))
        .collect();
    assert_eq!(
        seen,
        vec![
            ("user", "First question?"),
            ("assistant", "First answer."),
            ("user", "Second question?"),
        ]
    );
    let system = requests[1][0].content.as_deref().unwrap();
    assert!(
        system.contains("untrusted"),
        "restored assistant turns must be marked as untrusted context: {system}"
    );
    let id = opened["conversation_id"].as_str().unwrap();
    assert_eq!(mddbd.conversation(id).unwrap().messages.len(), 4);
}

#[tokio::test]
async fn the_restored_history_is_the_last_max_history_length_messages() {
    let mddbd = FakeMddbd::start().await;
    let server =
        Harness::start_with_mddbd(config().session("max_history_length = 2"), &mddbd).await;
    for text in ["a1", "a2"] {
        server.llm.answer(text, 0, 0);
    }
    let mut client = server.connect().await;
    signed_join(&mut client, "u", json!({})).await;
    for text in ["q1", "q2"] {
        ask(&mut client, text).await;
        client.recv_answer().await;
    }

    let mut again = server.connect().await;
    signed_join(&mut again, "u", json!({})).await;
    let history = again.recv_json().await;

    assert_eq!(
        history["messages"],
        json!([
            {"role": "user", "content": "q2"},
            {"role": "assistant", "content": "a2"},
        ])
    );
}

#[tokio::test]
async fn a_conversation_id_reopens_that_conversation_of_the_same_user_only() {
    let (mddbd, server) = server().await;
    let mut client = server.connect().await;
    let first = signed_join(&mut client, "u", json!({})).await;
    signed_join(&mut client, "u", json!({"new_conversation": true})).await;
    let id = first["conversation_id"].clone();

    let reopened = signed_join(&mut client, "u", json!({"conversation_id": id})).await;
    assert_eq!(reopened["conversation_id"], id);

    // Another user naming it is answered as for an id that does not exist.
    let mut mallory = server.connect().await;
    let mut extra = json!({"conversation_id": id});
    extra["token"] = json!(token("mallory", "assistant"));
    let refused = join_with(&mut mallory, extra).await;
    assert_eq!(refused["message"], "conversation not found");
    assert!(mddbd.conversations_of("mallory").is_empty());
    assert_eq!(server.state.session_manager.attached_count(), 1);
}

#[tokio::test]
async fn a_conversation_of_another_scenario_is_not_reopened_here() {
    let (mddbd, server) = server().await;
    let elsewhere = mddbd
        .client()
        .await
        .create_conversation("u", "support", "", 0)
        .await
        .unwrap();
    let mut client = server.connect().await;

    let mut extra = json!({"conversation_id": elsewhere.id});
    extra["token"] = json!(token("u", "assistant"));
    let refused = join_with(&mut client, extra).await;

    assert_eq!(refused["message"], "conversation not found");
}
