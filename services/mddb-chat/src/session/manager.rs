use std::collections::VecDeque;
use std::time::Duration;

use dashmap::DashMap;
use tokio::sync::{Mutex, oneshot};
use uuid::Uuid;

use crate::config::SessionConfig;
use crate::session::types::Session;

pub struct SessionManager {
    /// Every live session, attached or detached (#402). Only the attached
    /// ones hold a `max_concurrent` slot.
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

#[derive(Debug)]
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

/// Why a `resume` was refused (#402).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ResumeError {
    /// No such session: it never existed, was ended, or its TTL ran out.
    NotFound,
    /// A socket still owns the session. Two connections must never share
    /// one; the second would be someone who copied the id while the owner is
    /// still talking.
    StillAttached,
    /// Every `max_concurrent` slot is taken. The session is kept for a later
    /// attempt.
    AtCapacity,
}

impl std::fmt::Display for ResumeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            // The widget rejoins when it reads "session not found", so that
            // phrase is part of the wire protocol.
            Self::NotFound => write!(f, "session not found or expired"),
            Self::StillAttached => write!(f, "session is open in another connection"),
            Self::AtCapacity => write!(f, "server is at capacity; try resuming again shortly"),
        }
    }
}

/// A session the TTL sweep removed, so the caller can announce its end.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ExpiredSession {
    pub id: String,
    pub name: String,
    pub scenario: String,
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
        if self.has_free_slot() {
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

    /// Ends a session for good: `end` from the visitor, or the TTL sweep.
    pub async fn remove_session(&self, id: &str) {
        self.active.remove(id);
        self.fill_free_slots().await;
    }

    /// Marks a session as having no socket (#402) and hands its slot on.
    ///
    /// Returns whether the session existed. The session itself stays, with its
    /// history, until `resume` reattaches it or the TTL sweep removes it.
    pub async fn detach(&self, id: &str) -> bool {
        let found = match self.active.get_mut(id) {
            Some(mut session) => {
                session.detach();
                true
            }
            None => false,
        };
        if found {
            self.fill_free_slots().await;
        }
        found
    }

    /// Gives a detached session back to a socket (#402).
    ///
    /// Returns the session's scenario on success, so the client can be told
    /// which conversation it is back in.
    pub fn resume(&self, id: &str) -> Result<String, ResumeError> {
        // Counted before the entry is locked: iterating the map while holding
        // one of its entries would deadlock on that entry's shard. Two resumes
        // racing for the last slot can both pass, which is a one-off
        // over-capacity, not a leak — the slot is counted correctly again as
        // soon as either socket closes.
        let has_free_slot = self.has_free_slot();
        let mut session = self.active.get_mut(id).ok_or(ResumeError::NotFound)?;
        if session.attached {
            return Err(ResumeError::StillAttached);
        }
        if !has_free_slot {
            return Err(ResumeError::AtCapacity);
        }
        session.attach();
        Ok(session.scenario.clone())
    }

    /// Sessions that currently hold a slot: those with a live socket.
    pub fn attached_count(&self) -> usize {
        self.active.iter().filter(|entry| entry.attached).count()
    }

    /// Sessions kept for a `resume`, holding no slot (#402).
    pub fn detached_count(&self) -> usize {
        self.active.iter().filter(|entry| !entry.attached).count()
    }

    pub async fn queue_len(&self) -> usize {
        self.queue.lock().await.len()
    }

    fn has_free_slot(&self) -> bool {
        self.attached_count() < self.config.max_concurrent
    }

    /// Admits queued visitors into whatever slots are free.
    ///
    /// Bounded by the count rather than "one per call": removing a detached
    /// session frees nothing (its slot was handed on at the disconnect), and
    /// a blind admission there would run past `max_concurrent`.
    async fn fill_free_slots(&self) {
        while self.has_free_slot() {
            if self.admit_from_queue().await.is_none() {
                break;
            }
        }
    }

    /// Removes sessions idle for longer than the TTL, attached or not, and
    /// reports them so the caller can fire `session.end` for each (#402).
    pub async fn cleanup_expired(&self) -> Vec<ExpiredSession> {
        let ttl = Duration::from_secs(self.config.session_ttl_minutes * 60);
        let expired: Vec<ExpiredSession> = self
            .active
            .iter()
            .filter(|entry| entry.last_active.elapsed() > ttl)
            .map(|entry| ExpiredSession {
                id: entry.key().clone(),
                name: entry.name.clone(),
                scenario: entry.scenario.clone(),
            })
            .collect();

        for session in &expired {
            self.active.remove(&session.id);
        }

        self.fill_free_slots().await;
        expired
    }
}
