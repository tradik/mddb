use serde::Serialize;

#[derive(Debug, Clone, Serialize)]
pub struct WebhookPayload {
    pub event: String,
    pub timestamp: u64,
    pub data: WebhookData,
}

#[derive(Debug, Clone, Serialize)]
#[serde(untagged)]
pub enum WebhookData {
    Session {
        session_id: String,
        user_name: String,
        scenario: String,
    },
    Queue {
        queue_size: usize,
        active_sessions: usize,
    },
    /// A visitor's question, as sanitized (#399).
    Message {
        session_id: String,
        scenario: String,
        message_id: String,
        text: String,
    },
    /// The assistant's final text and what producing it cost (#399).
    Answer {
        session_id: String,
        scenario: String,
        message_id: String,
        /// The `message_id` of the question this answers.
        in_reply_to: String,
        text: String,
        provider: String,
        model: String,
        /// `None` when the provider reported no usage.
        usage: Option<UsageReport>,
        tools: Vec<ToolCallRecord>,
        latency_ms: u64,
    },
    /// A visitor's rating of an answer (#399).
    Feedback {
        session_id: String,
        scenario: String,
        /// `None` when the client predates message ids.
        message_id: Option<String>,
        rating: String,
    },
}

/// Token usage as the provider reported it.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct UsageReport {
    pub input_tokens: u64,
    pub output_tokens: u64,
}

/// One tool the model called while answering: its name and the collection it
/// searched, which is the one argument a host app can aggregate on.
///
/// MCP-005: an external tool carries its `server` and its own name, and no
/// collection; both fields are left out of the JSON when not set, so a
/// built-in tool's record reads exactly as before.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct ToolCallRecord {
    pub name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub collection: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub server: Option<String>,
}

impl WebhookPayload {
    pub fn session_start(session_id: &str, user_name: &str, scenario: &str) -> Self {
        Self {
            event: super::events::SESSION_START.to_string(),
            timestamp: now_unix(),
            data: WebhookData::Session {
                session_id: session_id.to_string(),
                user_name: user_name.to_string(),
                scenario: scenario.to_string(),
            },
        }
    }

    pub fn session_end(session_id: &str, user_name: &str, scenario: &str) -> Self {
        Self {
            event: super::events::SESSION_END.to_string(),
            timestamp: now_unix(),
            data: WebhookData::Session {
                session_id: session_id.to_string(),
                user_name: user_name.to_string(),
                scenario: scenario.to_string(),
            },
        }
    }

    pub fn queue_full(queue_size: usize, active_sessions: usize) -> Self {
        Self {
            event: super::events::QUEUE_FULL.to_string(),
            timestamp: now_unix(),
            data: WebhookData::Queue {
                queue_size,
                active_sessions,
            },
        }
    }
}

pub(super) fn now_unix() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs()
}

#[cfg(test)]
mod tests {
    use super::*;

    // TEST-001. Webhook payloads leave the process and land in someone else's
    // parser. The event name and the field names are the contract.

    fn json(payload: &WebhookPayload) -> serde_json::Value {
        serde_json::to_value(payload).expect("a payload must serialise")
    }

    #[test]
    fn session_events_carry_who_and_which_scenario() {
        let payload = WebhookPayload::session_start("sess-1", "Ada", "support");
        let v = json(&payload);

        assert_eq!(v["event"], "session.start");
        assert_eq!(v["data"]["session_id"], "sess-1");
        assert_eq!(v["data"]["user_name"], "Ada");
        assert_eq!(v["data"]["scenario"], "support");
    }

    #[test]
    fn start_and_end_differ_only_by_their_event_name() {
        let start = json(&WebhookPayload::session_start("sess-1", "Ada", "support"));
        let end = json(&WebhookPayload::session_end("sess-1", "Ada", "support"));

        assert_eq!(start["event"], "session.start");
        assert_eq!(end["event"], "session.end");
        assert_eq!(
            start["data"], end["data"],
            "the two events describe the session differently"
        );
    }

    #[test]
    fn a_queue_event_carries_both_numbers() {
        // "queue full" without the active count says nothing about whether the
        // server is busy or misconfigured.
        let v = json(&WebhookPayload::queue_full(7, 3));

        assert_eq!(v["event"], "queue.full");
        assert_eq!(v["data"]["queue_size"], 7);
        assert_eq!(v["data"]["active_sessions"], 3);
    }

    #[test]
    fn every_payload_is_stamped() {
        let v = json(&WebhookPayload::session_start("s", "n", "sc"));

        // Seconds since the epoch; anything below the 2020s means the clock
        // arithmetic went wrong rather than the clock being unusual.
        let stamp = v["timestamp"].as_u64().expect("timestamp must be a number");
        assert!(stamp > 1_600_000_000, "implausible timestamp: {stamp}");
    }

    // The data variants are untagged, so a receiver distinguishes them by the
    // event name and the fields present — never by a discriminator.
    #[test]
    fn the_data_object_carries_no_variant_tag() {
        let v = json(&WebhookPayload::queue_full(1, 1));
        let data = v["data"].as_object().expect("data must be an object");

        assert!(!data.contains_key("type"), "an untagged enum emitted a tag");
        assert_eq!(data.len(), 2, "unexpected fields: {data:?}");
    }
}
