//! Tests for the OpenAI-compatible provider, against a local server that
//! records what it was sent and answers what the test tells it to.

use std::net::SocketAddr;
use std::sync::{Arc, Mutex};

use axum::Router;
use axum::extract::State;
use axum::http::{HeaderMap, StatusCode};
use axum::routing::post;
use futures::StreamExt;
use serde_json::{Value, json};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

use super::*;
use crate::llm::provider::ToolFunction;
use crate::test_support::test_config;

/// One recorded request: its Authorization header and its JSON body.
type Seen = (Option<String>, Value);

/// What the fake endpoint saw and what it answers.
#[derive(Clone)]
struct Endpoint {
    seen: Arc<Mutex<Vec<Seen>>>,
    status: StatusCode,
    body: String,
}

async fn completions(
    State(endpoint): State<Endpoint>,
    headers: HeaderMap,
    axum::Json(body): axum::Json<Value>,
) -> (StatusCode, String) {
    let auth = headers
        .get("authorization")
        .map(|v| v.to_str().unwrap_or_default().to_string());
    endpoint.seen.lock().unwrap().push((auth, body));
    (endpoint.status, endpoint.body.clone())
}

/// Starts the fake endpoint and a provider pointed at it.
async fn provider_answering(
    status: StatusCode,
    body: &str,
    edit: impl FnOnce(&mut LlmConfig),
) -> (OpenAiProvider, Endpoint) {
    let endpoint = Endpoint {
        seen: Arc::default(),
        status,
        body: body.to_string(),
    };
    let app = Router::new()
        .route("/v1/chat/completions", post(completions))
        .with_state(endpoint.clone());
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move { axum::serve(listener, app).await });

    (provider_at(addr, edit), endpoint)
}

fn provider_at(addr: SocketAddr, edit: impl FnOnce(&mut LlmConfig)) -> OpenAiProvider {
    let mut config = test_config("http://127.0.0.1:1").llm;
    // A trailing slash must not double up in the URL.
    config.api_url = format!("http://{addr}/v1/");
    edit(&mut config);
    OpenAiProvider::new(config)
}

fn user(content: &str) -> ApiMsg {
    ApiMsg {
        role: "user".into(),
        content: Some(content.into()),
        tool_calls: None,
        tool_call_id: None,
    }
}

fn search_tool() -> ToolDef {
    ToolDef {
        tool_type: "function".into(),
        function: ToolFunction {
            name: "search_docs".into(),
            description: "search".into(),
            parameters: json!({"type": "object"}),
        },
    }
}

fn sent(endpoint: &Endpoint) -> Seen {
    endpoint
        .seen
        .lock()
        .unwrap()
        .last()
        .cloned()
        .expect("no request arrived")
}

fn content(turn: ChatTurn) -> String {
    match turn.response {
        ChatResponse::Content(text) => text,
        other => panic!("wanted content, got {other:?}"),
    }
}

const HELLO: &str = r#"{"choices":[{"message":{"content":"hello"}}],
    "usage":{"prompt_tokens":900,"completion_tokens":100}}"#;

// RAG-005. OpenAI names the same two numbers differently from Anthropic,
// which is exactly the kind of difference that goes unnoticed until a
// budget silently stops counting on one provider.

#[test]
fn usage_is_read_from_the_response() {
    let parsed: ChatCompletion = serde_json::from_str(HELLO).expect("parse");
    let usage = parsed.usage.expect("usage block");

    assert_eq!(usage.prompt_tokens, 900);
    assert_eq!(usage.completion_tokens, 100);
}

#[test]
fn a_partial_usage_block_counts_what_it_reports() {
    let body = r#"{"choices": [], "usage": {"prompt_tokens": 42}}"#;

    let parsed: ChatCompletion = serde_json::from_str(body).expect("parse");
    let usage = parsed.usage.expect("usage block");

    assert_eq!(usage.prompt_tokens, 42);
    assert_eq!(usage.completion_tokens, 0);
}

#[tokio::test]
async fn a_plain_answer_comes_back_with_its_usage() {
    let (provider, endpoint) = provider_answering(StatusCode::OK, HELLO, |_| {}).await;

    let turn = provider
        .chat_with_tools(&[user("hi")], &[], &CallParams::new(0.5, 256))
        .await
        .expect("chat");

    assert_eq!(
        turn.usage,
        TokenUsage {
            input: 900,
            output: 100
        }
    );
    assert_eq!(content(turn), "hello");

    let (auth, body) = sent(&endpoint);
    assert_eq!(
        auth, None,
        "an empty api_key must send no Authorization header"
    );
    assert_eq!(body["model"], "test-model");
    assert_eq!(body["max_tokens"], 256);
    assert_eq!(body["temperature"], 0.5);
    assert_eq!(body["stream"], false);
    assert_eq!(body["messages"], json!([{"role": "user", "content": "hi"}]));
    assert!(body.get("tools").is_none(), "no tools, no tools key");
}

#[tokio::test]
async fn the_api_key_tools_and_tool_messages_are_sent() {
    let (provider, endpoint) =
        provider_answering(StatusCode::OK, HELLO, |c| c.api_key = "sk-test".into()).await;
    let messages = [
        ApiMsg {
            role: "assistant".into(),
            content: None,
            tool_calls: Some(vec![json!({"id": "call-1"})]),
            tool_call_id: None,
        },
        ApiMsg {
            role: "tool".into(),
            content: Some("result".into()),
            tool_calls: None,
            tool_call_id: Some("call-1".into()),
        },
    ];

    provider
        .chat_with_tools(&messages, &[search_tool()], &CallParams::new(0.0, 16))
        .await
        .expect("chat");

    let (auth, body) = sent(&endpoint);
    assert_eq!(auth.as_deref(), Some("Bearer sk-test"));
    assert_eq!(body["tools"][0]["function"]["name"], "search_docs");
    assert_eq!(body["messages"][0]["tool_calls"][0]["id"], "call-1");
    assert!(body["messages"][0].get("content").is_none());
    assert_eq!(body["messages"][1]["tool_call_id"], "call-1");
}

#[tokio::test]
async fn tool_calls_are_returned_as_tool_calls() {
    let body = r#"{"choices":[{"message":{"content":null,"tool_calls":[
        {"id":"call-1","type":"function","function":{"name":"search_docs","arguments":"{}"}}]}}]}"#;
    let (provider, _) = provider_answering(StatusCode::OK, body, |_| {}).await;

    let turn = provider
        .chat_with_tools(&[user("hi")], &[search_tool()], &CallParams::new(0.5, 64))
        .await
        .expect("chat");

    // No usage block: the call counts as zero rather than failing.
    assert_eq!(turn.usage, TokenUsage::default());
    assert!(
        matches!(&turn.response, ChatResponse::ToolCalls { tool_calls }
            if tool_calls.len() == 1 && tool_calls[0].function.name == "search_docs"),
        "got {:?}",
        turn.response
    );
}

#[tokio::test]
async fn an_empty_tool_call_list_is_an_answer_not_a_tool_round() {
    let body = r#"{"choices":[{"message":{"content":"done","tool_calls":[]}}]}"#;
    let (provider, _) = provider_answering(StatusCode::OK, body, |_| {}).await;

    let turn = provider
        .chat_with_tools(&[user("hi")], &[], &CallParams::new(0.5, 64))
        .await;

    assert_eq!(content(turn.expect("chat")), "done");
}

// #400. There was no way to send a field the provider did not model, so a
// thinking model behind Ollama could not be told to stop thinking.

#[tokio::test]
async fn extra_body_is_merged_into_every_request() {
    let (provider, endpoint) = provider_answering(StatusCode::OK, HELLO, |c| {
        c.extra_body = json!({
            "reasoning_effort": "none",
            "options": {"num_ctx": 8192},
            "seed": 7,
        })
        .as_object()
        .cloned()
        .unwrap();
    })
    .await;

    provider
        .chat_with_tools(&[user("hi")], &[], &CallParams::new(0.5, 64))
        .await
        .expect("chat");
    let (_, body) = sent(&endpoint);
    assert_eq!(body["reasoning_effort"], "none");
    assert_eq!(body["options"]["num_ctx"], 8192);
    assert_eq!(body["seed"], 7);

    // The streaming request carries it too.
    let _ = provider
        .chat_stream_raw(&[user("hi")], &CallParams::new(0.5, 64))
        .await;
    let (_, body) = sent(&endpoint);
    assert_eq!(body["stream"], true);
    assert_eq!(body["reasoning_effort"], "none");
}

// #403: a scenario stored in mddbd may name its own model and extra body.
#[tokio::test]
async fn a_scenarios_model_and_extra_body_are_laid_over_the_configured_ones() {
    let (provider, endpoint) = provider_answering(StatusCode::OK, HELLO, |c| {
        c.extra_body = json!({"reasoning_effort": "none", "seed": 7})
            .as_object()
            .cloned()
            .unwrap();
    })
    .await;
    let params = CallParams {
        model: Some("scenario-model".into()),
        extra_body: json!({"reasoning_effort": "low", "top_k": 40, "model": "smuggled"})
            .as_object()
            .cloned()
            .unwrap(),
        ..CallParams::new(0.0, 800)
    };

    provider
        .chat_with_tools(&[user("hi")], &[], &params)
        .await
        .expect("chat");

    let (_, body) = sent(&endpoint);
    assert_eq!(body["model"], "scenario-model");
    assert_eq!(body["temperature"], 0.0);
    assert_eq!(body["max_tokens"], 800);
    assert_eq!(body["reasoning_effort"], "low", "the scenario's key wins");
    assert_eq!(body["seed"], 7, "a key only [llm] sets is kept");
    assert_eq!(body["top_k"], 40);

    // The streaming request is built the same way.
    let _ = provider.chat_stream_raw(&[user("hi")], &params).await;
    let (_, body) = sent(&endpoint);
    assert_eq!(body["stream"], true);
    assert_eq!(body["model"], "scenario-model");
    assert_eq!(body["top_k"], 40);
}

#[tokio::test]
async fn request_fields_win_over_colliding_extra_body_keys() {
    // extra_body adds; it must not be a way to silently swap the model or
    // drop the conversation.
    let (provider, endpoint) = provider_answering(StatusCode::OK, HELLO, |c| {
        c.extra_body = json!({
            "model": "something-else",
            "messages": [],
            "stream": true,
            "max_tokens": 1,
            "temperature": 2.0,
            "tools": "nonsense",
            "reasoning_effort": "low",
        })
        .as_object()
        .cloned()
        .unwrap();
    })
    .await;

    provider
        .chat_with_tools(&[user("hi")], &[search_tool()], &CallParams::new(0.5, 64))
        .await
        .expect("chat");

    let (_, body) = sent(&endpoint);
    assert_eq!(body["model"], "test-model");
    assert_eq!(body["messages"][0]["content"], "hi");
    assert_eq!(body["stream"], false);
    assert_eq!(body["max_tokens"], 64);
    assert_eq!(body["temperature"], 0.5);
    assert_eq!(body["tools"][0]["type"], "function");
    assert_eq!(
        body["reasoning_effort"], "low",
        "the non-colliding key was lost"
    );
}

#[tokio::test]
async fn reasoning_without_content_is_never_shown_to_the_visitor() {
    // What a thinking model returns once the reasoning used up max_tokens.
    for field in ["reasoning", "reasoning_content"] {
        let body = format!(
            r#"{{"choices":[{{"message":{{"content":"","{field}":"The user asks... secret chain of thought"}}}}]}}"#
        );
        let (provider, _) = provider_answering(StatusCode::OK, &body, |_| {}).await;

        let turn = provider
            .chat_with_tools(&[user("hi")], &[], &CallParams::new(0.5, 64))
            .await;

        assert_eq!(
            content(turn.expect("chat")),
            "",
            "{field} leaked into the answer"
        );
    }
}

#[tokio::test]
async fn reasoning_next_to_content_changes_nothing() {
    let body = r#"{"choices":[{"message":{"content":"42","reasoning":"long thoughts"}}]}"#;
    let (provider, _) = provider_answering(StatusCode::OK, body, |_| {}).await;

    let turn = provider
        .chat_with_tools(&[user("hi")], &[], &CallParams::new(0.5, 64))
        .await;

    assert_eq!(content(turn.expect("chat")), "42");
}

#[test]
fn a_long_reasoning_is_shortened_for_the_log_on_a_character_boundary() {
    assert_eq!(preview("short"), "short");

    let long = "ż".repeat(REASONING_PREVIEW_CHARS + 50);
    let shortened = preview(&long);
    assert_eq!(shortened.chars().count(), REASONING_PREVIEW_CHARS + 1);
    assert!(shortened.ends_with('…'));
}

// Failure modes.

fn llm_error(result: Result<ChatTurn, AppError>) -> String {
    match result {
        Err(AppError::LlmError(msg)) => msg,
        other => panic!("wanted an LLM error, got {other:?}"),
    }
}

#[tokio::test]
async fn a_refusal_from_the_endpoint_carries_its_status_and_body() {
    let (provider, _) =
        provider_answering(StatusCode::TOO_MANY_REQUESTS, "slow down", |_| {}).await;

    let err = llm_error(
        provider
            .chat_with_tools(&[user("hi")], &[], &CallParams::new(0.5, 64))
            .await,
    );
    assert!(err.contains("429") && err.contains("slow down"), "{err}");

    let stream_err = provider
        .chat_stream_raw(&[user("hi")], &CallParams::new(0.5, 64))
        .await
        .err();
    assert!(
        matches!(&stream_err, Some(AppError::LlmError(m)) if m.contains("429")),
        "got {stream_err:?}"
    );
}

#[tokio::test]
async fn an_unparseable_or_empty_answer_is_an_error() {
    let (provider, _) = provider_answering(StatusCode::OK, "<html>", |_| {}).await;
    let err = llm_error(
        provider
            .chat_with_tools(&[user("hi")], &[], &CallParams::new(0.5, 64))
            .await,
    );
    assert!(err.starts_with("failed to parse response"), "{err}");

    let (provider, _) = provider_answering(StatusCode::OK, r#"{"choices":[]}"#, |_| {}).await;
    let err = llm_error(
        provider
            .chat_with_tools(&[user("hi")], &[], &CallParams::new(0.5, 64))
            .await,
    );
    assert_eq!(err, "no choices in response");
}

#[tokio::test]
async fn an_unreachable_endpoint_is_an_error() {
    let reserved = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let addr = reserved.local_addr().unwrap();
    drop(reserved);
    let provider = provider_at(addr, |_| {});

    let result = provider
        .chat_with_tools(&[user("hi")], &[], &CallParams::new(0.5, 64))
        .await;

    assert!(!llm_error(result).is_empty());
}

// Streaming.

async fn collect(provider: &OpenAiProvider) -> Vec<Result<String, String>> {
    provider
        .chat_stream_raw(&[user("hi")], &CallParams::new(0.5, 64))
        .await
        .expect("the stream should start")
        .map(|chunk| chunk.map_err(|e| e.to_string()))
        .collect()
        .await
}

fn delta(content: &str) -> String {
    format!("data: {{\"choices\":[{{\"delta\":{{\"content\":\"{content}\"}}}}]}}\n\n")
}

#[tokio::test]
async fn a_stream_yields_its_text_deltas_until_done() {
    let body = [
        ": keep-alive comment\n\n".to_string(),
        delta("Hel"),
        "data: not json\n\n".to_string(),
        "data: {\"choices\":[]}\n\n".to_string(),
        "data: {\"choices\":[{\"delta\":{\"content\":\"\"}}]}\n\n".to_string(),
        "data: {\"choices\":[{\"delta\":{\"reasoning\":\"hidden\"}}]}\n\n".to_string(),
        delta("lo"),
        "data: [DONE]\n\n".to_string(),
        delta("after the end"),
    ]
    .concat();
    let (provider, _) = provider_answering(StatusCode::OK, &body, |_| {}).await;

    let chunks = collect(&provider).await;

    assert_eq!(chunks, vec![Ok("Hel".to_string()), Ok("lo".to_string())]);
}

#[tokio::test]
async fn a_stream_ends_at_a_finish_reason_or_when_the_body_does() {
    let body = [
        delta("one"),
        "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n".to_string(),
        delta("after the end"),
    ]
    .concat();
    let (provider, _) = provider_answering(StatusCode::OK, &body, |_| {}).await;
    assert_eq!(collect(&provider).await, vec![Ok("one".to_string())]);

    // No [DONE], no finish_reason: the body simply stops.
    let (provider, _) = provider_answering(StatusCode::OK, &delta("only"), |_| {}).await;
    assert_eq!(collect(&provider).await, vec![Ok("only".to_string())]);
}

#[tokio::test]
async fn a_stream_cut_off_mid_body_reports_an_error() {
    // A raw socket: promise more bytes than are sent, then hang up.
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.unwrap();
        let mut request = [0u8; 4096];
        let _ = socket.read(&mut request).await;
        let chunk = delta("partial");
        let head = format!(
            "HTTP/1.1 200 OK\r\ncontent-type: text/event-stream\r\ncontent-length: {}\r\n\r\n",
            chunk.len() + 500
        );
        socket.write_all(head.as_bytes()).await.unwrap();
        socket.write_all(chunk.as_bytes()).await.unwrap();
        socket.shutdown().await.unwrap();
    });
    let provider = provider_at(addr, |_| {});

    let chunks = collect(&provider).await;

    assert_eq!(chunks[0], Ok("partial".to_string()));
    assert!(
        chunks.get(1).is_some_and(|c| c.is_err()),
        "a truncated body ended without an error: {chunks:?}"
    );
}
