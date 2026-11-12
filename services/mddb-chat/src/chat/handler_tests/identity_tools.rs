//! #414: a server that forwards the user's identity, end to end — its tools
//! are offered only to a signed-in session, and the call carries that
//! session's verified `sub`, whatever the model puts in the arguments.

use serde_json::json;

use super::ask;
use super::signed::{SECRET, token};
use crate::chat::testing::{ConfigToml, Harness};
use crate::grpc::client::proto::{ChatMcpServerStateProto, ChatScenarioProto};
use crate::grpc::testing::{FakeFeatures, FakeMcpCall, FakeMddbd, mcp_server, mcp_state, scenario};

const GET_ORDER: &str = "mcp__orders__get_order";

/// A server whose `support` scenario has `orders`, which forwards the
/// user's identity, and joins that accept signed tokens.
async fn serving() -> (Harness, FakeMddbd) {
    let mddbd = FakeMddbd::start().await;
    mddbd.set_features(FakeFeatures::List {
        chat: true,
        chat_external_mcp: true,
    });
    mddbd.set_mcp_states(
        "support",
        vec![ChatMcpServerStateProto {
            forward_identity: true,
            ..mcp_state("orders", true, &[("get_order", true)])
        }],
    );
    let support = ChatScenarioProto {
        mcp_servers: vec![mcp_server("orders", 1000, 200)],
        ..scenario("support", &["kb"])
    };
    mddbd.put_scenario(support, "You are Ada.");
    mddbd.set_mcp_call(FakeMcpCall::Answer {
        content: "Your order 7 shipped.".into(),
        is_error: false,
        truncated: false,
    });
    let config = ConfigToml::default().identity(SECRET, false);
    let server = Harness::start_with_mddbd(config, &mddbd).await;
    server
        .llm
        .tool_call(GET_ORDER, r#"{"id":7,"sub":"someone-else"}"#)
        .answer("It shipped.", 0, 0);
    (server, mddbd)
}

fn offered(server: &Harness) -> Vec<String> {
    let tools = server.llm.tools.lock().unwrap();
    tools[0].iter().map(|t| t.function.name.clone()).collect()
}

#[tokio::test]
async fn a_signed_session_is_offered_the_tools_and_forwards_its_own_sub() {
    let (server, mddbd) = serving().await;
    let mut client = server.connect().await;
    client
        .send_json(
            json!({"type": "join", "name": "Visitor", "scenario": "support",
            "token": token("user-42", "support")}),
        )
        .await;
    assert_eq!(client.recv_json().await["type"], "session");

    ask(&mut client, "where is my order 7?").await;
    client.recv_answer().await;

    assert!(offered(&server).contains(&GET_ORDER.to_string()));
    let call = mddbd
        .calls()
        .into_iter()
        .find(|c| c.method == "CallChatMcpTool")
        .expect("called through mddbd");
    assert!(call.request.contains("sub: \"user-42\""), "{call:?}");
    assert!(!call.request.contains("sub: \"someone-else\""), "{call:?}");
}

#[tokio::test]
async fn an_anonymous_session_is_not_offered_the_tools_and_cannot_call_them() {
    let (server, mddbd) = serving().await;
    let mut client = server.connect().await;
    server.join_scenario(&mut client, "Ada", "support").await;

    ask(&mut client, "where is order 7?").await;
    client.recv_answer().await;

    assert_eq!(offered(&server), vec!["search_docs", "get_document"]);
    assert_eq!(mddbd.count("CallChatMcpTool"), 0);
}
