//! Webhook event names and the payload constructors for the per-message
//! events (#399).
//!
//! The three session-level events predate this module and keep their dotted
//! names; the per-message events are single words, as the issue named them.
//! A `[[webhooks]]` entry that lists no `events` receives exactly the three
//! legacy ones, so a config written before #399 behaves as it did.

use crate::llm::provider::TokenUsage;

use super::types::{ToolCallRecord, UsageReport, WebhookData, WebhookPayload, now_unix};

pub const SESSION_START: &str = "session.start";
pub const SESSION_END: &str = "session.end";
pub const QUEUE_FULL: &str = "queue.full";
pub const MESSAGE: &str = "message";
pub const ANSWER: &str = "answer";
pub const FEEDBACK: &str = "feedback";

/// Every event this server can emit, in documentation order.
pub const ALL: [&str; 6] = [
    SESSION_START,
    SESSION_END,
    QUEUE_FULL,
    MESSAGE,
    ANSWER,
    FEEDBACK,
];

/// What a webhook receives when its config names no events: the set that
/// existed before the per-message events did.
pub fn legacy_events() -> Vec<String> {
    [SESSION_START, SESSION_END, QUEUE_FULL]
        .iter()
        .map(|name| name.to_string())
        .collect()
}

/// Whether `name` is an event this server emits. A typo in a config would
/// otherwise subscribe to nothing and never say so.
pub fn is_known(name: &str) -> bool {
    ALL.contains(&name)
}

/// What the `answer` event reports as usage: nothing when the provider
/// reported nothing, rather than a pair of zeros a receiver would sum.
pub fn usage_report(spent: TokenUsage) -> Option<UsageReport> {
    (spent.total() > 0).then_some(UsageReport {
        input_tokens: spent.input,
        output_tokens: spent.output,
    })
}

/// Everything an `answer` event says about one turn.
#[derive(Debug)]
pub struct AnswerReport<'a> {
    pub session_id: &'a str,
    pub scenario: &'a str,
    /// The answer's own id, the one a later `feedback` names.
    pub message_id: &'a str,
    /// The id of the question this answers.
    pub in_reply_to: &'a str,
    pub text: &'a str,
    pub provider: &'a str,
    pub model: &'a str,
    pub spent: TokenUsage,
    pub tools: Vec<ToolCallRecord>,
    pub latency_ms: u64,
}

impl WebhookPayload {
    /// A visitor's question, after sanitising.
    pub fn message(session_id: &str, scenario: &str, message_id: &str, text: &str) -> Self {
        Self {
            event: MESSAGE.to_string(),
            timestamp: now_unix(),
            data: WebhookData::Message {
                session_id: session_id.to_string(),
                scenario: scenario.to_string(),
                message_id: message_id.to_string(),
                text: text.to_string(),
            },
        }
    }

    /// The assistant's final text and what producing it took.
    pub fn answer(report: AnswerReport<'_>) -> Self {
        Self {
            event: ANSWER.to_string(),
            timestamp: now_unix(),
            data: WebhookData::Answer {
                session_id: report.session_id.to_string(),
                scenario: report.scenario.to_string(),
                message_id: report.message_id.to_string(),
                in_reply_to: report.in_reply_to.to_string(),
                text: report.text.to_string(),
                provider: report.provider.to_string(),
                model: report.model.to_string(),
                usage: usage_report(report.spent),
                tools: report.tools,
                latency_ms: report.latency_ms,
            },
        }
    }

    /// A visitor's rating of an answer.
    pub fn feedback(
        session_id: &str,
        scenario: &str,
        message_id: Option<&str>,
        rating: &str,
    ) -> Self {
        Self {
            event: FEEDBACK.to_string(),
            timestamp: now_unix(),
            data: WebhookData::Feedback {
                session_id: session_id.to_string(),
                scenario: scenario.to_string(),
                message_id: message_id.map(str::to_string),
                rating: rating.to_string(),
            },
        }
    }
}
