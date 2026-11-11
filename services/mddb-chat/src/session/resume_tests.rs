//! The detach/resume lifecycle (#402).
//!
//! A session used to be removed the moment its WebSocket closed, so a
//! `resume` after a reconnect always got "session not found or expired" —
//! every network blip (Cloudflare drops idle sockets after 100 s, mobile
//! networks, page reloads) lost the conversation the LLM sees. A session now
//! outlives its socket: it is *detached* on disconnect, kept until
//! `session_ttl_minutes` of inactivity, and reattached by `resume`.

use crate::session::manager::{ResumeError, SessionManager};
use crate::session::manager_tests::{admitted, config, queued};
use crate::session::types::MessageRole;

#[tokio::test]
async fn a_detached_session_is_kept_with_its_history() {
    let m = SessionManager::new(config(1, 1));
    let id = admitted(m.join("Ada".into(), "default".into()).await);
    m.get_session_mut(&id)
        .expect("just created")
        .add_message(MessageRole::User, "remember this".into());

    assert!(m.detach(&id).await, "detach did not find the session");

    let session = m
        .get_session(&id)
        .expect("the session was removed on disconnect");
    assert!(!session.attached);
    assert_eq!(session.history.len(), 1, "the history was lost");
    assert_eq!(m.attached_count(), 0);
    assert_eq!(m.detached_count(), 1);
}

#[tokio::test]
async fn detaching_an_unknown_session_is_harmless() {
    let m = SessionManager::new(config(1, 1));

    assert!(!m.detach("not-a-session").await);
    assert_eq!(m.detached_count(), 0);
}

#[tokio::test]
async fn a_detached_session_does_not_hold_a_slot() {
    // Detached sessions cost memory, not an LLM. Counting them against
    // max_concurrent would let two closed tabs block a two-seat server for
    // the whole TTL.
    let m = SessionManager::new(config(1, 2));
    let ada = admitted(m.join("Ada".into(), "default".into()).await);
    let mut grace = queued(m.join("Grace".into(), "default".into()).await);

    m.detach(&ada).await;

    let grace_id = grace
        .try_recv()
        .expect("the slot freed by a disconnect was not given to the queue");
    assert!(m.get_session(&grace_id).is_some());
    assert_eq!(m.attached_count(), 1);
    assert_eq!(m.detached_count(), 1);
}

#[tokio::test]
async fn resume_reattaches_a_detached_session_and_returns_its_scenario() {
    let m = SessionManager::new(config(1, 1));
    let id = admitted(m.join("Ada".into(), "support".into()).await);
    m.detach(&id).await;

    let scenario = m.resume(&id).expect("resume refused a detached session");

    assert_eq!(scenario, "support");
    assert!(m.get_session(&id).expect("missing").attached);
    assert_eq!(m.attached_count(), 1);
    assert_eq!(m.detached_count(), 0);
}

#[tokio::test]
async fn resume_refuses_a_session_that_is_still_attached() {
    // Two sockets must never share a session: the second one would be
    // someone who guessed or copied the id while the owner is still talking.
    let m = SessionManager::new(config(2, 1));
    let id = admitted(m.join("Ada".into(), "default".into()).await);

    assert_eq!(m.resume(&id), Err(ResumeError::StillAttached));
}

#[tokio::test]
async fn resume_refuses_an_unknown_or_expired_session() {
    let m = SessionManager::new(config(2, 1));

    assert_eq!(m.resume("never-existed"), Err(ResumeError::NotFound));
}

#[tokio::test]
async fn resume_refuses_when_every_slot_is_taken() {
    // The slot freed by a disconnect may have gone to the queue in the
    // meantime. Reattaching anyway would run the server past max_concurrent,
    // which is the operator's LLM budget guard.
    let m = SessionManager::new(config(1, 1));
    let ada = admitted(m.join("Ada".into(), "default".into()).await);
    m.detach(&ada).await;
    admitted(m.join("Grace".into(), "default".into()).await);

    assert_eq!(m.resume(&ada), Err(ResumeError::AtCapacity));
    // The session is still there for a later attempt.
    assert_eq!(m.detached_count(), 1);
}

#[tokio::test]
async fn a_detached_session_expires_after_the_ttl() {
    let mut cfg = config(2, 1);
    cfg.session_ttl_minutes = 0;
    let m = SessionManager::new(cfg);
    let id = admitted(m.join("Ada".into(), "default".into()).await);
    m.detach(&id).await;

    tokio::time::sleep(std::time::Duration::from_millis(2)).await;
    let reaped = m.cleanup_expired().await;

    assert_eq!(reaped.len(), 1);
    assert_eq!(reaped[0].id, id);
    assert_eq!(m.resume(&id), Err(ResumeError::NotFound));
}

#[tokio::test]
async fn ending_a_detached_session_frees_no_slot_twice() {
    // The disconnect already handed the slot on. Removing the detached
    // session must not admit a second visitor past capacity.
    let m = SessionManager::new(config(1, 2));
    let ada = admitted(m.join("Ada".into(), "default".into()).await);
    let mut grace = queued(m.join("Grace".into(), "default".into()).await);
    let mut barbara = queued(m.join("Barbara".into(), "default".into()).await);

    m.detach(&ada).await;
    grace.try_recv().expect("Grace should have Ada's slot");

    m.remove_session(&ada).await;

    assert!(
        barbara.try_recv().is_err(),
        "removing a detached session admitted Barbara past capacity"
    );
    assert_eq!(m.attached_count(), 1);
    assert_eq!(m.detached_count(), 0);
}

#[tokio::test]
async fn resume_errors_read_well() {
    for (err, needle) in [
        (ResumeError::NotFound, "not found"),
        (ResumeError::StillAttached, "another connection"),
        (ResumeError::AtCapacity, "capacity"),
    ] {
        let text = err.to_string();
        assert!(text.contains(needle), "{err:?} rendered as {text:?}");
    }
}
