//! How fast one session may send messages (#403).
//!
//! The global limiter in `security/rate_limiter.rs` is charged per client
//! address and protects the server. This one belongs to a session and
//! enforces a scenario's `rateLimit.messagesPerMinute`: it lives and dies
//! with the session, and reads the limit on every message, so a limit edited
//! in mddbd applies from the next message on.

use std::collections::VecDeque;
use std::time::{Duration, Instant};

const WINDOW: Duration = Duration::from_secs(60);

/// When this session's recent messages were accepted, oldest first.
#[derive(Debug, Default)]
pub struct MessagePace {
    recent: VecDeque<Instant>,
}

impl MessagePace {
    /// Whether one more message fits `per_minute` at `now`, counting it when
    /// it does. A refused message is not counted, so a visitor who keeps
    /// trying is not pushed further back. 0 means no limit.
    pub fn admit(&mut self, per_minute: u32, now: Instant) -> bool {
        if per_minute == 0 {
            return true;
        }
        while let Some(oldest) = self.recent.front()
            && now.saturating_duration_since(*oldest) >= WINDOW
        {
            self.recent.pop_front();
        }
        if self.recent.len() >= per_minute as usize {
            return false;
        }
        self.recent.push_back(now);
        true
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn no_limit_admits_everything_and_remembers_nothing() {
        let mut pace = MessagePace::default();
        let now = Instant::now();

        assert!((0..1000).all(|_| pace.admit(0, now)));
        assert!(pace.recent.is_empty());
    }

    #[test]
    fn a_limit_admits_that_many_messages_within_a_minute() {
        let mut pace = MessagePace::default();
        let start = Instant::now();

        assert!(pace.admit(2, start));
        assert!(pace.admit(2, start + Duration::from_secs(10)));
        assert!(!pace.admit(2, start + Duration::from_secs(20)));
        assert!(
            !pace.admit(2, start + Duration::from_secs(59)),
            "a refused message must not have been counted"
        );
    }

    #[test]
    fn a_message_older_than_a_minute_makes_room_again() {
        let mut pace = MessagePace::default();
        let start = Instant::now();
        pace.admit(2, start);
        pace.admit(2, start + Duration::from_secs(30));

        assert!(pace.admit(2, start + Duration::from_secs(60)));
        assert!(!pace.admit(2, start + Duration::from_secs(61)));
        assert!(pace.admit(2, start + Duration::from_secs(90)));
    }

    #[test]
    fn a_lowered_limit_applies_to_the_messages_already_sent() {
        let mut pace = MessagePace::default();
        let start = Instant::now();
        for _ in 0..5 {
            pace.admit(10, start);
        }

        assert!(!pace.admit(3, start + Duration::from_secs(1)));
    }
}
