//! get_document, and the failures every tool shares.

use super::tests::{FakeSearch, call, hit, policy};
use super::{DOCUMENT_MAX_CHARS, execute_tool};
use crate::chat::markdown::ELLIPSIS;
use crate::error::AppError;

#[tokio::test]
async fn get_document_returns_the_exact_key_when_present() {
    let mut db = FakeSearch {
        results: vec![
            hit("api-guide", "close"),
            hit("api", "# API\n\nUse `GET /v1/docs`."),
        ],
        ..Default::default()
    };
    let got = execute_tool(
        &mut db,
        &call("get_document", r#"{"key":"api"}"#),
        &policy(&["docs"], 800),
    )
    .await
    .unwrap();

    assert_eq!(db.calls, vec![("api".to_string(), "docs".to_string())]);
    assert_eq!(got, "Document: api\n\nAPI\n\nUse `GET /v1/docs`.");
}

#[tokio::test]
async fn get_document_falls_back_to_the_closest_match() {
    let mut db = FakeSearch {
        results: vec![hit("api-guide", "Guide body.")],
        ..Default::default()
    };
    let got = execute_tool(
        &mut db,
        &call("get_document", r#"{"key":"api","collection":"docs"}"#),
        &policy(&["docs"], 800),
    )
    .await
    .unwrap();

    assert_eq!(
        got,
        "Document 'api' not found. Closest match: api-guide\n\nGuide body."
    );
}

#[tokio::test]
async fn get_document_reports_a_miss() {
    let mut db = FakeSearch::default();
    let got = execute_tool(
        &mut db,
        &call("get_document", r#"{"key":"nope"}"#),
        &policy(&["docs"], 800),
    )
    .await
    .unwrap();

    assert_eq!(got, "Document 'nope' not found in collection 'docs'.");
}

#[tokio::test]
async fn a_long_document_is_cut_on_a_character_and_marked() {
    let content = "ę".repeat(DOCUMENT_MAX_CHARS + 50);
    let mut db = FakeSearch {
        results: vec![hit("big", &content)],
        ..Default::default()
    };
    let got = execute_tool(
        &mut db,
        &call("get_document", r#"{"key":"big"}"#),
        &policy(&["docs"], 800),
    )
    .await
    .unwrap();

    let expected = format!(
        "Document: big\n\n{}{ELLIPSIS}\n\n[Document truncated]",
        "ę".repeat(DOCUMENT_MAX_CHARS)
    );
    assert_eq!(got, expected);
}

#[tokio::test]
async fn malformed_arguments_are_an_llm_error() {
    let mut db = FakeSearch::default();
    let err = execute_tool(
        &mut db,
        &call("search_docs", "not json"),
        &policy(&["docs"], 800),
    )
    .await
    .unwrap_err();
    assert!(matches!(err, AppError::LlmError(m) if m.starts_with("invalid tool args")));

    let err = execute_tool(
        &mut db,
        &call("get_document", "{}"),
        &policy(&["docs"], 800),
    )
    .await
    .unwrap_err();
    assert!(matches!(err, AppError::LlmError(_)));
}

#[tokio::test]
async fn an_unknown_tool_is_named_back_to_the_model() {
    let mut db = FakeSearch::default();
    let got = execute_tool(&mut db, &call("delete_all", "{}"), &policy(&["docs"], 800))
        .await
        .unwrap();
    assert_eq!(got, "Unknown tool: delete_all");
}

#[tokio::test]
async fn a_database_failure_propagates() {
    let mut db = FakeSearch {
        fail: true,
        ..Default::default()
    };
    let err = execute_tool(
        &mut db,
        &call("search_docs", r#"{"query":"q"}"#),
        &policy(&["docs"], 800),
    )
    .await
    .unwrap_err();
    assert!(matches!(err, AppError::Internal(_)));
}
