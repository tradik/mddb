//! #413: the loop guard end to end — a looping model is stopped, the request
//! cancelled, the stop reported, and legitimate answers left alone.

use std::sync::atomic::Ordering;

use super::{ask, collect, five_tool_rounds};
use crate::chat::loop_guard::MARKER;
use crate::chat::testing::{ConfigToml, Harness};
use crate::grpc::client::proto::{ChatLlmProto, ChatLoggingProto, ChatLoopGuardProto};
use crate::grpc::testing::{FakeMddbd, scenario};
use crate::session::types::MessageRole;
use crate::webhook::test_support::WebhookSink;

const INTRO: &str = "Backups are taken while the server keeps serving, so nothing stops. ";
const PARAGRAPH: &str = "To restore one, call the restore endpoint with its name and \
    wait until the server reports that it is ready again. ";
const CODE: &str = include_str!("../../../tests/fixtures/loop_guard/code_block.md");

/// The answer the session kept, the last in its history.
fn kept_answer(server: &Harness, session: &str) -> (String, u64) {
    let held = server.state.session_manager.get_session(session).unwrap();
    let last = held.history.last().expect("an answer was kept");
    assert_eq!(last.role, MessageRole::Assistant);
    (last.content.clone(), held.total_tokens_used)
}

#[tokio::test]
async fn an_endless_stream_is_stopped_cancelled_and_reported() {
    let mut sink = WebhookSink::start().await;
    let config = ConfigToml::default().webhook(&sink.url, &["answer", "message.loop_stopped"]);
    let server = Harness::start(config).await;
    five_tool_rounds(&server);
    let dropped = server.llm.stream_forever(INTRO, PARAGRAPH);
    let mut client = server.connect().await;
    let session = server.join(&mut client, "Ada").await;

    ask(&mut client, "How do I restore?").await;
    let (shown, done) = client.recv_answer().await;

    assert!(
        dropped.load(Ordering::SeqCst),
        "the upstream request runs on"
    );
    assert!(shown.ends_with(&format!("\n\n{MARKER}")), "{shown}");
    assert!(
        shown.len() < INTRO.len() + 4 * PARAGRAPH.len(),
        "the visitor saw at most a window of the loop: {} bytes",
        shown.len()
    );

    let (kept, tokens) = kept_answer(&server, &session);
    assert_eq!(kept, format!("{INTRO}{}\n\n{MARKER}", PARAGRAPH.trim_end()));
    assert_eq!(
        tokens, 50,
        "the five tool rounds; the stopped stream adds nothing"
    );

    let events = collect(&mut sink, 2).await;
    let stop = &events["message.loop_stopped"];
    assert_eq!(stop.signature.len(), 64, "signed like every event");
    let data = &stop.body["data"];
    assert_eq!(data["session_id"], session.as_str());
    assert_eq!(data["scenario"], "assistant");
    assert_eq!(data["message_id"], done["message_id"]);
    assert_eq!(data["round"], 5, "the streamed answer after five rounds");
    assert!(data["ratio"].as_f64().unwrap() >= 0.8);
    assert!(data["position"].as_u64().unwrap() > 0);
    assert!(!stop.raw_body.contains("restore"), "no message content");
    assert_eq!(events["answer"].body["data"]["text"], kept.as_str());

    let metrics = server.state.metrics.render();
    assert!(metrics.contains("mddb_chat_loop_guard_triggered_total{scenario=\"assistant\"} 1"));
}

#[tokio::test]
async fn a_looping_answer_from_a_tool_round_is_cut_before_it_is_shown() {
    let server = Harness::start(ConfigToml::default()).await;
    let looping = format!("{INTRO}{}", PARAGRAPH.repeat(30));
    server.llm.answer(&looping, 40, 900);
    let mut client = server.connect().await;
    let session = server.join(&mut client, "Ada").await;

    ask(&mut client, "How do I restore?").await;
    let (shown, _) = client.recv_answer().await;

    let expected = format!("{INTRO}{}\n\n{MARKER}", PARAGRAPH.trim_end());
    assert_eq!(shown, expected, "never more than the text before the loop");
    let (kept, tokens) = kept_answer(&server, &session);
    assert_eq!(kept, expected);
    assert_eq!(tokens, 940, "what the provider reports it used");
}

#[tokio::test]
async fn legitimate_repetition_streams_and_answers_in_full() {
    let server = Harness::start(ConfigToml::default()).await;
    server.llm.answer(CODE, 10, 10);
    five_tool_rounds(&server);
    let chunks = CODE
        .as_bytes()
        .chunks(5)
        .map(|c| Ok(String::from_utf8_lossy(c).into_owned()))
        .collect();
    server.llm.stream(chunks);
    let mut client = server.connect().await;
    server.join(&mut client, "Ada").await;

    ask(&mut client, "Compose file?").await;
    assert_eq!(client.recv_answer().await.0, CODE);
    ask(&mut client, "Again, after searching").await;
    assert_eq!(client.recv_answer().await.0, CODE);
    assert!(!server.state.metrics.render().contains("{scenario="));
}

#[tokio::test]
async fn the_guard_can_be_turned_off_in_the_toml() {
    let config = ConfigToml::default().llm("[llm.loop_guard]\nenabled = false\n");
    let server = Harness::start(config).await;
    let looping = PARAGRAPH.repeat(10);
    server.llm.answer(&looping, 1, 1);
    let mut client = server.connect().await;
    server.join(&mut client, "Ada").await;

    ask(&mut client, "go").await;
    assert_eq!(client.recv_answer().await.0, looping);
}

#[tokio::test]
async fn a_scenario_turns_the_guard_off_and_a_logged_stop_is_flagged() {
    let mddbd = FakeMddbd::start().await;
    let loose = scenario("code", &["docs"]);
    let mut loose = loose;
    loose.llm = Some(ChatLlmProto {
        loop_guard: Some(ChatLoopGuardProto {
            enabled: Some(false),
            ..Default::default()
        }),
        ..Default::default()
    });
    mddbd.put_scenario(loose, "Write code.");
    let mut logged = scenario("assistant", &["docs"]);
    logged.logging = Some(ChatLoggingProto {
        enabled: true,
        retention_days: 1,
        redact: Some(false),
    });
    mddbd.put_scenario(logged, "Be brief.");
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    let looping = PARAGRAPH.repeat(10);

    server.llm.answer(&looping, 1, 1);
    let mut client = server.connect().await;
    server.join_scenario(&mut client, "Ada", "code").await;
    ask(&mut client, "go").await;
    assert_eq!(client.recv_answer().await.0, looping, "not cut in `code`");

    server.llm.answer(&looping, 1, 1);
    let mut client = server.connect().await;
    let session = server.join(&mut client, "Bo").await;
    ask(&mut client, "go").await;
    assert!(client.recv_answer().await.0.ends_with(MARKER));

    let sub = format!("anon:{session}");
    let stored = tokio::time::timeout(std::time::Duration::from_secs(5), async {
        loop {
            let logs = mddbd.conversations_of(&sub);
            if let Some(answer) = logs.first().and_then(|c| c.messages.get(1)) {
                return answer.clone();
            }
            tokio::time::sleep(std::time::Duration::from_millis(5)).await;
        }
    })
    .await
    .expect("the turn is logged");
    assert!(stored.loop_stopped, "the log carries the flag");
    assert!(stored.content.ends_with(MARKER));
}
