use std::collections::VecDeque;
use std::time::Duration;

use dashmap::DashMap;
use tokio::sync::{Mutex, oneshot};
use uuid::Uuid;

use crate::config::SessionConfig;
use crate::session::types::Session;

pub struct SessionManager {
    active: DashMap<String, Session>,
    queue: Mutex<VecDeque<QueueEntry>>,
    config: SessionConfig,
}

struct QueueEntry {
    pub name: String,
    pub scenario: String,
    /// Delivers the session created for this visitor when a slot frees.
    ///
    /// TEST-001: this used to be a bare `Notify`. `admit_from_queue` created
    /// the session, inserted it into `active`, and then only woke the waiting
    /// task — which called `join` again and created a *second* one. Every
    /// admission from the queue left an orphaned session holding a
    /// `max_concurrent` slot until its TTL expired, and at capacity 1 the
    /// orphan took the freed slot, so the visitor it was created for was
    /// re-queued behind a phantom of themselves and waited forever. The id now
    /// travels with the wake-up, so there is exactly one session per visitor.
    pub admitted: oneshot::Sender<String>,
}

pub enum JoinResult {
    Admitted {
        session_id: String,
    },
    Queued {
        position: usize,
        /// Resolves with the session id once a slot frees. An error means the
        /// manager was dropped, which only happens at shutdown.
        admitted: oneshot::Receiver<String>,
    },
    Full,
}

impl SessionManager {
    pub fn new(config: SessionConfig) -> Self {
        Self {
            active: DashMap::new(),
            queue: Mutex::new(VecDeque::new()),
            config,
        }
    }

    pub async fn join(&self, name: String, scenario: String) -> JoinResult {
        // Check if there's room
        if self.active.len() < self.config.max_concurrent {
            let session_id = Uuid::new_v4().to_string();
            let session = Session::new(name, scenario);
            self.active.insert(session_id.clone(), session);
            return JoinResult::Admitted { session_id };
        }

        // Try to queue
        let mut queue = self.queue.lock().await;
        if queue.len() >= self.config.queue_size {
            return JoinResult::Full;
        }

        let (tx, rx) = oneshot::channel();
        queue.push_back(QueueEntry {
            name,
            scenario,
            admitted: tx,
        });
        let position = queue.len();
        JoinResult::Queued {
            position,
            admitted: rx,
        }
    }

    /// Creates the session for the visitor at the front of the queue and hands
    /// it to them.
    ///
    /// Returns `None` when the queue is empty, or when the visitor gave up
    /// while waiting — a dropped receiver means the WebSocket closed, and the
    /// session created for it is discarded rather than left holding a slot.
    pub async fn admit_from_queue(&self) -> Option<(String, String, String)> {
        let mut queue = self.queue.lock().await;

        while let Some(entry) = queue.pop_front() {
            let session_id = Uuid::new_v4().to_string();

            match entry.admitted.send(session_id.clone()) {
                Ok(()) => {
                    let session = Session::new(entry.name.clone(), entry.scenario.clone());
                    self.active.insert(session_id.clone(), session);
                    return Some((session_id, entry.name, entry.scenario));
                }
                Err(_) => {
                    // The visitor closed their connection while queued. Take
                    // the next one instead of burning the freed slot on
                    // someone who is no longer there.
                    tracing::debug!(name = %entry.name, "a queued visitor left before their turn");
                    continue;
                }
            }
        }

        None
    }

    pub fn get_session(&self, id: &str) -> Option<dashmap::mapref::one::Ref<'_, String, Session>> {
        self.active.get(id)
    }

    pub fn get_session_mut(
        &self,
        id: &str,
    ) -> Option<dashmap::mapref::one::RefMut<'_, String, Session>> {
        self.active.get_mut(id)
    }

    pub async fn remove_session(&self, id: &str) {
        self.active.remove(id);
        // Try to admit next from queue
        self.admit_from_queue().await;
    }

    pub fn active_count(&self) -> usize {
        self.active.len()
    }

    pub async fn queue_len(&self) -> usize {
        self.queue.lock().await.len()
    }

    /// Clean up expired sessions
    pub async fn cleanup_expired(&self) {
        let ttl = Duration::from_secs(self.config.session_ttl_minutes * 60);
        let expired: Vec<String> = self
            .active
            .iter()
            .filter(|entry| entry.last_active.elapsed() > ttl)
            .map(|entry| entry.key().clone())
            .collect();

        for id in expired {
            self.active.remove(&id);
        }

        // Try to admit queued users for freed slots
        while self.active.len() < self.config.max_concurrent {
            if self.admit_from_queue().await.is_none() {
                break;
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::session::types::MessageRole;

    // TEST-001. The session manager decides who gets in, who waits and who is
    // turned away. All three are how a small server survives a crowd, and none
    // of them had a test.

    fn config(max_concurrent: usize, queue_size: usize) -> SessionConfig {
        SessionConfig {
            max_concurrent,
            queue_size,
            max_history_length: 50,
            session_ttl_minutes: 30,
            max_response_length: 4000,
            name_max_chars: 40,
        }
    }

    fn admitted(result: JoinResult) -> String {
        match result {
            JoinResult::Admitted { session_id } => session_id,
            JoinResult::Queued { position, .. } => panic!("queued at {position}, wanted admission"),
            JoinResult::Full => panic!("turned away, wanted admission"),
        }
    }

    #[tokio::test]
    async fn the_first_arrivals_are_admitted() {
        let m = SessionManager::new(config(2, 4));

        let first = admitted(m.join("Ada".into(), "default".into()).await);
        let second = admitted(m.join("Grace".into(), "default".into()).await);

        assert_ne!(first, second, "two sessions were given the same id");
        assert_eq!(m.active_count(), 2);
        assert_eq!(m.queue_len().await, 0);
    }

    #[tokio::test]
    async fn arrivals_past_capacity_are_queued_in_order() {
        let m = SessionManager::new(config(1, 3));
        admitted(m.join("Ada".into(), "default".into()).await);

        let positions: Vec<usize> = {
            let mut out = Vec::new();
            for name in ["Grace", "Barbara", "Katherine"] {
                match m.join(name.into(), "default".into()).await {
                    JoinResult::Queued { position, .. } => out.push(position),
                    other => panic!("{name} was not queued: {}", describe(&other)),
                }
            }
            out
        };

        // Position is 1-based and reported as "how many are waiting including
        // you", which is what a waiting visitor is shown.
        assert_eq!(positions, vec![1, 2, 3]);
        assert_eq!(m.queue_len().await, 3);
        assert_eq!(m.active_count(), 1);
    }

    #[tokio::test]
    async fn a_full_queue_turns_people_away() {
        let m = SessionManager::new(config(1, 1));
        admitted(m.join("Ada".into(), "default".into()).await);
        let _grace = match m.join("Grace".into(), "default".into()).await {
            JoinResult::Queued { admitted, .. } => admitted,
            other => panic!("Grace was {}", describe(&other)),
        };

        // Refusing beats queueing without bound: a queue nobody will reach the
        // front of is a worse answer than "come back later".
        match m.join("Barbara".into(), "default".into()).await {
            JoinResult::Full => {}
            other => panic!("a third arrival was {}", describe(&other)),
        }
        assert_eq!(m.queue_len().await, 1);
    }

    #[tokio::test]
    async fn leaving_admits_the_next_in_line() {
        let m = SessionManager::new(config(1, 2));
        let first = admitted(m.join("Ada".into(), "default".into()).await);

        // The receiver has to be held: dropping it is how a visitor who closed
        // their browser looks to the manager, and they are then skipped.
        let _grace = match m.join("Grace".into(), "default".into()).await {
            JoinResult::Queued { admitted, .. } => admitted,
            other => panic!("Grace was {}", describe(&other)),
        };

        m.remove_session(&first).await;

        assert_eq!(
            m.queue_len().await,
            0,
            "the queued visitor was not admitted"
        );
        assert_eq!(m.active_count(), 1);
        assert!(
            m.get_session(&first).is_none(),
            "the closed session is still active"
        );
    }

    #[tokio::test]
    async fn the_admitted_visitor_keeps_their_name_and_scenario() {
        let m = SessionManager::new(config(1, 2));
        admitted(m.join("Ada".into(), "default".into()).await);

        let queued = m.join("Grace".into(), "support".into()).await;
        let mut rx = match queued {
            JoinResult::Queued { admitted, .. } => admitted,
            other => panic!("Grace was {}", describe(&other)),
        };

        let (id, name, scenario) = m
            .admit_from_queue()
            .await
            .expect("the queue would not give up its only entry");

        assert!(!id.is_empty());
        assert_eq!(name, "Grace");
        assert_eq!(
            scenario, "support",
            "the queued visitor lost their scenario"
        );

        // The same id must reach the visitor waiting on the channel, or they
        // would be holding a session nobody created for them.
        assert_eq!(
            rx.try_recv().expect("Grace was never told her session id"),
            id
        );
    }

    // TEST-001: admit_from_queue created the session and only woke the waiting
    // task, which then called join() again and created a second one. Every
    // admission left an orphan holding a max_concurrent slot until its TTL.
    #[tokio::test]
    async fn admitting_from_the_queue_creates_exactly_one_session() {
        let m = SessionManager::new(config(1, 2));
        let first = admitted(m.join("Ada".into(), "default".into()).await);
        let mut rx = match m.join("Grace".into(), "default".into()).await {
            JoinResult::Queued { admitted, .. } => admitted,
            other => panic!("Grace was {}", describe(&other)),
        };

        m.remove_session(&first).await;

        let grace_id = rx
            .try_recv()
            .expect("Grace was woken without being told her session id");

        assert_eq!(
            m.active_count(),
            1,
            "one visitor was admitted and {} sessions exist",
            m.active_count()
        );
        assert!(
            m.get_session(&grace_id).is_some(),
            "the id Grace was given does not name an active session"
        );
    }

    // At capacity 1 the orphan took the freed slot, so the visitor it was
    // created for was re-queued behind a phantom of themselves.
    #[tokio::test]
    async fn a_queue_drains_completely_at_capacity_one() {
        let m = SessionManager::new(config(1, 3));
        let mut current = admitted(m.join("Ada".into(), "default".into()).await);

        let mut waiting = Vec::new();
        for name in ["Grace", "Barbara", "Katherine"] {
            match m.join(name.into(), "default".into()).await {
                JoinResult::Queued { admitted, .. } => waiting.push((name, admitted)),
                other => panic!("{name} was {}", describe(&other)),
            }
        }

        for (name, mut rx) in waiting {
            m.remove_session(&current).await;

            current = rx
                .try_recv()
                .unwrap_or_else(|e| panic!("{name} was never admitted: {e}"));

            assert_eq!(
                m.active_count(),
                1,
                "{name}'s admission left extra sessions"
            );
        }

        assert_eq!(m.queue_len().await, 0, "the queue never drained");
    }

    // A visitor who closes their browser while waiting must not consume the
    // slot that frees up.
    #[tokio::test]
    async fn a_visitor_who_leaves_the_queue_does_not_take_a_slot() {
        let m = SessionManager::new(config(1, 3));
        let first = admitted(m.join("Ada".into(), "default".into()).await);

        // Grace joins and immediately gives up: dropping the receiver is what
        // a closed WebSocket looks like to the manager.
        match m.join("Grace".into(), "default".into()).await {
            JoinResult::Queued { admitted, .. } => drop(admitted),
            other => panic!("Grace was {}", describe(&other)),
        }
        let mut barbara = match m.join("Barbara".into(), "default".into()).await {
            JoinResult::Queued { admitted, .. } => admitted,
            other => panic!("Barbara was {}", describe(&other)),
        };

        m.remove_session(&first).await;

        let id = barbara
            .try_recv()
            .expect("Barbara was skipped because Grace held the slot she had abandoned");
        assert!(m.get_session(&id).is_some());
        assert_eq!(m.active_count(), 1);
        assert_eq!(m.queue_len().await, 0);
    }

    #[tokio::test]
    async fn admitting_from_an_empty_queue_does_nothing() {
        let m = SessionManager::new(config(2, 2));

        assert!(m.admit_from_queue().await.is_none());
        assert_eq!(m.active_count(), 0);
    }

    #[tokio::test]
    async fn removing_a_session_that_does_not_exist_is_harmless() {
        let m = SessionManager::new(config(1, 1));
        admitted(m.join("Ada".into(), "default".into()).await);

        m.remove_session("not-a-session").await;

        assert_eq!(m.active_count(), 1);
    }

    #[tokio::test]
    async fn a_session_can_be_read_and_written_through_the_manager() {
        let m = SessionManager::new(config(1, 1));
        let id = admitted(m.join("Ada".into(), "default".into()).await);

        m.get_session_mut(&id)
            .expect("the session just created is missing")
            .add_message(MessageRole::User, "hello".into());

        let session = m.get_session(&id).expect("missing");
        assert_eq!(session.history.len(), 1);
        assert_eq!(session.user_turns, 1);
    }

    #[tokio::test]
    async fn cleanup_keeps_sessions_that_are_still_within_their_ttl() {
        let m = SessionManager::new(config(2, 2));
        admitted(m.join("Ada".into(), "default".into()).await);

        m.cleanup_expired().await;

        assert_eq!(m.active_count(), 1, "a fresh session was reaped");
    }

    #[tokio::test]
    async fn cleanup_reaps_expired_sessions_and_admits_the_queue() {
        // A zero-minute TTL expires everything immediately, which is what a
        // long-abandoned session looks like without waiting for one.
        let mut cfg = config(1, 2);
        cfg.session_ttl_minutes = 0;
        let m = SessionManager::new(cfg);

        admitted(m.join("Ada".into(), "default".into()).await);
        let _grace = match m.join("Grace".into(), "default".into()).await {
            JoinResult::Queued { admitted, .. } => admitted,
            other => panic!("Grace was {}", describe(&other)),
        };
        assert_eq!(m.queue_len().await, 1);

        // Instant has whole-nanosecond resolution; a zero TTL is already past.
        tokio::time::sleep(std::time::Duration::from_millis(2)).await;
        m.cleanup_expired().await;

        // Ada's slot was freed and Grace took it — and Grace, being brand new,
        // must not be reaped in the same pass.
        assert_eq!(
            m.queue_len().await,
            0,
            "the queue was not drained into the freed slot"
        );
        assert!(m.active_count() <= 1, "cleanup admitted past capacity");
    }

    #[tokio::test]
    async fn cleanup_does_not_admit_past_capacity() {
        let m = SessionManager::new(config(2, 5));
        for name in ["Ada", "Grace"] {
            admitted(m.join(name.into(), "default".into()).await);
        }
        let _waiting: Vec<_> = {
            let mut out = Vec::new();
            for name in ["Barbara", "Katherine", "Dorothy"] {
                match m.join(name.into(), "default".into()).await {
                    JoinResult::Queued { admitted, .. } => out.push(admitted),
                    other => panic!("{name} was {}", describe(&other)),
                }
            }
            out
        };

        m.cleanup_expired().await;

        assert_eq!(
            m.active_count(),
            2,
            "cleanup let the server past max_concurrent"
        );
        assert_eq!(m.queue_len().await, 3);
    }

    fn describe(result: &JoinResult) -> String {
        match result {
            JoinResult::Admitted { .. } => "admitted".into(),
            JoinResult::Queued { position, .. } => format!("queued at {position}"),
            JoinResult::Full => "turned away".into(),
        }
    }
}
