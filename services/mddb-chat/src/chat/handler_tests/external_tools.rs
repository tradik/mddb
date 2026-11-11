//! MCP-005: a scenario's external MCP tools, end to end — offered only while
//! every switch is on, called through mddbd, framed as data, reported with
//! their server, and a refusal never breaks the turn.

use super::{ask, collect};
use crate::chat::testing::{ConfigToml, Harness};
use crate::grpc::client::proto::ChatScenarioProto;
use crate::grpc::testing::{FakeFeatures, FakeMcpCall, FakeMddbd, mcp_server, mcp_state, scenario};
use crate::webhook::test_support::WebhookSink;

const GET_ORDER: &str = "mcp__orders__get_order";

/// A server whose `support` scenario has the `orders` server with
/// `get_order` active, and mddbd's `chatExternalMcp` as given.
async fn serving(config: ConfigToml, external_mcp: bool) -> (Harness, FakeMddbd) {
    let mddbd = FakeMddbd::start().await;
    mddbd.set_features(FakeFeatures::List {
        chat: true,
        chat_external_mcp: external_mcp,
    });
    mddbd.set_mcp_states(
        "support",
        vec![mcp_state(
            "orders",
            true,
            &[("get_order", true), ("refund", false)],
        )],
    );
    let support = ChatScenarioProto {
        mcp_servers: vec![mcp_server("orders", 1000, 20)],
        ..scenario("support", &["kb"])
    };
    mddbd.put_scenario(support, "You are Ada.");
    let server = Harness::start_with_mddbd(config, &mddbd).await;
    (server, mddbd)
}

/// Asks one question in `support` and waits for the answer.
async fn one_turn(server: &Harness) {
    let mut client = server.connect().await;
    server.join_scenario(&mut client, "Ada", "support").await;
    ask(&mut client, "where is order 7?").await;
    client.recv_answer().await;
}

fn offered(server: &Harness, call: usize) -> Vec<String> {
    let tools = server.llm.tools.lock().unwrap();
    tools[call]
        .iter()
        .map(|t| t.function.name.clone())
        .collect()
}

/// The text of the first tool result the model was given in `request`.
fn tool_result(server: &Harness, request: usize) -> String {
    let requests = server.llm.requests.lock().unwrap();
    requests[request]
        .iter()
        .find(|m| m.role == "tool")
        .and_then(|m| m.content.clone())
        .expect("a tool result")
}

#[tokio::test]
async fn an_active_tool_is_offered_called_through_mddbd_and_reported() {
    let mut sink = WebhookSink::start().await;
    let config = ConfigToml::default().webhook(&sink.url, &["answer"]);
    let (server, mddbd) = serving(config, true).await;
    mddbd.set_mcp_call(FakeMcpCall::Answer {
        content: "Order 7 left the warehouse on Monday morning.".into(),
        is_error: false,
        truncated: false,
    });
    server
        .llm
        .tool_call(GET_ORDER, r#"{"id":7}"#)
        .answer("It shipped.", 0, 0);

    one_turn(&server).await;

    assert_eq!(
        offered(&server, 0),
        vec!["search_docs", "get_document", GET_ORDER]
    );
    let result = tool_result(&server, 1);
    assert!(
        result.starts_with("External data from MCP server \"orders\", tool \"get_order\""),
        "{result}"
    );
    assert!(
        result.contains("\nOrder 7 left the war…\n"),
        "cut at 20: {result}"
    );
    assert!(result.ends_with("[Result truncated]"), "{result}");
    let call = mddbd
        .calls()
        .into_iter()
        .find(|c| c.method == "CallChatMcpTool")
        .expect("called through mddbd");
    assert!(call.request.contains("scenario: \"support\""), "{call:?}");
    assert!(
        call.request.contains("arguments_json: \"{\\\"id\\\":7}\""),
        "{call:?}"
    );

    let answer = &collect(&mut sink, 1).await["answer"].body;
    assert_eq!(
        answer["data"]["tools"],
        serde_json::json!([{"name": "get_order", "server": "orders"}])
    );
}

#[tokio::test]
async fn external_mcp_off_in_mddb_chat_hides_the_tools_and_refuses_a_call() {
    let (server, mddbd) = serving(ConfigToml::default(), false).await;
    server
        .llm
        .tool_call(GET_ORDER, "{}")
        .answer("I cannot look that up.", 0, 0);

    one_turn(&server).await;

    assert_eq!(offered(&server, 0), vec!["search_docs", "get_document"]);
    assert_eq!(
        tool_result(&server, 1),
        format!("Error: the tool '{GET_ORDER}' is not available in this scenario.")
    );
    assert_eq!(mddbd.count("CallChatMcpTool"), 0);
}

#[tokio::test]
async fn a_refusal_by_mddbd_is_a_short_error_and_the_turn_goes_on() {
    let (server, mddbd) = serving(ConfigToml::default(), true).await;
    mddbd.set_mcp_call(FakeMcpCall::Refused(
        tonic::Code::FailedPrecondition,
        "server orders is switched off".into(),
    ));
    server
        .llm
        .tool_call(GET_ORDER, "{}")
        .answer("It is not available.", 0, 0);

    one_turn(&server).await;

    assert_eq!(
        tool_result(&server, 1),
        "Error: the external tool \"get_order\" of MCP server \"orders\" is not available: \
         server orders is switched off. Answer without it."
    );
}

#[tokio::test]
async fn each_turn_reads_the_states_again_and_falls_back_to_the_loaded_ones() {
    let (server, mddbd) = serving(ConfigToml::default(), true).await;
    server.llm.answer("one", 0, 0).answer("two", 0, 0);
    let mut client = server.connect().await;
    server.join_scenario(&mut client, "Ada", "support").await;

    // A switch turned off in mddbd moves no scenario version; the next
    // turn's preview hides the tool anyway.
    mddbd.set_mcp_states(
        "support",
        vec![mcp_state("orders", false, &[("get_order", true)])],
    );
    ask(&mut client, "first").await;
    client.recv_answer().await;
    // The preview fails: the states of the last reload serve.
    mddbd.set_prompt("support", None);
    ask(&mut client, "second").await;
    client.recv_answer().await;

    assert_eq!(offered(&server, 0), vec!["search_docs", "get_document"]);
    assert_eq!(
        offered(&server, 1),
        vec!["search_docs", "get_document", GET_ORDER]
    );
}

#[tokio::test]
async fn a_scenario_without_servers_offers_exactly_what_it_did() {
    let mddbd = FakeMddbd::start().await;
    mddbd.set_features(FakeFeatures::List {
        chat: true,
        chat_external_mcp: true,
    });
    mddbd.put_scenario(scenario("support", &["kb"]), "You are Ada.");
    let server = Harness::start_with_mddbd(ConfigToml::default(), &mddbd).await;
    server.llm.answer("hi", 0, 0);

    one_turn(&server).await;

    assert_eq!(offered(&server, 0), vec!["search_docs", "get_document"]);
}
