//! MCP-005: external tools next to the built-in ones, refused locally when the
//! turn does not offer them.

use std::collections::BTreeMap;

use super::tests::{FakeSearch, call, policy};
use super::{execute_tool, tool_definitions};
use crate::chat::mcp::McpToolset;
use crate::grpc::testing::mcp_state;

fn with_orders(enabled: bool) -> super::ToolPolicy {
    let states = vec![mcp_state("orders", true, &[("get_order", true)])];
    policy(&["docs"], 800).with_mcp(McpToolset::for_turn(
        "support",
        &states,
        &BTreeMap::new(),
        enabled,
        None,
    ))
}

#[test]
fn offered_external_tools_follow_the_built_in_ones() {
    let names = |p| {
        tool_definitions(&p)
            .iter()
            .map(|d| d.function.name.clone())
            .collect::<Vec<_>>()
    };

    assert_eq!(
        names(with_orders(true)),
        vec!["search_docs", "get_document", "mcp__orders__get_order"]
    );
    assert_eq!(
        names(with_orders(false)),
        vec!["search_docs", "get_document"]
    );
}

#[tokio::test]
async fn an_offered_tool_is_called_through_mddbd() {
    // FakeSearch knows only searching: the trait's default answers the call,
    // which proves the call went to the client and not to a search.
    let mut db = FakeSearch::default();

    let got = execute_tool(
        &mut db,
        &call("mcp__orders__get_order", r#"{"id":7}"#),
        &with_orders(true),
    )
    .await
    .unwrap();

    assert_eq!(
        got,
        "Error: the external tool \"get_order\" of MCP server \"orders\" is not available: \
         the call failed. Answer without it."
    );
    assert!(db.calls.is_empty(), "no search was made");
}

#[tokio::test]
async fn a_tool_the_turn_does_not_offer_is_refused_before_mddbd() {
    for (name, policy) in [
        ("mcp__orders__get_order", with_orders(false)),
        ("mcp__orders__delete_order", with_orders(true)),
    ] {
        let mut db = FakeSearch::default();
        let got = execute_tool(&mut db, &call(name, "{}"), &policy)
            .await
            .unwrap();
        assert_eq!(
            got,
            format!("Error: the tool '{name}' is not available in this scenario.")
        );
    }
}
