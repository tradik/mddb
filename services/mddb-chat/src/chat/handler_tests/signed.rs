//! #405: a signed join. The host's token names the user; the conversation
//! lives in mddbd and comes back on the next socket, pod or restart.

use serde_json::{Value, json};

use super::{SEARCH, ask};
use crate::chat::testing::{ConfigToml, Harness, WsClient};
use crate::grpc::testing::FakeMddbd;
use crate::identity::token::sign;

pub const SECRET: &str = "host-secret-0123456789abcdef0123456789";

/// A token for `sub` in `scenario`, valid for five minutes.
pub fn token(sub: &str, scenario: &str) -> String {
    let exp = crate::identity::now() + 300;
    sign(
        SECRET.as_bytes(),
        &format!(r#"{{"sub":"{sub}","name":"Ada","scenario":"{scenario}","exp":{exp}}}"#),
    )
}

pub fn config() -> ConfigToml {
    ConfigToml::default().identity(SECRET, false).scenarios(
        r#"
[scenarios.assistant]
name = "assistant"
system_prompt = "Be brief."
allowed_collections = ["docs", "guides"]
retention_days = 7
"#,
    )
}

pub async fn server() -> (FakeMddbd, Harness) {
    let mddbd = FakeMddbd::start().await;
    let server = Harness::start_with_mddbd(config(), &mddbd).await;
    (mddbd, server)
}

/// Sends a join with `extra` fields and returns the first frame back.
pub async fn join_with(client: &mut WsClient, extra: Value) -> Value {
    let mut join = json!({"type": "join", "name": "Visitor", "scenario": "assistant"});
    join.as_object_mut()
        .unwrap()
        .extend(extra.as_object().unwrap().clone());
    client.send_json(join).await;
    client.recv_json().await
}

/// A signed join for `sub`; returns the `session` frame.
pub async fn signed_join(client: &mut WsClient, sub: &str, extra: Value) -> Value {
    let mut extra = extra;
    extra["token"] = json!(token(sub, "assistant"));
    let frame = join_with(client, extra).await;
    assert_eq!(frame["type"], "session", "the signed join failed: {frame}");
    frame
}

#[tokio::test]
async fn a_signed_conversation_is_stored_in_mddbd_turn_by_turn() {
    let (mddbd, server) = server().await;
    server.llm.tool_call("search_docs", SEARCH);
    server.llm.answer("Rotate it.", 40, 2);
    let mut client = server.connect().await;

    let session = signed_join(&mut client, "user-42", json!({})).await;
    ask(&mut client, "How do I rotate the key?").await;
    client.recv_answer().await;

    let id = session["conversation_id"]
        .as_str()
        .expect("a conversation id");
    let stored = mddbd.conversation(id).expect("stored");
    assert_eq!(
        (stored.sub.as_str(), stored.scenario.as_str()),
        ("user-42", "assistant")
    );
    assert_eq!(
        stored.display_name, "Ada",
        "the token's name, not the join's"
    );
    assert_eq!(stored.retention_days, 7, "the scenario's retention");
    assert_eq!(stored.messages.len(), 2, "{:?}", stored.messages);
    let (question, answer) = (&stored.messages[0], &stored.messages[1]);
    assert_eq!(
        (question.role.as_str(), question.content.as_str()),
        ("user", "How do I rotate the key?")
    );
    assert_eq!(
        (answer.role.as_str(), answer.content.as_str()),
        ("assistant", "Rotate it.")
    );
    assert_eq!(
        (answer.model.as_str(), answer.provider.as_str()),
        ("test-model", "scripted")
    );
    assert_eq!((answer.prompt_tokens, answer.completion_tokens), (47, 5));
    assert_eq!(answer.tool_calls.len(), 1);
    assert_eq!(
        (
            answer.tool_calls[0].name.as_str(),
            answer.tool_calls[0].collection.as_str()
        ),
        ("search_docs", "guides")
    );
}

#[tokio::test]
async fn new_conversation_starts_fresh_and_keeps_the_old_one() {
    let (mddbd, server) = server().await;
    let mut client = server.connect().await;
    let old = signed_join(&mut client, "u", json!({})).await;

    let fresh = signed_join(&mut client, "u", json!({"new_conversation": true})).await;

    assert_ne!(fresh["conversation_id"], old["conversation_id"]);
    assert_eq!(mddbd.conversations_of("u").len(), 2);
}

#[tokio::test]
async fn a_user_id_sent_by_the_browser_is_never_believed() {
    let (mddbd, server) = server().await;
    let mut client = server.connect().await;

    signed_join(&mut client, "user-42", json!({"sub": "victim"})).await;

    assert!(mddbd.conversations_of("victim").is_empty());
    assert_eq!(mddbd.conversations_of("user-42").len(), 1);
}

#[tokio::test]
async fn an_anonymous_join_never_touches_the_conversation_store() {
    let (mddbd, server) = server().await;
    server.llm.answer("hi", 0, 0);
    let mut client = server.connect().await;

    let session = join_with(&mut client, json!({})).await;
    ask(&mut client, "hello").await;
    client.recv_answer().await;

    assert_eq!(session["type"], "session");
    assert!(session.get("conversation_id").is_none(), "{session}");
    let stored_calls = mddbd
        .calls()
        .iter()
        .filter(|c| c.method.contains("Conversation") || c.method == "AppendChatMessages")
        .count();
    assert_eq!(stored_calls, 0);
}
