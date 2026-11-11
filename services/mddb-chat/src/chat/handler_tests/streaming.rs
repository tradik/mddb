use super::{ask, five_tool_rounds};
use crate::chat::testing::{ConfigToml, Harness};
use crate::error::AppError;
use crate::webhook::test_support::WebhookSink;

#[tokio::test]
async fn after_the_last_tool_round_the_answer_is_streamed() {
    let mut sink = WebhookSink::start().await;
    let server = Harness::start(ConfigToml::default().webhook(&sink.url, &["answer"])).await;
    five_tool_rounds(&server);
    // A provider may send a long delta; it is forwarded as one frame.
    let tail = "ed".repeat(100);
    server
        .llm
        .stream(vec![Ok("stream".to_string()), Ok(tail.clone())]);
    let mut client = server.connect().await;
    server.join(&mut client, "Ada").await;

    ask(&mut client, "go").await;
    let (text, done) = client.recv_answer().await;

    assert_eq!(text, format!("stream{tail}"));
    let answer = &sink.next().await.body["data"];
    assert_eq!(answer["message_id"], done["message_id"]);
    assert_eq!(answer["text"], text);
    assert_eq!(answer["tools"].as_array().unwrap().len(), 5);
    // The streaming call's own usage is not reported; see handler.rs.
    assert_eq!(answer["usage"]["input_tokens"], 35);
}

#[tokio::test]
async fn a_streamed_answer_is_cut_at_the_response_limit() {
    let server = Harness::start(ConfigToml::default().session("max_response_length = 5")).await;
    five_tool_rounds(&server);
    server
        .llm
        .stream(vec![Ok("abc".to_string()), Ok("defg".to_string())]);
    let mut client = server.connect().await;
    server.join(&mut client, "Ada").await;

    ask(&mut client, "go").await;
    let (text, _) = client.recv_answer().await;

    assert_eq!(text, "abc\n\n[Response truncated]");
}

#[tokio::test]
async fn a_stream_that_breaks_reports_the_error_and_still_ends_the_turn() {
    let server = Harness::start(ConfigToml::default()).await;
    five_tool_rounds(&server);
    server.llm.stream(vec![
        Ok("partial".to_string()),
        Err(AppError::LlmError("cut".into())),
    ]);
    let mut client = server.connect().await;
    server.join(&mut client, "Ada").await;

    ask(&mut client, "go").await;

    assert_eq!(client.recv_json().await["content"], "partial");
    let error = client.recv_json().await;
    assert_eq!(error["type"], "error");
    assert_eq!(error["message"], "error generating response");
    assert_eq!(client.recv_json().await["type"], "done");
}

#[tokio::test]
async fn model_failures_are_reported_as_errors() {
    let server = Harness::start(ConfigToml::default()).await;
    let mut client = server.connect().await;
    server.join(&mut client, "Ada").await;

    server.llm.fail("down");
    ask(&mut client, "hi").await;
    assert_eq!(client.recv_json().await["message"], "llm error: down");

    five_tool_rounds(&server);
    server.llm.fail_stream("boom");
    ask(&mut client, "again").await;
    assert_eq!(client.recv_json().await["message"], "llm error: boom");
}
