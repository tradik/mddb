use serde_json::json;

use super::{SEARCH, ask, collect};
use crate::chat::testing::{ConfigToml, Harness};
use crate::session::types::MessageRole;
use crate::webhook::test_support::WebhookSink;

#[tokio::test]
async fn an_answer_streams_in_chunks_and_ends_with_a_message_id() {
    let server = Harness::start(ConfigToml::default()).await;
    let long = "x".repeat(120);
    server.llm.answer(&long, 10, 5);
    let mut client = server.connect().await;
    let id = server.join(&mut client, "Ada").await;

    ask(&mut client, &"hello ".repeat(40)).await;
    let (text, done) = client.recv_answer().await;

    assert_eq!(text, long);
    assert_eq!(done["message_id"].as_str().unwrap().len(), 36);
    {
        let mut session = server.state.session_manager.get_session_mut(&id).unwrap();
        assert_eq!(session.history.len(), 2);
        assert_eq!(session.total_tokens_used, 15);
        session.add_message(
            MessageRole::System,
            "The visitor is on the pricing page.".into(),
        );
    }

    // The second turn replays the whole history, each entry under its role,
    // and its answer gets an id of its own.
    server.llm.answer("again", 0, 0);
    ask(&mut client, "and now?").await;
    let (_, second) = client.recv_answer().await;

    assert_ne!(second["message_id"], done["message_id"]);
    let requests = server.llm.requests.lock().unwrap();
    let roles: Vec<&str> = requests[1].iter().map(|m| m.role.as_str()).collect();
    assert_eq!(roles, vec!["system", "user", "assistant", "system", "user"]);
}

#[tokio::test]
async fn message_and_answer_events_share_their_ids_with_the_client() {
    let mut sink = WebhookSink::start().await;
    let server =
        Harness::start(ConfigToml::default().webhook(&sink.url, &["message", "answer"])).await;
    server.llm.answer("Fine.", 3, 2);
    let mut client = server.connect().await;
    let id = server.join(&mut client, "Ada").await;

    ask(&mut client, "<b>hi</b> there").await;
    let (text, done) = client.recv_answer().await;
    assert_eq!(text, "Fine.");

    let events = collect(&mut sink, 2).await;
    let message = &events["message"].body["data"];
    assert_eq!(message["session_id"], id);
    assert_eq!(message["scenario"], "assistant");
    assert_eq!(message["text"], "hi there", "the text is the sanitized one");
    assert_eq!(message["message_id"].as_str().unwrap().len(), 36);

    let answer = &events["answer"].body["data"];
    assert_eq!(answer["session_id"], id);
    assert_eq!(answer["in_reply_to"], message["message_id"]);
    assert_eq!(answer["message_id"], done["message_id"]);
    assert_eq!(answer["text"], "Fine.");
    assert_eq!(answer["provider"], "scripted");
    assert_eq!(answer["model"], "test-model");
    assert_eq!(answer["usage"]["input_tokens"], 3);
    assert_eq!(answer["usage"]["output_tokens"], 2);
    assert_eq!(answer["tools"], json!([]));
    assert!(answer["latency_ms"].is_u64(), "got {answer}");
}

#[tokio::test]
async fn tool_calls_are_reported_with_the_collection_they_searched() {
    let mut sink = WebhookSink::start().await;
    let server = Harness::start(ConfigToml::default().webhook(&sink.url, &["answer"])).await;
    server
        .llm
        .tool_call("search_docs", SEARCH)
        .tool_call("get_document", r#"{"key":"api"}"#)
        .tool_call("frobnicate", "{}")
        .answer("Done.", 1, 1);
    let mut client = server.connect().await;
    server.join(&mut client, "Ada").await;

    ask(&mut client, "how do I export?").await;
    let (text, _) = client.recv_answer().await;
    assert_eq!(text, "Done.");

    let answer = &sink.next().await.body["data"];
    assert_eq!(
        answer["tools"],
        json!([
            {"name": "search_docs", "collection": "guides"},
            {"name": "get_document", "collection": "docs"},
            {"name": "frobnicate", "collection": "docs"},
        ])
    );
    // Three tool rounds at 7+3 each, then the answer at 1+1.
    assert_eq!(answer["usage"]["input_tokens"], 22);
    assert_eq!(answer["usage"]["output_tokens"], 10);

    // mddbd is unreachable, so the tool results the model saw were errors;
    // an unknown tool is reported as such rather than failing the turn.
    let requests = server.llm.requests.lock().unwrap();
    assert_eq!(requests.len(), 4);
    let tool_results: Vec<&str> = requests[3]
        .iter()
        .filter(|m| m.role == "tool")
        .map(|m| m.content.as_deref().unwrap())
        .collect();
    assert!(
        tool_results[0].starts_with("Tool error:"),
        "{tool_results:?}"
    );
    assert!(
        tool_results[1].starts_with("Tool error:"),
        "{tool_results:?}"
    );
    assert_eq!(tool_results[2], "Unknown tool: frobnicate");
}
