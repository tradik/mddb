//! #405: when a join is not let in — a bad token, a token for another
//! scenario, an unsigned join where identity is required, or no store.

use serde_json::json;

use super::signed::{SECRET, config, join_with, server, token};
use crate::chat::testing::{ConfigToml, Harness};
use crate::grpc::testing::FakeMddbd;

async fn error_of_join(server: &Harness, extra: serde_json::Value) -> String {
    let mut client = server.connect().await;
    let frame = join_with(&mut client, extra).await;
    assert_eq!(frame["type"], "error", "the join was not refused: {frame}");
    frame["message"].as_str().unwrap().to_string()
}

#[tokio::test]
async fn bad_tokens_are_refused_and_admit_nobody() {
    let (_mddbd, server) = server().await;

    let forged = format!("{}x", token("u", "assistant"));
    let err = error_of_join(&server, json!({"token": forged})).await;
    assert_eq!(err, "unauthorized: identity token rejected: bad signature");

    let err = error_of_join(&server, json!({"token": token("u", "support")})).await;
    assert_eq!(
        err,
        "unauthorized: the identity token is for another scenario"
    );

    let err = error_of_join(&server, json!({"conversation_id": "conv-1"})).await;
    assert_eq!(err, "unauthorized: conversation_id needs a signed join");

    assert_eq!(server.state.session_manager.attached_count(), 0);
}

#[tokio::test]
async fn a_required_identity_refuses_an_unsigned_join() {
    let mddbd = FakeMddbd::start().await;
    let server =
        Harness::start_with_mddbd(ConfigToml::default().identity(SECRET, true), &mddbd).await;

    let err = error_of_join(&server, json!({})).await;

    assert_eq!(err, "unauthorized: a signed identity token is required");
}

#[tokio::test]
async fn a_token_on_a_server_without_a_secret_is_refused() {
    let mddbd = FakeMddbd::start().await;
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;

    let err = error_of_join(&server, json!({"token": token("u", "assistant")})).await;

    assert_eq!(
        err,
        "unauthorized: signed joins are not enabled on this server"
    );
}

#[tokio::test]
async fn a_signed_join_needs_mddbd() {
    let server = Harness::start_before_mddbd(config()).await;

    let err = error_of_join(&server, json!({"token": token("u", "assistant")})).await;

    assert!(err.contains("not connected yet"), "{err}");
    assert_eq!(server.state.session_manager.attached_count(), 0);
}

#[tokio::test]
async fn a_failing_store_refuses_the_join_and_frees_the_seat() {
    let (mddbd, server) = server().await;
    mddbd.fail_with(Some(tonic::Code::Unavailable));

    let err = error_of_join(&server, json!({"token": token("u", "assistant")})).await;

    assert!(err.starts_with("grpc error"), "{err}");
    assert_eq!(server.state.session_manager.attached_count(), 0);
}
