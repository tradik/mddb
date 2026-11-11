//! #406: the turn log of a scenario with `logging.enabled`, anonymous
//! sessions included, and ratings stored with the answer they rate.

use serde_json::json;

use super::signed::{config, signed_join};
use super::{SEARCH, ask};
use crate::chat::testing::{ConfigToml, Harness, WsClient};
use crate::grpc::client::proto::{self, ChatLoggingProto};
use crate::grpc::testing::{FakeMddbd, scenario};

/// A fake mddbd whose `assistant` scenario logs for nine days, `redact` as
/// given (none: mddbd's default, on).
async fn logging_mddbd(enabled: bool, redact: Option<bool>) -> FakeMddbd {
    let mddbd = FakeMddbd::start().await;
    let mut logged = scenario("assistant", &["docs", "guides"]);
    logged.logging = Some(ChatLoggingProto {
        enabled,
        retention_days: 9,
        redact,
    });
    mddbd.put_scenario(logged, "Be brief.");
    mddbd.set_hits(&[
        ("export-guide", "How to export.", 0.82),
        ("cli", "The CLI.", 0.4),
    ]);
    mddbd
}

/// Waits until `f` holds: a turn is stored after its `done` frame is sent.
async fn eventually(what: &str, f: impl Fn() -> bool) {
    let waited = tokio::time::timeout(std::time::Duration::from_secs(5), async {
        while !f() {
            tokio::time::sleep(std::time::Duration::from_millis(5)).await;
        }
    });
    waited
        .await
        .unwrap_or_else(|_| panic!("never happened: {what}"));
}

/// The anonymous session's log once it holds `n` messages.
async fn anon_log(mddbd: &FakeMddbd, session: &str, n: usize) -> proto::ChatConversationProto {
    let sub = format!("anon:{session}");
    eventually("the anonymous log", || {
        mddbd
            .conversations_of(&sub)
            .first()
            .is_some_and(|c| c.messages.len() >= n)
    })
    .await;
    mddbd.conversations_of(&sub).remove(0)
}

/// Sends feedback and waits until the server has handled it.
async fn rate(client: &mut WsClient, frame: serde_json::Value) {
    client.send_json(frame).await;
    client.send_json(json!({"type": "ping"})).await;
    assert_eq!(client.recv_json().await["type"], "pong");
}

#[tokio::test]
async fn an_anonymous_turn_is_logged_with_its_tools_and_rated_by_its_id() {
    let mddbd = logging_mddbd(true, None).await;
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    server.llm.tool_call("search_docs", SEARCH);
    server.llm.answer("Use export.", 30, 4);
    let mut client = server.connect().await;
    let session = server.join(&mut client, "Visitor").await;

    ask(&mut client, "How do I export?").await;
    let (_, done) = client.recv_answer().await;
    let answer_id = done["message_id"].as_str().expect("an answer id");

    let log = anon_log(&mddbd, &session, 2).await;
    assert_eq!(log.scenario, "assistant");
    assert_eq!(log.retention_days, 9, "the scenario's log retention");
    assert!(
        log.redact,
        "redaction is on unless the scenario turns it off"
    );
    assert_eq!(log.display_name, "", "a visitor's name is no identity");
    let (question, answer) = (&log.messages[0], &log.messages[1]);
    assert_eq!(
        (question.role.as_str(), question.content.as_str()),
        ("user", "How do I export?")
    );
    assert_eq!(answer.id, answer_id, "stored under the id the client saw");
    assert_eq!(answer.outcome, "answered");
    // Every round of the turn is counted: the tool round and the answer.
    assert_eq!((answer.prompt_tokens, answer.completion_tokens), (37, 7));
    let tool = &answer.tool_calls[0];
    assert_eq!(
        (
            tool.name.as_str(),
            tool.collection.as_str(),
            tool.query.as_str()
        ),
        ("search_docs", "guides", "export")
    );
    assert_eq!(tool.result_keys, vec!["export-guide", "cli"]);
    assert!((tool.top_score - 0.82).abs() < 1e-6, "{}", tool.top_score);

    rate(
        &mut client,
        json!({"type": "feedback", "rating": "down", "question": "q", "answer": "a",
               "message_id": answer_id, "comment": format!("  Wrong.{}  ", "x".repeat(2100))}),
    )
    .await;
    let rated = anon_log(&mddbd, &session, 2).await.messages[1]
        .feedback
        .clone()
        .expect("a rating");
    assert_eq!(rated.rating, "down");
    assert_eq!(rated.comment.chars().count(), 2000, "cut to mddbd's limit");
    assert!(rated.comment.starts_with("Wrong."));

    // The second turn goes to the same log; a search that finds nothing is
    // a `noResults` turn.
    mddbd.set_hits(&[]);
    server.llm.tool_call("search_docs", SEARCH);
    server.llm.answer("I could not find that.", 1, 1);
    ask(&mut client, "And to PDF?").await;
    client.recv_answer().await;
    let log = anon_log(&mddbd, &session, 4).await;
    assert_eq!(mddbd.conversations_of(&format!("anon:{session}")).len(), 1);
    assert_eq!(log.messages[3].outcome, "noResults");
    assert!(log.messages[3].tool_calls[0].result_keys.is_empty());
}

#[tokio::test]
async fn a_failed_turn_is_logged_as_an_error() {
    let mddbd = logging_mddbd(true, Some(false)).await;
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    server.llm.fail("model down");
    let mut client = server.connect().await;
    let session = server.join(&mut client, "Visitor").await;

    ask(&mut client, "hello?").await;
    assert_eq!(client.recv_json().await["type"], "error");

    let log = anon_log(&mddbd, &session, 2).await;
    assert!(!log.redact, "the scenario turned redaction off");
    assert_eq!(log.messages[1].outcome, "error");
    assert!(log.messages[1].content.contains("model down"));
}

#[tokio::test]
async fn without_logging_an_anonymous_session_stores_nothing() {
    let mddbd = logging_mddbd(false, None).await;
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    server.llm.answer("hi", 0, 0);
    server.llm.fail("model down");
    let mut client = server.connect().await;
    let session = server.join(&mut client, "Visitor").await;

    ask(&mut client, "hello").await;
    let (_, done) = client.recv_answer().await;
    ask(&mut client, "again").await;
    assert_eq!(client.recv_json().await["type"], "error");
    rate(
        &mut client,
        json!({"type": "feedback", "rating": "up", "question": "q", "answer": "a", "message_id": done["message_id"]}),
    )
    .await;

    assert!(
        mddbd
            .conversations_of(&format!("anon:{session}"))
            .is_empty()
    );
    assert_eq!(mddbd.count("CreateChatConversation"), 0);
    assert_eq!(
        mddbd.count("RateChatMessage"),
        0,
        "nowhere to store a rating"
    );
}

#[tokio::test]
async fn a_signed_users_logged_turn_carries_its_details_in_their_conversation() {
    let mddbd = logging_mddbd(true, None).await;
    let server = Harness::start_with_mddbd(config(), &mddbd).await;
    server.llm.tool_call("search_docs", SEARCH);
    server.llm.answer("Use export.", 3, 1);
    let mut client = server.connect().await;
    let session = signed_join(&mut client, "user-42", json!({})).await;
    let id = session["conversation_id"]
        .as_str()
        .expect("an id")
        .to_string();

    ask(&mut client, "How do I export?").await;
    let (_, done) = client.recv_answer().await;
    eventually("the answer", || {
        mddbd
            .conversation(&id)
            .is_some_and(|c| c.messages.len() == 2)
    })
    .await;
    let stored = mddbd.conversation(&id).expect("stored");
    assert!(
        stored.redact,
        "the question was appended asking for redaction"
    );
    assert_eq!(stored.messages[1].id, done["message_id"].as_str().unwrap());
    assert_eq!(stored.messages[1].outcome, "answered");
    assert_eq!(stored.messages[1].tool_calls[0].query, "export");
    assert!(
        mddbd
            .conversations_of(&format!("anon:{}", session["id"].as_str().unwrap()))
            .is_empty()
    );

    rate(
        &mut client,
        json!({"type": "feedback", "rating": "up", "question": "q", "answer": "a", "message_id": done["message_id"]}),
    )
    .await;
    let rated = mddbd.conversation(&id).unwrap().messages[1]
        .feedback
        .clone();
    assert_eq!(rated.map(|f| f.rating).as_deref(), Some("up"));

    // A rating of an id the store does not hold, or without an id, is only logged.
    rate(&mut client, json!({"type": "feedback", "rating": "up", "question": "q", "answer": "a", "message_id": "nope"})).await;
    rate(&mut client, json!({"type": "feedback", "rating": "up", "question": "q", "answer": "a", "message_id": ""})).await;
    assert_eq!(mddbd.count("RateChatMessage"), 2);
}

#[tokio::test]
async fn a_signed_answer_without_logging_is_stored_plain_under_its_id() {
    let (mddbd, server) = super::signed::server().await;
    server.llm.tool_call("search_docs", SEARCH);
    server.llm.answer("Rotate it.", 1, 1);
    let mut client = server.connect().await;
    let session = signed_join(&mut client, "user-7", json!({})).await;
    let id = session["conversation_id"].as_str().unwrap().to_string();

    ask(&mut client, "rotate?").await;
    let (_, done) = client.recv_answer().await;
    eventually("the answer", || {
        mddbd
            .conversation(&id)
            .is_some_and(|c| c.messages.len() == 2)
    })
    .await;
    let stored = mddbd.conversation(&id).unwrap();
    assert!(!stored.redact);
    assert_eq!(stored.messages[1].id, done["message_id"].as_str().unwrap());
    assert_eq!(stored.messages[1].outcome, "", "not a logged turn");
    assert_eq!(
        stored.messages[1].tool_calls[0].query, "",
        "no details without logging"
    );

    // mddbd gone: a rating is dropped with a warning.
    mddbd.fail_with(Some(tonic::Code::Unavailable));
    rate(&mut client, json!({"type": "feedback", "rating": "down", "question": "q", "answer": "a", "message_id": done["message_id"]})).await;
}

#[tokio::test]
async fn a_log_that_cannot_be_created_leaves_the_turn_unstored() {
    let mddbd = logging_mddbd(true, None).await;
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    server.llm.answer("hi", 0, 0);
    let mut client = server.connect().await;
    let session = server.join(&mut client, "Visitor").await;
    mddbd.fail_with(Some(tonic::Code::Unavailable));

    ask(&mut client, "hello").await;
    client.recv_answer().await;
    rate(&mut client, json!({"type": "ping"})).await;
    assert!(
        mddbd
            .conversations_of(&format!("anon:{session}"))
            .is_empty()
    );
}

#[tokio::test]
async fn a_rating_without_mddbd_is_dropped() {
    let server = Harness::start_before_mddbd(ConfigToml::default()).await;
    let mut client = server.connect().await;
    let session = server.join(&mut client, "Visitor").await;
    if let Some(mut held) = server.state.session_manager.get_session_mut(&session) {
        held.log = Some(crate::session::conversation::ConversationRef {
            sub: format!("anon:{session}"),
            id: "c1".into(),
        });
    }
    rate(
        &mut client,
        json!({"type": "feedback", "rating": "up", "question": "q", "answer": "a", "message_id": "m1"}),
    )
    .await;
}
