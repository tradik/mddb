//! Admission tests for the session manager: who gets in, who waits and who is
//! turned away. All three are how a small server survives a crowd (TEST-001).
//!
//! The detach/resume lifecycle (#402) is tested in `resume_tests.rs`.

use tokio::sync::oneshot;

use crate::config::SessionConfig;
use crate::session::manager::{JoinResult, SessionManager};
use crate::session::types::MessageRole;

pub(crate) fn config(max_concurrent: usize, queue_size: usize) -> SessionConfig {
    SessionConfig {
        max_concurrent,
        queue_size,
        max_history_length: 50,
        session_ttl_minutes: 30,
        max_response_length: 4000,
        name_max_chars: 40,
    }
}

pub(crate) fn admitted(result: JoinResult) -> String {
    match result {
        JoinResult::Admitted { session_id } => session_id,
        other => panic!("wanted admission, got {other:?}"),
    }
}

/// The receiver has to be held by the caller: dropping it is how a visitor
/// who closed their browser looks to the manager, and they are then skipped.
pub(crate) fn queued(result: JoinResult) -> oneshot::Receiver<String> {
    match result {
        JoinResult::Queued { admitted, .. } => admitted,
        other => panic!("wanted a place in the queue, got {other:?}"),
    }
}

#[tokio::test]
async fn the_first_arrivals_are_admitted() {
    let m = SessionManager::new(config(2, 4));

    let first = admitted(m.join("Ada".into(), "default".into()).await);
    let second = admitted(m.join("Grace".into(), "default".into()).await);

    assert_ne!(first, second, "two sessions were given the same id");
    assert_eq!(m.attached_count(), 2);
    assert_eq!(m.queue_len().await, 0);
}

#[tokio::test]
async fn arrivals_past_capacity_are_queued_in_order() {
    let m = SessionManager::new(config(1, 3));
    admitted(m.join("Ada".into(), "default".into()).await);

    let mut positions = Vec::new();
    let mut held = Vec::new();
    for name in ["Grace", "Barbara", "Katherine"] {
        let result = m.join(name.into(), "default".into()).await;
        let JoinResult::Queued { position, admitted } = result else {
            panic!("{name} was not queued: {result:?}");
        };
        positions.push(position);
        held.push(admitted);
    }

    // Position is 1-based and reported as "how many are waiting including
    // you", which is what a waiting visitor is shown.
    assert_eq!(positions, vec![1, 2, 3]);
    assert_eq!(m.queue_len().await, 3);
    assert_eq!(m.attached_count(), 1);
}

#[tokio::test]
async fn a_full_queue_turns_people_away() {
    let m = SessionManager::new(config(1, 1));
    admitted(m.join("Ada".into(), "default".into()).await);
    let _grace = queued(m.join("Grace".into(), "default".into()).await);

    // Refusing beats queueing without bound: a queue nobody will reach the
    // front of is a worse answer than "come back later".
    let barbara = m.join("Barbara".into(), "default".into()).await;
    assert!(
        matches!(barbara, JoinResult::Full),
        "a third arrival was {barbara:?}"
    );
    assert_eq!(m.queue_len().await, 1);
}

#[tokio::test]
async fn leaving_admits_the_next_in_line() {
    let m = SessionManager::new(config(1, 2));
    let first = admitted(m.join("Ada".into(), "default".into()).await);
    let _grace = queued(m.join("Grace".into(), "default".into()).await);

    m.remove_session(&first).await;

    assert_eq!(
        m.queue_len().await,
        0,
        "the queued visitor was not admitted"
    );
    assert_eq!(m.attached_count(), 1);
    assert!(
        m.get_session(&first).is_none(),
        "the closed session is still active"
    );
}

#[tokio::test]
async fn the_admitted_visitor_keeps_their_name_and_scenario() {
    let m = SessionManager::new(config(1, 2));
    admitted(m.join("Ada".into(), "default".into()).await);
    let mut rx = queued(m.join("Grace".into(), "support".into()).await);

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
    let mut rx = queued(m.join("Grace".into(), "default".into()).await);

    m.remove_session(&first).await;

    let grace_id = rx
        .try_recv()
        .expect("Grace was woken without being told her session id");

    assert_eq!(
        m.attached_count(),
        1,
        "one visitor was admitted and {} sessions exist",
        m.attached_count()
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
        waiting.push((name, queued(m.join(name.into(), "default".into()).await)));
    }

    for (name, mut rx) in waiting {
        m.remove_session(&current).await;

        current = rx
            .try_recv()
            .unwrap_or_else(|e| panic!("{name} was never admitted: {e}"));

        assert_eq!(
            m.attached_count(),
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

    // Grace joins and immediately gives up: dropping the receiver is what a
    // closed WebSocket looks like to the manager.
    drop(queued(m.join("Grace".into(), "default".into()).await));
    let mut barbara = queued(m.join("Barbara".into(), "default".into()).await);

    m.remove_session(&first).await;

    let id = barbara
        .try_recv()
        .expect("Barbara was skipped because Grace held the slot she had abandoned");
    assert!(m.get_session(&id).is_some());
    assert_eq!(m.attached_count(), 1);
    assert_eq!(m.queue_len().await, 0);
}

#[tokio::test]
async fn admitting_from_an_empty_queue_does_nothing() {
    let m = SessionManager::new(config(2, 2));

    assert!(m.admit_from_queue().await.is_none());
    assert_eq!(m.attached_count(), 0);
}

#[tokio::test]
async fn removing_a_session_that_does_not_exist_is_harmless() {
    let m = SessionManager::new(config(1, 1));
    admitted(m.join("Ada".into(), "default".into()).await);

    m.remove_session("not-a-session").await;

    assert_eq!(m.attached_count(), 1);
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

    let reaped = m.cleanup_expired().await;

    assert!(reaped.is_empty(), "a fresh session was reaped");
    assert_eq!(m.attached_count(), 1);
}

#[tokio::test]
async fn cleanup_reaps_expired_sessions_and_admits_the_queue() {
    // A zero-minute TTL expires everything immediately, which is what a
    // long-abandoned session looks like without waiting for one.
    let mut cfg = config(1, 2);
    cfg.session_ttl_minutes = 0;
    let m = SessionManager::new(cfg);

    let ada = admitted(m.join("Ada".into(), "default".into()).await);
    let _grace = queued(m.join("Grace".into(), "support".into()).await);
    assert_eq!(m.queue_len().await, 1);

    // Instant has whole-nanosecond resolution; a zero TTL is already past.
    tokio::time::sleep(std::time::Duration::from_millis(2)).await;
    let reaped = m.cleanup_expired().await;

    // The caller gets told who was reaped, so it can fire the session.end
    // webhook: a session that times out ended just as surely as one the
    // visitor closed (#402).
    assert_eq!(reaped.len(), 1);
    assert_eq!(reaped[0].id, ada);
    assert_eq!(reaped[0].name, "Ada");
    assert_eq!(reaped[0].scenario, "default");

    // Ada's slot was freed and Grace took it — and Grace, being brand new,
    // must not be reaped in the same pass.
    assert_eq!(
        m.queue_len().await,
        0,
        "the queue was not drained into the freed slot"
    );
    assert!(m.attached_count() <= 1, "cleanup admitted past capacity");
}

#[tokio::test]
async fn cleanup_does_not_admit_past_capacity() {
    let m = SessionManager::new(config(2, 5));
    for name in ["Ada", "Grace"] {
        admitted(m.join(name.into(), "default".into()).await);
    }
    let mut waiting = Vec::new();
    for name in ["Barbara", "Katherine", "Dorothy"] {
        waiting.push(queued(m.join(name.into(), "default".into()).await));
    }

    m.cleanup_expired().await;

    assert_eq!(
        m.attached_count(),
        2,
        "cleanup let the server past max_concurrent"
    );
    assert_eq!(m.queue_len().await, 3);
}
