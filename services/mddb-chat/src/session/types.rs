use crate::llm::provider::TokenUsage;
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
    pub name: String,
    pub scenario: String,
    pub history: Vec<ChatMessage>,
    pub last_active: Instant,
    pub message_count: usize,
    /// User messages sent in this session, which is what a scenario's
    /// `max_turns` counts. Separate from `message_count`, which also counts
    /// the assistant's replies and so would halve any configured limit.
    pub user_turns: usize,
    /// Tokens this session has spent (RAG-005).
    ///
    /// This field existed before and was never written or read, which made it
    /// a cost counter that did not count — worse than none, because it implies
    /// a control that is not there. It is now charged from the `usage` block
    /// every provider response carries, summed across every tool-calling round
    /// rather than only the round that produced the answer.
    pub total_tokens_used: u64,
}

impl Session {
    /// Builds a session.
    ///
    /// RUST-001: the id is not stored. The manager keys its map by it, and
    /// holding a second copy inside the value was the same two-sources-of-truth
    /// shape as a scenario's name — with nothing reading the copy, it could
    /// only ever disagree.
    pub fn new(name: String, scenario: String) -> Self {
        let now = Instant::now();
        Self {
            name,
            scenario,
            history: Vec::new(),
            last_active: now,
            message_count: 0,
            user_turns: 0,
            total_tokens_used: 0,
        }
    }

    /// Whether this session may spend more, given a budget (RAG-005).
    ///
    /// A budget of 0 means unlimited, which is the behaviour before this
    /// existed. Mirrors `Scenario::allows_another_turn` deliberately: the two
    /// limits answer the same question about different resources, and reading
    /// them side by side in the handler should not require holding two shapes
    /// in mind.
    pub fn within_token_budget(&self, budget: u64) -> bool {
        budget == 0 || self.total_tokens_used < budget
    }

    /// Charges a turn's token usage to this session.
    pub fn add_tokens(&mut self, usage: TokenUsage) {
        // Saturating rather than wrapping: a session that overflowed would
        // report almost nothing spent, which is the one wrong answer a budget
        // must not give.
        self.total_tokens_used = self.total_tokens_used.saturating_add(usage.total());
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
    Session { id: String, scenario: String },
    Queued { position: usize },
    Chunk { content: String },
    Done,
    Error { message: String },
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
        Session::new("Ada".into(), "default".into())
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

        assert!(
            json.contains(r#""type":"error""#),
            "unexpected shape: {json}"
        );

        let incoming: WsIncoming =
            serde_json::from_str(r#"{"type":"message","content":"hello"}"#).expect("parse");
        match incoming {
            WsIncoming::Message { content } => assert_eq!(content, "hello"),
            other => panic!("parsed as {other:?}"),
        }
    }

    // RAG-005: the counter that used to exist and never counted.

    #[test]
    fn a_new_session_has_spent_nothing() {
        assert_eq!(
            Session::new("Ada".into(), "default".into()).total_tokens_used,
            0
        );
    }

    #[test]
    fn tokens_accumulate_across_turns() {
        let mut session = Session::new("Ada".into(), "default".into());

        session.add_tokens(TokenUsage {
            input: 1200,
            output: 300,
        });
        assert_eq!(session.total_tokens_used, 1500);

        // A second turn adds to the first rather than replacing it — the whole
        // point of a session budget.
        session.add_tokens(TokenUsage {
            input: 800,
            output: 200,
        });
        assert_eq!(session.total_tokens_used, 2500);
    }

    #[test]
    fn a_turn_that_reported_nothing_costs_nothing() {
        // A provider that omits its usage block must not be able to break the
        // count; it simply contributes zero.
        let mut session = Session::new("Ada".into(), "default".into());
        session.add_tokens(TokenUsage::default());
        assert_eq!(session.total_tokens_used, 0);
    }

    #[test]
    fn the_counter_saturates_rather_than_wrapping() {
        // Wrapping would report a session that had spent almost nothing, which
        // is the one wrong answer a budget must not give.
        let mut session = Session::new("Ada".into(), "default".into());
        session.add_tokens(TokenUsage {
            input: u64::MAX,
            output: 0,
        });
        session.add_tokens(TokenUsage {
            input: 1000,
            output: 1000,
        });
        assert_eq!(session.total_tokens_used, u64::MAX);
    }

    #[test]
    fn an_unset_budget_never_refuses() {
        // 0 means unlimited: the behaviour before a budget existed.
        let mut session = Session::new("Ada".into(), "default".into());
        session.add_tokens(TokenUsage {
            input: u64::MAX,
            output: 0,
        });
        assert!(session.within_token_budget(0));
    }

    #[test]
    fn a_budget_refuses_once_it_is_reached() {
        let mut session = Session::new("Ada".into(), "default".into());

        session.add_tokens(TokenUsage {
            input: 9_000,
            output: 0,
        });
        assert!(session.within_token_budget(10_000), "under budget");

        session.add_tokens(TokenUsage {
            input: 1_000,
            output: 0,
        });
        // Reaching the budget exhausts it; the check is >=, not >, so a
        // session cannot spend one more turn on the boundary.
        assert!(!session.within_token_budget(10_000), "at the budget");
    }

    #[test]
    fn token_usage_totals_both_directions() {
        assert_eq!(
            TokenUsage {
                input: 10,
                output: 5
            }
            .total(),
            15
        );
        assert_eq!(TokenUsage::default().total(), 0);
    }

    #[test]
    fn token_usage_adds_each_direction_separately() {
        // Kept apart because input and output are priced differently, and a
        // later report will want them separately even though the budget sums.
        let mut a = TokenUsage {
            input: 10,
            output: 5,
        };
        a += TokenUsage {
            input: 1,
            output: 2,
        };
        assert_eq!(
            a,
            TokenUsage {
                input: 11,
                output: 7
            }
        );
    }

    #[test]
    fn token_usage_saturates_too() {
        let mut a = TokenUsage {
            input: u64::MAX,
            output: u64::MAX,
        };
        a += TokenUsage {
            input: 5,
            output: 5,
        };
        assert_eq!(
            a,
            TokenUsage {
                input: u64::MAX,
                output: u64::MAX
            }
        );
    }
}
