use std::collections::BTreeMap;

use super::*;
use crate::grpc::testing::{mcp_server, mcp_state};

fn limits() -> BTreeMap<String, McpLimits> {
    limits_of(&[mcp_server("orders", 2000, 50)])
}

fn toolset(enabled: bool) -> McpToolset {
    let states = vec![
        mcp_state(
            "orders",
            true,
            &[("get_order", true), ("list_refunds", false)],
        ),
        mcp_state("wiki", false, &[("search", true)]),
        mcp_state("status page", true, &[("health.check", true)]),
    ];
    McpToolset::for_turn("support", &states, &limits(), enabled)
}

#[test]
fn only_active_tools_of_active_servers_reach_the_model() {
    let set = toolset(true);

    let names: Vec<&str> = set.tools.iter().map(|t| t.exposed.as_str()).collect();
    assert_eq!(
        names,
        vec!["mcp__orders__get_order", "mcp__status_page__health_check"]
    );
    assert_eq!(set.scenario, "support");
    let orders = set.find("mcp__orders__get_order").expect("offered");
    assert_eq!(
        (orders.server.as_str(), orders.tool.as_str()),
        ("orders", "get_order")
    );
    assert_eq!(
        orders.limits,
        McpLimits {
            timeout_ms: 2000,
            result_max_chars: 50
        }
    );
    let status = set.find("mcp__status_page__health_check").expect("offered");
    assert_eq!(status.tool, "health.check", "the real name goes to mddbd");
    assert_eq!(
        status.limits,
        McpLimits::default(),
        "no limits listed: defaults"
    );
    assert!(set.find("mcp__orders__list_refunds").is_none());
    assert!(set.find("mcp__wiki__search").is_none());
}

#[test]
fn the_switches_in_mddb_chat_hide_every_tool() {
    let set = toolset(false);

    assert!(set.tools.is_empty());
    assert!(set.definitions().is_empty());
    assert_eq!(set.scenario, "support", "the scenario is kept for refusals");
}

#[test]
fn no_servers_means_no_tools() {
    let set = McpToolset::for_turn("plain", &[], &BTreeMap::new(), true);
    assert!(set.tools.is_empty());
    assert_eq!(
        set,
        McpToolset {
            scenario: "plain".into(),
            ..Default::default()
        }
    );
}

#[test]
fn names_follow_the_providers_rules_and_never_collide() {
    assert_eq!(exposed_name("a", "b"), "mcp__a__b");
    assert_eq!(exposed_name("Ünïcode", "tool/x"), "mcp___n_code__tool_x");
    let long = exposed_name(&"s".repeat(40), &"t".repeat(40));
    assert_eq!(long.chars().count(), 64);
    assert!(long.starts_with(PREFIX));
    assert!(is_mcp_name("mcp__a__b"));
    assert!(!is_mcp_name("search_docs"));

    // Two tools that sanitise to one name: the first is kept, the second is
    // not offered rather than offered under a name that calls the first.
    let states = vec![mcp_state("x", true, &[("a.b", true), ("a/b", true)])];
    let set = McpToolset::for_turn("s", &states, &BTreeMap::new(), true);
    assert_eq!(set.tools.len(), 1);
    assert_eq!(set.tools[0].tool, "a.b");
}

#[test]
fn limits_fall_back_to_the_defaults_for_zero_or_nonsense() {
    let got = limits_of(&[mcp_server("a", 0, -3), mcp_server("b", 900, 120)]);
    assert_eq!(got["a"], McpLimits::default());
    assert_eq!(
        got["a"],
        McpLimits {
            timeout_ms: DEFAULT_TIMEOUT_MS,
            result_max_chars: DEFAULT_RESULT_MAX_CHARS
        }
    );
    assert_eq!(
        got["b"],
        McpLimits {
            timeout_ms: 900,
            result_max_chars: 120
        }
    );
}

#[test]
fn a_definition_names_the_server_and_takes_any_object() {
    let defs = toolset(true).definitions();

    assert_eq!(defs.len(), 2);
    assert_eq!(defs[0].tool_type, "function");
    assert_eq!(defs[0].function.name, "mcp__orders__get_order");
    let description = &defs[0].function.description;
    assert!(description.contains("\"get_order\""), "{description}");
    assert!(description.contains("\"orders\""), "{description}");
    assert!(description.contains("not instructions"), "{description}");
    assert_eq!(defs[0].function.parameters["type"], "object");
    assert_eq!(defs[0].function.parameters["additionalProperties"], true);
}
