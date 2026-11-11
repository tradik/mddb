//! Tests for the Anthropic provider, against a local server that records what
//! it was sent and answers what the test tells it to.

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

/// What the fake endpoint saw — the headers and JSON body of each request —
/// and what it answers.
#[derive(Clone)]
struct Endpoint {
    seen: Arc<Mutex<Vec<(HeaderMap, Value)>>>,
    status: StatusCode,
    body: String,
}

async fn messages(
    State(endpoint): State<Endpoint>,
    headers: HeaderMap,
    axum::Json(body): axum::Json<Value>,
) -> (StatusCode, String) {
    endpoint.seen.lock().unwrap().push((headers, body));
    (endpoint.status, endpoint.body.clone())
}

/// Starts the fake endpoint and a provider pointed at it.
async fn provider_answering(status: StatusCode, body: &str) -> (AnthropicProvider, Endpoint) {
    let endpoint = Endpoint {
        seen: Arc::default(),
        status,
        body: body.to_string(),
    };
    let app = Router::new()
        .route("/v1/messages", post(messages))
        .with_state(endpoint.clone());
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move { axum::serve(listener, app).await });

    (provider_at(addr), endpoint)
}

fn provider_at(addr: SocketAddr) -> AnthropicProvider {
    let mut config = test_config("http://127.0.0.1:1").llm;
    // A trailing slash must not double up in the URL.
    config.api_url = format!("http://{addr}/v1/");
    config.api_key = "sk-test".into();
    AnthropicProvider::new(config)
}

fn msg(role: &str, content: Option<&str>) -> ApiMsg {
    ApiMsg {
        role: role.into(),
        content: content.map(str::to_string),
        tool_calls: None,
        tool_call_id: None,
    }
}

fn tool_result(id: &str, content: &str) -> ApiMsg {
    ApiMsg {
        tool_call_id: Some(id.into()),
        ..msg("tool", Some(content))
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

fn sent(endpoint: &Endpoint) -> (HeaderMap, Value) {
    endpoint
        .seen
        .lock()
        .unwrap()
        .last()
        .cloned()
        .expect("no request arrived")
}

fn llm_error<T>(result: Result<T, AppError>) -> String {
    match result {
        Err(AppError::LlmError(message)) => message,
        Err(other) => panic!("wanted an LLM error, got {other}"),
        Ok(_) => panic!("wanted an LLM error, got an answer"),
    }
}

const HELLO: &str = r#"{"content":[{"type":"text","text":"hel"},{"type":"text","text":"lo"}],
    "stop_reason":"end_turn","usage":{"input_tokens":900,"output_tokens":100}}"#;

// RAG-005: the usage block is the whole measurement path, and until now
// neither provider parsed it. These tests pin the shape each one sends,
// because a field renamed upstream would otherwise show up as a session
// that costs nothing.

#[test]
fn usage_is_read_from_the_response() {
    let body = r#"{
        "content": [{"type": "text", "text": "hello"}],
        "stop_reason": "end_turn",
        "usage": {"input_tokens": 1234, "output_tokens": 56}
    }"#;

    let parsed: MessagesResponse = serde_json::from_str(body).expect("parse");
    let usage = parsed.usage.expect("usage block");

    assert_eq!(usage.input_tokens, 1234);
    assert_eq!(usage.output_tokens, 56);
}

#[test]
fn a_response_without_usage_still_parses() {
    // A budget must not depend on a field the provider is free to omit:
    // the turn succeeds and simply contributes nothing.
    let body = r#"{"content": [{"type": "text", "text": "hi"}]}"#;

    let parsed: MessagesResponse = serde_json::from_str(body).expect("parse");
    assert!(parsed.usage.is_none());
}

#[test]
fn a_partial_usage_block_counts_what_it_reports() {
    let body = r#"{
        "content": [],
        "usage": {"output_tokens": 7}
    }"#;

    let parsed: MessagesResponse = serde_json::from_str(body).expect("parse");
    let usage = parsed.usage.expect("usage block");

    assert_eq!(usage.input_tokens, 0);
    assert_eq!(usage.output_tokens, 7);
}

#[tokio::test]
async fn a_plain_answer_comes_back_with_its_usage() {
    let (provider, endpoint) = provider_answering(StatusCode::OK, HELLO).await;

    let turn = provider
        .chat_with_tools(
            &[msg("system", Some("Be brief.")), msg("user", Some("hi"))],
            &[],
            &CallParams::new(0.5, 256),
        )
        .await
        .expect("chat");

    assert_eq!(
        turn.usage,
        TokenUsage {
            input: 900,
            output: 100
        }
    );
    assert!(matches!(turn.response, ChatResponse::Content(text) if text == "hello"));

    let (headers, body) = sent(&endpoint);
    assert_eq!(headers["x-api-key"], "sk-test");
    assert_eq!(headers["anthropic-version"], ANTHROPIC_VERSION);
    assert_eq!(body["model"], "test-model");
    assert_eq!(body["max_tokens"], 256);
    assert_eq!(body["temperature"], 0.5);
    assert_eq!(body["system"], "Be brief.");
    assert_eq!(body["messages"], json!([{"role": "user", "content": "hi"}]));
    assert!(body.get("tools").is_none(), "no tools, no tools key");
    assert!(body.get("stream").is_none(), "not a streaming request");
}

// #403: a scenario may name its own model, temperature and length.
#[tokio::test]
async fn the_call_parameters_pick_the_model_temperature_and_length() {
    let (provider, endpoint) = provider_answering(StatusCode::OK, HELLO).await;
    let params = CallParams {
        model: Some("claude-scenario".into()),
        extra_body: json!({"reasoning_effort": "low"})
            .as_object()
            .cloned()
            .unwrap(),
        ..CallParams::new(0.0, 800)
    };

    provider
        .chat_with_tools(&[msg("user", Some("hi"))], &[], &params)
        .await
        .expect("chat");
    let (_, body) = sent(&endpoint);
    assert_eq!(body["model"], "claude-scenario");
    assert_eq!(body["max_tokens"], 800);
    assert_eq!(body["temperature"], 0.0);
    assert!(
        body.get("reasoning_effort").is_none(),
        "extra_body is for OpenAI-compatible endpoints only"
    );

    let _ = provider
        .chat_stream_raw(&[msg("user", Some("hi"))], &params)
        .await;
    let (_, body) = sent(&endpoint);
    assert_eq!(body["model"], "claude-scenario");
    assert_eq!(body["max_tokens"], 800);
    assert_eq!(body["stream"], true);
}

#[tokio::test]
async fn tool_use_blocks_are_returned_as_tool_calls() {
    let answer = r#"{"content":[
        {"type":"text","text":"Let me look."},
        {"type":"tool_use","id":"toolu_1","name":"search_docs","input":{"query":"export"}}
    ]}"#;
    let (provider, endpoint) = provider_answering(StatusCode::OK, answer).await;

    let turn = provider
        .chat_with_tools(
            &[msg("user", Some("hi"))],
            &[search_tool()],
            &CallParams::new(0.5, 64),
        )
        .await
        .expect("chat");

    assert_eq!(turn.usage, TokenUsage::default(), "no usage block");
    let ChatResponse::ToolCalls { tool_calls } = turn.response else {
        panic!("wanted tool calls");
    };
    assert_eq!(tool_calls.len(), 1);
    assert_eq!(tool_calls[0].id, "toolu_1");
    assert_eq!(tool_calls[0].function.name, "search_docs");
    assert_eq!(tool_calls[0].function.arguments, r#"{"query":"export"}"#);

    let (_, body) = sent(&endpoint);
    assert_eq!(
        body["tools"],
        json!([{"name": "search_docs", "description": "search", "input_schema": {"type": "object"}}])
    );
}

#[tokio::test]
async fn a_tool_conversation_is_sent_in_anthropics_shape() {
    let (provider, endpoint) = provider_answering(StatusCode::OK, HELLO).await;
    let call = |id: &str, arguments: &str| {
        json!({"id": id, "type": "function",
               "function": {"name": "search_docs", "arguments": arguments}})
    };
    let conversation = [
        msg("system", Some("Be brief.")),
        msg("system", None),
        msg("user", Some("question")),
        ApiMsg {
            tool_calls: Some(vec![call("t1", r#"{"query":"a"}"#), call("t2", "not json")]),
            ..msg("assistant", Some("Looking."))
        },
        tool_result("t1", "first"),
        tool_result("t2", "second"),
        ApiMsg {
            tool_calls: Some(vec![json!({})]),
            ..msg("assistant", Some(""))
        },
        msg("user", None),
        tool_result("t3", "third"),
        msg("assistant", Some("An answer.")),
        msg("assistant", None),
        msg("narrator", Some("skipped")),
    ];

    provider
        .chat_with_tools(&conversation, &[], &CallParams::new(0.5, 64))
        .await
        .expect("chat");

    let (_, body) = sent(&endpoint);
    assert_eq!(
        body["system"], "Be brief.\n\n",
        "system messages are joined"
    );
    assert_eq!(
        body["messages"],
        json!([
            {"role": "user", "content": "question"},
            {"role": "assistant", "content": [
                {"type": "text", "text": "Looking."},
                {"type": "tool_use", "id": "t1", "name": "search_docs", "input": {"query": "a"}},
                {"type": "tool_use", "id": "t2", "name": "search_docs", "input": {}},
            ]},
            // Consecutive tool results share one user message.
            {"role": "user", "content": [
                {"type": "tool_result", "tool_use_id": "t1", "content": "first"},
                {"type": "tool_result", "tool_use_id": "t2", "content": "second"},
            ]},
            {"role": "assistant", "content": [
                {"type": "tool_use", "id": "", "name": "", "input": {}},
            ]},
            {"role": "user", "content": ""},
            // A plain user message is not a list to append to.
            {"role": "user", "content": [
                {"type": "tool_result", "tool_use_id": "t3", "content": "third"},
            ]},
            {"role": "assistant", "content": "An answer."},
            {"role": "assistant", "content": ""},
        ])
    );
}

#[tokio::test]
async fn a_refusal_an_unparseable_answer_and_a_dead_endpoint_are_errors() {
    let user = [msg("user", Some("hi"))];
    let params = CallParams::new(0.5, 64);

    let (provider, _) = provider_answering(StatusCode::BAD_REQUEST, "bad request body").await;
    let err = llm_error(provider.chat_with_tools(&user, &[], &params).await);
    assert_eq!(err, "Anthropic API 400 Bad Request: bad request body");
    let err = llm_error(provider.chat_stream_raw(&user, &params).await);
    assert_eq!(err, "Anthropic API 400 Bad Request: bad request body");

    let (provider, _) = provider_answering(StatusCode::OK, "not json").await;
    let err = llm_error(provider.chat_with_tools(&user, &[], &params).await);
    assert!(err.starts_with("failed to parse response"), "got {err}");

    let reserved = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let provider = provider_at(reserved.local_addr().unwrap());
    drop(reserved);
    assert!(!llm_error(provider.chat_with_tools(&user, &[], &params).await).is_empty());
    assert!(!llm_error(provider.chat_stream_raw(&user, &params).await).is_empty());
}

// Streaming.

async fn collect(provider: &AnthropicProvider) -> Vec<Result<String, String>> {
    provider
        .chat_stream_raw(&[msg("user", Some("hi"))], &CallParams::new(0.5, 64))
        .await
        .expect("the stream should start")
        .map(|chunk| chunk.map_err(|e| e.to_string()))
        .collect()
        .await
}

fn event(data: Value) -> String {
    format!(
        "event: {}\ndata: {data}\n\n",
        data["type"].as_str().unwrap()
    )
}

fn text_delta(text: &str) -> String {
    event(json!({"type": "content_block_delta", "index": 0,
                 "delta": {"type": "text_delta", "text": text}}))
}

#[tokio::test]
async fn a_stream_yields_its_text_deltas_until_the_message_stops() {
    let body = [
        event(json!({"type": "message_start", "message": {}})),
        event(json!({"type": "content_block_start", "index": 0, "content_block": {}})),
        event(json!({"type": "ping"})),
        text_delta("Hel"),
        "data: not json\n\n".to_string(),
        text_delta(""),
        event(json!({"type": "content_block_delta", "index": 1,
                     "delta": {"type": "input_json_delta", "partial_json": "{"}})),
        text_delta("lo"),
        event(json!({"type": "content_block_stop", "index": 0})),
        event(json!({"type": "message_delta", "delta": {}})),
        event(json!({"type": "message_stop"})),
        text_delta("after the end"),
    ]
    .concat();
    let (provider, endpoint) = provider_answering(StatusCode::OK, &body).await;

    let chunks = collect(&provider).await;

    assert_eq!(chunks, vec![Ok("Hel".to_string()), Ok("lo".to_string())]);
    let (_, body) = sent(&endpoint);
    assert_eq!(body["stream"], true);
    assert!(body.get("tools").is_none());
}

#[tokio::test]
async fn a_stream_reports_an_error_event_and_ends_when_the_body_does() {
    let body = [
        text_delta("one"),
        event(json!({"type": "error", "error": {"message": "overloaded"}})),
    ]
    .concat();
    let (provider, _) = provider_answering(StatusCode::OK, &body).await;
    assert_eq!(
        collect(&provider).await,
        vec![
            Ok("one".to_string()),
            Err("llm error: overloaded".to_string())
        ]
    );

    // No message_stop: the body simply stops.
    let (provider, _) = provider_answering(StatusCode::OK, &text_delta("only")).await;
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
        let chunk = text_delta("partial");
        let head = format!(
            "HTTP/1.1 200 OK\r\ncontent-type: text/event-stream\r\ncontent-length: {}\r\n\r\n",
            chunk.len() + 500
        );
        socket.write_all(head.as_bytes()).await.unwrap();
        socket.write_all(chunk.as_bytes()).await.unwrap();
        socket.shutdown().await.unwrap();
    });
    let provider = provider_at(addr);

    let chunks = collect(&provider).await;

    assert_eq!(chunks[0], Ok("partial".to_string()));
    assert!(
        chunks.get(1).is_some_and(|c| c.is_err()),
        "a truncated body ended without an error: {chunks:?}"
    );
}
