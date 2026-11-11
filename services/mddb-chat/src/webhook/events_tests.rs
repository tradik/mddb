//! Tests of `events.rs`, kept beside it so neither file grows long.

use super::events::*;
use super::types::{ToolCallRecord, UsageReport, WebhookPayload};
use crate::llm::provider::TokenUsage;

fn json(payload: &WebhookPayload) -> serde_json::Value {
    serde_json::to_value(payload).expect("a payload must serialise")
}

fn record(name: &str, collection: &str) -> ToolCallRecord {
    ToolCallRecord {
        name: name.into(),
        collection: collection.into(),
    }
}

fn report<'a>(spent: TokenUsage, tools: Vec<ToolCallRecord>) -> AnswerReport<'a> {
    AnswerReport {
        session_id: "sess-1",
        scenario: "support",
        message_id: "ans-1",
        in_reply_to: "msg-1",
        text: "Use mddb export.",
        provider: "openai",
        model: "gpt-4o-mini",
        spent,
        tools,
        latency_ms: 842,
    }
}

#[test]
fn a_webhook_without_an_events_list_gets_exactly_the_three_old_events() {
    // Backward compatibility: a config written before #399 must not start
    // receiving per-message traffic because the server learned new events.
    assert_eq!(
        legacy_events(),
        vec!["session.start", "session.end", "queue.full"]
    );
}

#[test]
fn every_documented_event_is_known_and_a_typo_is_not() {
    for name in ALL {
        assert!(is_known(name), "{name} is documented but unknown");
    }
    assert!(!is_known("session_start"), "underscores are not the names");
    assert!(!is_known("answers"));
    assert!(!is_known(""));
}

#[test]
fn a_message_event_names_the_question_and_where_it_was_asked() {
    let v = json(&WebhookPayload::message(
        "sess-1",
        "support",
        "msg-1",
        "how do I export?",
    ));

    assert_eq!(v["event"], "message");
    assert_eq!(v["data"]["session_id"], "sess-1");
    assert_eq!(v["data"]["scenario"], "support");
    assert_eq!(v["data"]["message_id"], "msg-1");
    assert_eq!(v["data"]["text"], "how do I export?");
    assert_eq!(v["data"].as_object().unwrap().len(), 4);
}

#[test]
fn an_answer_event_reports_the_model_its_cost_and_its_tools() {
    let spent = TokenUsage {
        input: 120,
        output: 30,
    };
    let tools = vec![record("search_docs", "docs"), record("get_document", "api")];
    let v = json(&WebhookPayload::answer(report(spent, tools)));

    assert_eq!(v["event"], "answer");
    assert_eq!(v["data"]["session_id"], "sess-1");
    assert_eq!(v["data"]["scenario"], "support");
    assert_eq!(v["data"]["message_id"], "ans-1");
    assert_eq!(v["data"]["in_reply_to"], "msg-1");
    assert_eq!(v["data"]["text"], "Use mddb export.");
    assert_eq!(v["data"]["provider"], "openai");
    assert_eq!(v["data"]["model"], "gpt-4o-mini");
    assert_eq!(v["data"]["usage"]["input_tokens"], 120);
    assert_eq!(v["data"]["usage"]["output_tokens"], 30);
    assert_eq!(v["data"]["tools"][0]["name"], "search_docs");
    assert_eq!(v["data"]["tools"][0]["collection"], "docs");
    assert_eq!(v["data"]["tools"][1]["collection"], "api");
    assert_eq!(v["data"]["latency_ms"], 842);
}

#[test]
fn an_answer_whose_provider_reported_no_usage_says_so_with_null() {
    // A pair of zeros would be summed by a receiver as "free"; null is
    // honest about the provider having said nothing.
    let v = json(&WebhookPayload::answer(report(
        TokenUsage::default(),
        Vec::new(),
    )));

    assert!(v["data"]["usage"].is_null(), "got {}", v["data"]["usage"]);
    assert_eq!(v["data"]["tools"], serde_json::json!([]));
}

#[test]
fn usage_counts_once_either_direction_is_reported() {
    assert_eq!(
        usage_report(TokenUsage {
            input: 0,
            output: 1
        }),
        Some(UsageReport {
            input_tokens: 0,
            output_tokens: 1
        })
    );
    assert_eq!(usage_report(TokenUsage::default()), None);
}

#[test]
fn a_feedback_event_names_the_answer_it_rates() {
    let v = json(&WebhookPayload::feedback(
        "sess-1",
        "support",
        Some("ans-1"),
        "down",
    ));

    assert_eq!(v["event"], "feedback");
    assert_eq!(v["data"]["session_id"], "sess-1");
    assert_eq!(v["data"]["scenario"], "support");
    assert_eq!(v["data"]["message_id"], "ans-1");
    assert_eq!(v["data"]["rating"], "down");
}

#[test]
fn feedback_from_a_client_without_message_ids_carries_null() {
    // Old widgets send rating, question and answer only; the event still
    // fires, and the receiver can see that the id is missing rather than
    // getting an empty string that looks like a real id.
    let v = json(&WebhookPayload::feedback("sess-1", "support", None, "up"));

    assert!(v["data"]["message_id"].is_null());
    assert_eq!(v["data"]["rating"], "up");
}
