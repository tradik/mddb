use serde::{Deserialize, Serialize};
use std::time::Instant;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ChatMessage {
    pub role: MessageRole,
    pub content: String,
    pub timestamp: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "lowercase")]
pub enum MessageRole {
    User,
    Assistant,
    System,
}

#[derive(Debug)]
pub struct Session {
    pub id: String,
    pub name: String,
    pub scenario: String,
    pub history: Vec<ChatMessage>,
    pub created_at: Instant,
    pub last_active: Instant,
    pub message_count: usize,
    /// User messages sent in this session, which is what a scenario's
    /// `max_turns` counts. Separate from `message_count`, which also counts
    /// the assistant's replies and so would halve any configured limit.
    pub user_turns: usize,
    pub total_tokens_used: usize,
}

impl Session {
    pub fn new(id: String, name: String, scenario: String) -> Self {
        let now = Instant::now();
        Self {
            id,
            name,
            scenario,
            history: Vec::new(),
            created_at: now,
            last_active: now,
            message_count: 0,
            user_turns: 0,
            total_tokens_used: 0,
        }
    }

    pub fn add_message(&mut self, role: MessageRole, content: String) {
        let role_for_count = role.clone();
        let timestamp = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs();

        self.history.push(ChatMessage {
            role,
            content,
            timestamp,
        });
        self.last_active = Instant::now();
        self.message_count += 1;
        if matches!(role_for_count, MessageRole::User) {
            self.user_turns += 1;
        }
    }

    pub fn trim_history(&mut self, max_length: usize) {
        if self.history.len() > max_length {
            let drain_count = self.history.len() - max_length;
            self.history.drain(..drain_count);
        }
    }
}

/// Messages sent over WebSocket
#[derive(Debug, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "lowercase")]
pub enum WsIncoming {
    Join {
        name: String,
        #[serde(default = "default_scenario")]
        scenario: String,
    },
    Message {
        content: String,
    },
    Resume {
        session_id: String,
    },
    End,
    Feedback {
        rating: String,
        question: String,
        answer: String,
    },
    Ping,
}

#[derive(Debug, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "lowercase")]
pub enum WsOutgoing {
    Session {
        id: String,
        scenario: String,
    },
    Queued {
        position: usize,
    },
    Chunk {
        content: String,
    },
    Done,
    Error {
        message: String,
    },
    Pong,
    Ended,
}

fn default_scenario() -> String {
    "assistant".to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    // TEST-001. A session's history is what is replayed to the LLM on every
    // turn, so trimming it wrong either loses the conversation or lets it grow
    // without bound.

    fn session() -> Session {
        Session::new("id".into(), "Ada".into(), "default".into())
    }

    #[test]
    fn a_new_session_has_nothing_in_it() {
        let s = session();

        assert!(s.history.is_empty());
        assert_eq!(s.message_count, 0);
        assert_eq!(s.user_turns, 0);
        assert_eq!(s.name, "Ada");
        assert_eq!(s.scenario, "default");
    }

    #[test]
    fn messages_are_appended_in_order() {
        let mut s = session();

        s.add_message(MessageRole::User, "first".into());
        s.add_message(MessageRole::Assistant, "second".into());

        let contents: Vec<&str> = s.history.iter().map(|m| m.content.as_str()).collect();
        assert_eq!(contents, vec!["first", "second"]);
        assert_eq!(s.message_count, 2);
    }

    #[test]
    fn only_user_messages_count_as_turns() {
        // max_turns caps what a visitor may send. Counting the assistant's
        // replies as well would halve every configured limit.
        let mut s = session();

        s.add_message(MessageRole::User, "q1".into());
        s.add_message(MessageRole::Assistant, "a1".into());
        s.add_message(MessageRole::User, "q2".into());
        s.add_message(MessageRole::System, "note".into());

        assert_eq!(s.message_count, 4);
        assert_eq!(s.user_turns, 2);
    }

    #[test]
    fn adding_a_message_marks_the_session_active() {
        let mut s = session();
        let before = s.last_active;

        std::thread::sleep(std::time::Duration::from_millis(5));
        s.add_message(MessageRole::User, "still here".into());

        assert!(
            s.last_active > before,
            "last_active did not move, so an active session would be reaped as idle"
        );
    }

    #[test]
    fn trimming_drops_the_oldest_messages() {
        let mut s = session();
        for i in 0..10 {
            s.add_message(MessageRole::User, format!("message {i}"));
        }

        s.trim_history(3);

        // The most recent turns are the ones worth keeping: the model needs
        // what was just said, not how the conversation opened.
        let kept: Vec<&str> = s.history.iter().map(|m| m.content.as_str()).collect();
        assert_eq!(kept, vec!["message 7", "message 8", "message 9"]);
    }

    #[test]
    fn trimming_a_short_history_changes_nothing() {
        let mut s = session();
        s.add_message(MessageRole::User, "only one".into());

        s.trim_history(50);

        assert_eq!(s.history.len(), 1);
    }

    #[test]
    fn trimming_to_the_exact_length_changes_nothing() {
        let mut s = session();
        for i in 0..5 {
            s.add_message(MessageRole::User, format!("{i}"));
        }

        s.trim_history(5);

        assert_eq!(s.history.len(), 5);
    }

    #[test]
    fn trimming_to_zero_empties_the_history() {
        let mut s = session();
        s.add_message(MessageRole::User, "gone".into());

        s.trim_history(0);

        assert!(s.history.is_empty());
    }

    // The count is a running total of the conversation, not of what is
    // currently in memory; trimming must not rewrite history's length.
    #[test]
    fn trimming_does_not_reset_the_counters() {
        let mut s = session();
        for i in 0..10 {
            s.add_message(MessageRole::User, format!("{i}"));
        }

        s.trim_history(2);

        assert_eq!(s.message_count, 10);
        assert_eq!(s.user_turns, 10);
    }

    #[test]
    fn a_message_carries_a_plausible_timestamp() {
        let mut s = session();
        s.add_message(MessageRole::User, "now".into());

        // Seconds since the epoch, so anything below the 2020s means the clock
        // arithmetic went wrong rather than the clock being unusual.
        assert!(s.history[0].timestamp > 1_600_000_000);
    }

    #[test]
    fn ws_messages_round_trip_through_json() {
        // The tag is part of the wire protocol the widget speaks; renaming a
        // variant silently breaks every client.
        let json = serde_json::to_string(&WsOutgoing::Error {
            message: "nope".into(),
        })
        .expect("serialise");

        assert!(json.contains(r#""type":"error""#), "unexpected shape: {json}");

        let incoming: WsIncoming =
            serde_json::from_str(r#"{"type":"message","content":"hello"}"#).expect("parse");
        match incoming {
            WsIncoming::Message { content } => assert_eq!(content, "hello"),
            other => panic!("parsed as {other:?}"),
        }
    }
}
