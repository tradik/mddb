use serde_json::json;
use tokio::sync::mpsc;

use super::*;

fn completion() -> Completion {
    Completion {
        id: "chatcmpl-1".into(),
        created: 1,
        model: "support".into(),
    }
}

#[tokio::test]
async fn collected_gathers_the_text_and_the_error() {
    let mut out = Collected::default();
    out.chunk("a".into()).await;
    out.chunk("b".into()).await;
    out.error("broke").await;
    out.done("id".into()).await;
    assert_eq!(out.text, "ab");
    assert_eq!(out.error.as_deref(), Some("broke"));
}

#[tokio::test]
async fn frames_carry_the_role_once_then_the_content() {
    let (tx, mut rx) = mpsc::channel(8);
    let mut out = Frames::new(tx, completion());
    out.chunk("a".into()).await;
    out.chunk("b".into()).await;
    out.error("broke").await;
    out.done("id".into()).await;
    drop(out);

    let first = rx.recv().await.unwrap();
    assert_eq!(
        first["choices"][0]["delta"],
        json!({"role": "assistant", "content": "a"})
    );
    let second = rx.recv().await.unwrap();
    assert_eq!(second["choices"][0]["delta"], json!({"content": "b"}));
    let error = rx.recv().await.unwrap();
    assert_eq!(error["error"]["message"], "broke");
    assert!(rx.recv().await.is_none(), "done sends nothing");
}

#[tokio::test]
async fn a_client_that_left_does_not_stop_the_turn() {
    let (tx, rx) = mpsc::channel(1);
    drop(rx);
    let mut out = Frames::new(tx, completion());
    out.chunk("nobody reads this".into()).await;
}
