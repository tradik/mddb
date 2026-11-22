use axum::body::to_bytes;
use axum::http::StatusCode;
use axum::response::IntoResponse;
use serde_json::json;

use super::*;

#[test]
fn every_status_has_the_type_openai_clients_expect() {
    for (status, kind) in [
        (StatusCode::BAD_REQUEST, "invalid_request_error"),
        (StatusCode::NOT_FOUND, "invalid_request_error"),
        (StatusCode::UNAUTHORIZED, "authentication_error"),
        (StatusCode::FORBIDDEN, "permission_error"),
        (StatusCode::TOO_MANY_REQUESTS, "rate_limit_error"),
        (StatusCode::BAD_GATEWAY, "api_error"),
        (StatusCode::SERVICE_UNAVAILABLE, "api_error"),
    ] {
        assert_eq!(ApiError::new(status, "x").kind(), kind, "{status}");
    }
}

#[test]
fn an_app_error_keeps_its_status_and_message() {
    let error = ApiError::from(AppError::TurnLimitReached(3));
    assert_eq!(error.status, StatusCode::FORBIDDEN);
    assert_eq!(error.message, "this conversation is limited to 3 turns");
    assert_eq!(error.code, None);
}

#[tokio::test]
async fn an_error_answers_the_openai_shape() {
    let response = ApiError::model_not_found("nope").into_response();
    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    let bytes = to_bytes(response.into_body(), 4096).await.unwrap();
    let body: Value = serde_json::from_slice(&bytes).unwrap();
    assert_eq!(
        body,
        json!({"error": {
            "message": "The model 'nope' does not exist or you do not have access to it.",
            "type": "invalid_request_error",
            "code": "model_not_found",
        }})
    );
}

#[test]
fn the_request_reads_what_it_uses_and_ignores_the_rest() {
    let request: CompletionRequest = serde_json::from_value(json!({
        "model": "support",
        "messages": [{"role": "user", "content": "q"}],
        "temperature": 0.3, "top_p": 1, "tools": [], "user": "u1",
        "max_tokens": 50, "max_completion_tokens": null,
    }))
    .unwrap();
    assert!(!request.stream);
    assert!(!request.include_usage());
    assert_eq!(request.max_tokens(), Some(50));

    let newer: CompletionRequest = serde_json::from_value(json!({
        "model": "m", "messages": [], "max_tokens": 50, "max_completion_tokens": 70,
        "stream": true, "stream_options": {"include_usage": true},
    }))
    .unwrap();
    assert_eq!(newer.max_tokens(), Some(70));
    assert!(newer.include_usage());
    let unset: CompletionRequest = serde_json::from_value(json!({
        "model": "m", "messages": [], "stream_options": {},
    }))
    .unwrap();
    assert_eq!(unset.max_tokens(), None);
    assert!(!unset.include_usage());
    assert!(StreamOptions::default().include_usage.eq(&false));
}

#[test]
fn the_model_list_and_the_completion_frames() {
    let list = model_list(
        &[Model {
            id: "support".into(),
            label: "Support desk".into(),
        }],
        7,
    );
    assert_eq!(
        list,
        json!({"object": "list", "data": [{
            "id": "support", "object": "model", "created": 7,
            "owned_by": "mddb", "name": "Support desk",
        }]})
    );

    let completion = Completion {
        id: "chatcmpl-1".into(),
        created: 9,
        model: "support".into(),
    };
    let spent = TokenUsage {
        input: 3,
        output: 4,
    };
    let whole = completion.whole("hi", spent);
    assert_eq!(whole["choices"][0]["message"]["content"], "hi");
    assert_eq!(whole["usage"]["total_tokens"], 7);
    let chunk = completion.chunk(json!({"content": "h"}), None);
    assert_eq!(chunk["object"], "chat.completion.chunk");
    assert_eq!(chunk["choices"][0]["finish_reason"], Value::Null);
    let usage = completion.usage_chunk(spent);
    assert_eq!(usage["choices"], json!([]));
    assert_eq!(usage["usage"]["prompt_tokens"], 3);
}
