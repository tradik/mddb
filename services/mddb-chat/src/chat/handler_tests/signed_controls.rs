//! #405: what a signed-in user controls — listing and deleting their
//! conversations, ending one.

use serde_json::json;

use super::signed::{join_with, server, signed_join, token};
use super::{ask, resume};

#[tokio::test]
async fn an_ended_conversation_is_not_resumed_and_cannot_be_reopened() {
    let (mddbd, server) = server().await;
    let mut client = server.connect().await;
    let first = signed_join(&mut client, "u", json!({})).await;
    let id = first["conversation_id"].as_str().unwrap().to_string();

    client.send_json(json!({"type": "end"})).await;
    assert_eq!(client.recv_json().await["type"], "ended");
    assert!(mddbd.conversation(&id).unwrap().ended);

    let next = signed_join(&mut client, "u", json!({})).await;
    assert_ne!(next["conversation_id"], json!(id));

    let mut extra = json!({"conversation_id": id});
    extra["token"] = json!(token("u", "assistant"));
    let refused = join_with(&mut client, extra).await;
    assert_eq!(
        refused["message"],
        "conversation has ended; start a new one"
    );
}

#[tokio::test]
async fn a_user_lists_their_conversations_of_this_scenario() {
    let (_mddbd, server) = server().await;
    server.llm.answer("ok", 0, 0);
    let mut client = server.connect().await;
    signed_join(&mut client, "u", json!({})).await;
    ask(&mut client, "Where is the export button?").await;
    client.recv_answer().await;
    let fresh = signed_join(&mut client, "u", json!({"new_conversation": true})).await;

    client
        .send_json(json!({"type": "list_conversations"}))
        .await;
    let listed = client.recv_json().await;

    assert_eq!(listed["type"], "conversations");
    let conversations = listed["conversations"].as_array().unwrap();
    assert_eq!(conversations.len(), 2);
    assert_eq!(conversations[0]["id"], fresh["conversation_id"]);
    assert_eq!(conversations[1]["message_count"], 2);
    assert_eq!(conversations[1]["ended"], false);
}

#[tokio::test]
async fn deleting_needs_a_confirmation_and_leaves_a_fresh_conversation() {
    let (mddbd, server) = server().await;
    let mut other = server.connect().await;
    signed_join(&mut other, "someone-else", json!({})).await;
    let mut client = server.connect().await;
    let before = signed_join(&mut client, "u", json!({})).await;

    client
        .send_json(json!({"type": "delete_conversations"}))
        .await;
    let refused = client.recv_json().await;
    assert_eq!(
        refused["message"],
        "invalid message: delete_conversations needs \"confirm\": true"
    );

    client
        .send_json(json!({"type": "delete_conversations", "confirm": true}))
        .await;
    let deleted = client.recv_json().await;

    assert_eq!(deleted["type"], "conversations_deleted");
    assert_eq!(deleted["deleted"], 1);
    assert_ne!(deleted["conversation_id"], before["conversation_id"]);
    assert_eq!(mddbd.conversations_of("u").len(), 1, "the fresh one");
    assert_eq!(mddbd.conversations_of("someone-else").len(), 1);
}

#[tokio::test]
async fn the_controls_need_a_signed_session() {
    let (_mddbd, server) = server().await;
    let mut client = server.connect().await;

    for request in [
        json!({"type": "list_conversations"}),
        json!({"type": "delete_conversations", "confirm": true}),
    ] {
        client.send_json(request.clone()).await;
        let frame = client.recv_json().await;
        assert_eq!(
            frame["message"], "unauthorized: needs a signed join",
            "{request}"
        );
    }

    join_with(&mut client, json!({})).await;
    client
        .send_json(json!({"type": "list_conversations"}))
        .await;
    assert_eq!(
        client.recv_json().await["message"],
        "unauthorized: needs a signed join"
    );
}

#[tokio::test]
async fn a_store_failure_is_reported_by_the_controls_but_not_by_a_turn() {
    let (mddbd, server) = server().await;
    server.llm.answer("still answered", 0, 0);
    let mut client = server.connect().await;
    signed_join(&mut client, "u", json!({})).await;
    mddbd.fail_with(Some(tonic::Code::Unavailable));

    ask(&mut client, "hello").await;
    let (text, _) = client.recv_answer().await;
    assert_eq!(text, "still answered");

    client
        .send_json(json!({"type": "list_conversations"}))
        .await;
    assert!(
        client.recv_json().await["message"]
            .as_str()
            .unwrap()
            .starts_with("grpc error")
    );
    client
        .send_json(json!({"type": "delete_conversations", "confirm": true}))
        .await;
    assert!(
        client.recv_json().await["message"]
            .as_str()
            .unwrap()
            .starts_with("grpc error")
    );
    // Ending still ends the session when the store cannot be told.
    client.send_json(json!({"type": "end"})).await;
    assert_eq!(client.recv_json().await["type"], "ended");
}

#[tokio::test]
async fn resuming_a_signed_session_by_id_names_its_conversation() {
    let (_mddbd, server) = server().await;
    let mut first = server.connect().await;
    let session = signed_join(&mut first, "u", json!({})).await;
    first.send_close().await;
    drop(first);
    super::detached(&server, 1).await;

    let mut second = server.connect().await;
    let resumed = resume(&mut second, session["id"].as_str().unwrap()).await;

    assert_eq!(resumed["type"], "session");
    assert_eq!(resumed["conversation_id"], session["conversation_id"]);
}
