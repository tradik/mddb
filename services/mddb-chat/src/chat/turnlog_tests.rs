use super::*;
use crate::llm::provider::{ToolCall, ToolCallFunction};

fn run(query: &str, keys: &[&str], score: f64) -> LoggedRun {
    LoggedRun::of(
        &ToolRun {
            output: "text".into(),
            query: query.into(),
            result_keys: keys.iter().map(|k| k.to_string()).collect(),
            top_score: score,
        },
        12,
    )
}

#[test]
fn the_policy_is_off_and_redacting_unless_mddbd_says_otherwise() {
    assert_eq!(
        LogPolicy::from_proto(None),
        LogPolicy {
            enabled: false,
            retention_days: 0,
            redact: true
        }
    );
    let on = LogPolicy::from_proto(Some(proto::ChatLoggingProto {
        enabled: true,
        retention_days: 14,
        redact: None,
    }));
    assert_eq!((on.enabled, on.retention_days, on.redact), (true, 14, true));
    let off = LogPolicy::from_proto(Some(proto::ChatLoggingProto {
        enabled: true,
        retention_days: -3,
        redact: Some(false),
    }));
    assert_eq!((off.retention_days, off.redact), (0, false));
}

#[test]
fn a_turn_that_searched_and_found_nothing_has_no_results() {
    assert_eq!(
        outcome(&[]),
        ANSWERED,
        "no search: answered from the prompt"
    );
    assert_eq!(
        outcome(&[run("", &[], 0.0)]),
        ANSWERED,
        "a refused call searched nothing"
    );
    assert_eq!(outcome(&[run("q", &[], 0.0)]), NO_RESULTS);
    assert_eq!(
        outcome(&[run("q", &[], 0.0), run("r", &["k"], 0.5)]),
        ANSWERED
    );
    assert_eq!(
        outcome(&[run("q", &["k"], 0.5), run("r", &[], 0.0)]),
        ANSWERED
    );
}

#[test]
fn tool_calls_pair_each_name_with_what_it_found() {
    let tools = vec![
        ToolCallRecord {
            name: "search_docs".into(),
            collection: "docs".into(),
            server: None,
        },
        ToolCallRecord {
            name: "get_document".into(),
            collection: "kb".into(),
            server: None,
        },
    ];
    let calls = tool_calls(&tools, &[run("q", &["a", "b"], 0.7), run("k", &["k"], 0.9)]);
    assert_eq!(calls.len(), 2);
    assert_eq!(
        (
            calls[1].name.as_str(),
            calls[1].collection.as_str(),
            calls[1].query.as_str()
        ),
        ("get_document", "kb", "k")
    );
    assert_eq!(calls[0].result_keys, vec!["a", "b"]);
    assert_eq!((calls[0].duration_ms, calls[0].top_score), (12, 0.7));
}

#[test]
fn an_answer_is_stored_under_the_clients_id_with_details_only_when_logged() {
    let mut trace = TurnTrace::start();
    let call = ToolCall {
        id: "c".into(),
        function: ToolCallFunction {
            name: "search_docs".into(),
            arguments: r#"{"query":"q"}"#.into(),
        },
    };
    trace.record_tool(&call, "docs");
    trace.record_run(
        &ToolRun {
            query: "q".into(),
            ..ToolRun::default()
        },
        3,
    );
    let llm: LlmConfig =
        toml::from_str("provider = \"p\"\napi_url = \"http://x\"\nmodel = \"m\"").unwrap();
    let spent = TokenUsage {
        input: 5,
        output: 2,
    };

    let plain = answer(&trace, "text", (&llm, spent), None);
    assert_eq!(plain.id, trace.answer_id);
    assert_eq!((plain.outcome.as_str(), plain.latency_ms), ("", 0));
    assert_eq!(plain.tool_calls[0].query, "", "no details without the log");

    let logged = answer(&trace, "text", (&llm, spent), Some(NO_RESULTS));
    assert_eq!(logged.outcome, NO_RESULTS);
    assert_eq!(logged.tool_calls[0].query, "q");
    assert_eq!((logged.prompt_tokens, logged.completion_tokens), (5, 2));
    assert_eq!(logged.model, "m");
}
