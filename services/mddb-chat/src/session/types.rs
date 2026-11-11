use crate::llm::provider::TokenUsage;
use crate::session::conversation::{ConversationRef, ConversationSummary, HistoryEntry};
use crate::session::pace::MessagePace;
use serde::{Deserialize, Serialize};
use std::time::Instant;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ChatMessage {
    pub role: MessageRole,
    pub content: String,
    pub timestamp: u64,
    /// Read back from mddbd rather than said in this session (#405). The
    /// model is told such assistant turns are context, not instructions.
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub restored: bool,
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
    /// Whether a WebSocket currently owns this session (#402).
    ///
    /// A session used to be removed the moment its socket closed, which made
    /// `resume` impossible: every network blip lost the conversation. It now
    /// stays in the manager as *detached* until `session_ttl_minutes` of
    /// inactivity, and a reconnecting client reattaches it. A detached session
    /// does not count toward `max_concurrent`: it costs memory, not an LLM.
    pub attached: bool,
    /// When the recent messages were accepted, for the scenario's
    /// per-session rate limit (#403).
    pub pace: MessagePace,
    /// Where a signed session's turns are stored (#405); `None` for an
    /// anonymous one, which lives in memory only.
    pub conversation: Option<ConversationRef>,
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
            attached: true,
            pace: MessagePace::default(),
            conversation: None,
        }
    }

    /// Marks the session as having no socket (#402).
    ///
    /// The idle clock restarts here: the TTL is "how long after the visitor
    /// went away do we keep their conversation", so it counts from the
    /// disconnect, not from whatever they last said.
    pub fn detach(&mut self) {
        self.attached = false;
        self.last_active = Instant::now();
    }

    /// Gives the session back to a socket (#402).
    pub fn attach(&mut self) {
        self.attached = true;
        self.last_active = Instant::now();
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
            restored: false,
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
        /// Optional with a token that carries a name (#405).
        #[serde(default)]
        name: String,
        #[serde(default = "default_scenario")]
        scenario: String,
        /// The host's signed join token (#405); absent for an anonymous join.
        #[serde(default)]
        token: Option<String>,
        /// Reopen this stored conversation of the token's user (#405).
        #[serde(default)]
        conversation_id: Option<String>,
        /// Start a new stored conversation instead of resuming the latest.
        #[serde(default)]
        new_conversation: bool,
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
        /// The `message_id` of the `done` frame that ended the rated answer
        /// (#399). Optional: clients built before it existed send none.
        #[serde(default)]
        message_id: Option<String>,
    },
    Ping,
    /// The signed user's conversations in this scenario (#405).
    #[serde(rename = "list_conversations")]
    ListConversations,
    /// Deletes every conversation of the signed user (#405); refused unless
    /// `confirm` is true.
    #[serde(rename = "delete_conversations")]
    DeleteConversations {
        #[serde(default)]
        confirm: bool,
    },
}

#[derive(Debug, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "lowercase")]
pub enum WsOutgoing {
    Session {
        id: String,
        scenario: String,
        /// The stored conversation of a signed session (#405).
        #[serde(default, skip_serializing_if = "Option::is_none")]
        conversation_id: Option<String>,
    },
    /// The restored messages of a resumed conversation, oldest first (#405).
    History {
        messages: Vec<HistoryEntry>,
    },
    /// Answers `list_conversations`, newest first (#405).
    Conversations {
        conversations: Vec<ConversationSummary>,
    },
    /// Answers `delete_conversations`: how many went, and the fresh
    /// conversation the session continues in (#405).
    #[serde(rename = "conversations_deleted")]
    ConversationsDeleted {
        deleted: i32,
        conversation_id: String,
    },
    Queued {
        position: usize,
    },
    Chunk {
        content: String,
    },
    /// Ends an answer. `message_id` names the answer so that a `feedback`
    /// can refer to it (#399); clients that predate it ignore the extra field.
    Done {
        #[serde(default)]
        message_id: String,
    },
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
        assert!(
            matches!(incoming, WsIncoming::Message { ref content } if content == "hello"),
            "parsed as {incoming:?}"
        );
    }

    // #399: the frame that ends an answer names it, so feedback can too.

    #[test]
    fn a_done_frame_carries_the_answers_message_id() {
        let json = serde_json::to_string(&WsOutgoing::Done {
            message_id: "ans-1".into(),
        })
        .expect("serialise");

        assert!(
            json.contains(r#""type":"done""#),
            "unexpected shape: {json}"
        );
        assert!(
            json.contains(r#""message_id":"ans-1""#),
            "unexpected shape: {json}"
        );
    }

    #[test]
    fn a_done_frame_without_an_id_still_parses() {
        // The pre-#399 shape, so a reader of recorded traffic is not broken.
        let parsed: WsOutgoing = serde_json::from_str(r#"{"type":"done"}"#).expect("parse");
        assert!(
            matches!(parsed, WsOutgoing::Done { ref message_id } if message_id.is_empty()),
            "parsed as {parsed:?}"
        );
    }

    #[test]
    fn feedback_may_name_the_answer_it_rates() {
        let with_id: WsIncoming = serde_json::from_str(
            r#"{"type":"feedback","rating":"down","question":"q","answer":"a","message_id":"ans-1"}"#,
        )
        .expect("parse");
        assert!(matches!(
            with_id,
            WsIncoming::Feedback { ref message_id, .. } if message_id.as_deref() == Some("ans-1")
        ));

        // A widget built before message ids sends none; the message must still
        // be accepted, since refusing it would break every deployed client.
        let without: WsIncoming = serde_json::from_str(
            r#"{"type":"feedback","rating":"up","question":"q","answer":"a"}"#,
        )
        .expect("parse");
        assert!(matches!(
            without,
            WsIncoming::Feedback { ref message_id, ref rating, .. }
                if message_id.is_none() && rating == "up"
        ));
    }

    #[test]
    fn a_join_without_a_scenario_gets_the_default_one() {
        let incoming: WsIncoming =
            serde_json::from_str(r#"{"type":"join","name":"Ada"}"#).expect("parse");
        assert!(
            matches!(&incoming, WsIncoming::Join { scenario, .. } if scenario == "assistant"),
            "parsed as {incoming:?}"
        );
    }

    // #402: a session used to be removed the moment its socket closed, so a
    // resume after a reconnect always found nothing. It now survives a
    // disconnect as "detached" until its TTL runs out.

    #[test]
    fn a_new_session_is_attached_to_its_socket() {
        assert!(session().attached);
    }

    #[test]
    fn detaching_marks_the_session_and_restarts_its_idle_clock() {
        let mut s = session();
        let before = s.last_active;
        std::thread::sleep(std::time::Duration::from_millis(5));

        s.detach();

        assert!(!s.attached);
        assert!(
            s.last_active > before,
            "the TTL must count from the disconnect, not from the last message"
        );
    }

    #[test]
    fn attaching_reverses_a_detach_and_marks_the_session_active() {
        let mut s = session();
        s.detach();
        let before = s.last_active;
        std::thread::sleep(std::time::Duration::from_millis(5));

        s.attach();

        assert!(s.attached);
        assert!(s.last_active > before);
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
