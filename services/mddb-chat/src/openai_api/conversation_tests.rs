use serde_json::json;

use super::text_of;
use crate::openai_api::wire::Content;

fn content(value: serde_json::Value) -> Content {
    serde_json::from_value(value).expect("content")
}

#[test]
fn a_string_is_the_text() {
    let text = text_of(Some(&content(json!("hello")))).unwrap();
    assert_eq!(text.as_deref(), Some("hello"));
}

#[test]
fn text_parts_are_joined_and_a_part_without_text_adds_nothing() {
    let parts = content(
        json!([{"type": "text", "text": "a"}, {"type": "text"}, {"type": "text", "text": "b"}]),
    );
    assert_eq!(text_of(Some(&parts)).unwrap().as_deref(), Some("a\nb"));
}

#[test]
fn no_content_or_blank_text_is_no_message() {
    assert_eq!(text_of(None).unwrap(), None);
    assert_eq!(text_of(Some(&content(json!(" \n")))).unwrap(), None);
    assert_eq!(text_of(Some(&content(json!([])))).unwrap(), None);
}

#[test]
fn any_other_part_is_refused_by_its_type() {
    let audio = content(json!([{"type": "input_audio", "input_audio": {}}]));
    let error = text_of(Some(&audio)).unwrap_err();
    assert_eq!(error.status, axum::http::StatusCode::BAD_REQUEST);
    assert!(error.message.contains("'input_audio'"), "{}", error.message);
}
