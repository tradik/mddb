use super::{DOCUMENT_MAX_CHARS, DocSearch, ToolPolicy, execute_tool};
use crate::chat::markdown::ELLIPSIS;
use crate::config::Config;
use crate::error::AppError;
use crate::grpc::client::SearchResult;
use crate::llm::provider::{ToolCall, ToolCallFunction};

/// Stands in for mddbd: hands back canned results and remembers what it was
/// asked.
#[derive(Default)]
struct FakeSearch {
    results: Vec<SearchResult>,
    calls: Vec<(String, String)>,
    fail: bool,
}

#[async_trait::async_trait]
impl DocSearch for FakeSearch {
    async fn search(
        &mut self,
        query: &str,
        collection: &str,
    ) -> Result<Vec<SearchResult>, AppError> {
        self.calls.push((query.to_string(), collection.to_string()));
        if self.fail {
            return Err(AppError::Internal("mddbd is down".into()));
        }
        Ok(self.results.clone())
    }
}

fn hit(key: &str, content: &str) -> SearchResult {
    SearchResult {
        key: key.into(),
        content: content.into(),
        score: 0.5,
    }
}

fn call(name: &str, args: &str) -> ToolCall {
    ToolCall {
        id: "call_1".into(),
        function: ToolCallFunction {
            name: name.into(),
            arguments: args.into(),
        },
    }
}

fn policy(default: &str, result_max_chars: usize) -> ToolPolicy {
    ToolPolicy {
        default_collection: default.into(),
        result_max_chars,
    }
}

fn config(body: &str) -> Config {
    toml::from_str(body).expect("the fixture should parse")
}

const BASE: &str = r#"
[server]
[mddb]
default_collection = "docs"
result_max_chars = 300
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
"#;

#[test]
fn the_policy_takes_the_collection_and_the_limit_from_the_config() {
    let cfg = config(&format!(
        "{BASE}\n[scenarios.support]\nname = \"s\"\nsystem_prompt = \"p\"\nallowed_collections = [\"faq\", \"docs\"]\n"
    ));
    assert_eq!(
        ToolPolicy::for_scenario(&cfg, "support"),
        policy("faq", 300)
    );
    assert_eq!(
        ToolPolicy::for_scenario(&cfg, "missing"),
        policy("docs", 300)
    );
}

#[tokio::test]
async fn search_without_a_collection_uses_the_default() {
    let mut db = FakeSearch {
        results: vec![hit("quickstart", "Start here.")],
        ..Default::default()
    };
    let got = execute_tool(
        &mut db,
        &call("search_docs", r#"{"query":"start"}"#),
        &policy("docs", 800),
    )
    .await
    .unwrap();

    assert_eq!(db.calls, vec![("start".to_string(), "docs".to_string())]);
    assert!(got.starts_with("Result 1 (key: quickstart, score: 0.50)\nStart here.\n"));
}

#[tokio::test]
async fn search_in_a_named_collection_reports_no_hits() {
    let mut db = FakeSearch::default();
    let got = execute_tool(
        &mut db,
        &call("search_docs", r#"{"query":"refund","collection":"faq"}"#),
        &policy("docs", 800),
    )
    .await
    .unwrap();

    assert_eq!(db.calls, vec![("refund".to_string(), "faq".to_string())]);
    assert_eq!(got, "No results found for this query.");
}

// #398: whole chunks when they fit, code kept, the limit from the config.

#[tokio::test]
async fn a_chunk_within_the_limit_arrives_whole_with_its_code_block() {
    let content =
        "Index a folder:\n\n```sh\nmddb-cli autoindex --source ./docs\n```\n\nThat is **all**.";
    let mut db = FakeSearch {
        results: vec![hit("cli", content)],
        ..Default::default()
    };
    let got = execute_tool(
        &mut db,
        &call("search_docs", r#"{"query":"index"}"#),
        &policy("docs", 800),
    )
    .await
    .unwrap();

    assert_eq!(
        got,
        "Result 1 (key: cli, score: 0.50)\nIndex a folder:\n\n```sh\nmddb-cli autoindex --source ./docs\n```\n\nThat is all.\n\n---\n\n"
    );
}

#[tokio::test]
async fn a_chunk_over_the_limit_is_cut_on_a_character_with_a_marker() {
    let content = "ż".repeat(1000);
    let mut db = FakeSearch {
        results: vec![hit("pl", &content), hit("pl2", "krótki")],
        ..Default::default()
    };
    let got = execute_tool(
        &mut db,
        &call("search_docs", r#"{"query":"ż"}"#),
        &policy("docs", 10),
    )
    .await
    .unwrap();

    let expected_first = format!(
        "Result 1 (key: pl, score: 0.50)\n{}{ELLIPSIS}\n\n---\n\n",
        "ż".repeat(10)
    );
    assert!(got.starts_with(&expected_first), "got: {got}");
    assert!(got.contains("Result 2 (key: pl2, score: 0.50)\nkrótki\n"));
}

#[tokio::test]
async fn a_zero_limit_returns_the_whole_chunk() {
    let content = "x".repeat(5000);
    let mut db = FakeSearch {
        results: vec![hit("long", &content)],
        ..Default::default()
    };
    let got = execute_tool(
        &mut db,
        &call("search_docs", r#"{"query":"x"}"#),
        &policy("docs", 0),
    )
    .await
    .unwrap();

    assert!(got.contains(&content));
    assert!(!got.contains(ELLIPSIS));
}

#[tokio::test]
async fn get_document_returns_the_exact_key_when_present() {
    let mut db = FakeSearch {
        results: vec![
            hit("api-guide", "close"),
            hit("api", "# API\n\nUse `GET /v1/docs`."),
        ],
        ..Default::default()
    };
    let got = execute_tool(
        &mut db,
        &call("get_document", r#"{"key":"api"}"#),
        &policy("docs", 800),
    )
    .await
    .unwrap();

    assert_eq!(db.calls, vec![("api".to_string(), "docs".to_string())]);
    assert_eq!(got, "Document: api\n\nAPI\n\nUse `GET /v1/docs`.");
}

#[tokio::test]
async fn get_document_falls_back_to_the_closest_match() {
    let mut db = FakeSearch {
        results: vec![hit("api-guide", "Guide body.")],
        ..Default::default()
    };
    let got = execute_tool(
        &mut db,
        &call("get_document", r#"{"key":"api","collection":"docs"}"#),
        &policy("docs", 800),
    )
    .await
    .unwrap();

    assert_eq!(
        got,
        "Document 'api' not found. Closest match: api-guide\n\nGuide body."
    );
}

#[tokio::test]
async fn get_document_reports_a_miss() {
    let mut db = FakeSearch::default();
    let got = execute_tool(
        &mut db,
        &call("get_document", r#"{"key":"nope"}"#),
        &policy("docs", 800),
    )
    .await
    .unwrap();

    assert_eq!(got, "Document 'nope' not found in collection 'docs'.");
}

#[tokio::test]
async fn a_long_document_is_cut_on_a_character_and_marked() {
    let content = "ę".repeat(DOCUMENT_MAX_CHARS + 50);
    let mut db = FakeSearch {
        results: vec![hit("big", &content)],
        ..Default::default()
    };
    let got = execute_tool(
        &mut db,
        &call("get_document", r#"{"key":"big"}"#),
        &policy("docs", 800),
    )
    .await
    .unwrap();

    let expected = format!(
        "Document: big\n\n{}{ELLIPSIS}\n\n[Document truncated]",
        "ę".repeat(DOCUMENT_MAX_CHARS)
    );
    assert_eq!(got, expected);
}

#[tokio::test]
async fn malformed_arguments_are_an_llm_error() {
    let mut db = FakeSearch::default();
    let err = execute_tool(
        &mut db,
        &call("search_docs", "not json"),
        &policy("docs", 800),
    )
    .await
    .unwrap_err();
    assert!(matches!(err, AppError::LlmError(m) if m.starts_with("invalid tool args")));

    let err = execute_tool(&mut db, &call("get_document", "{}"), &policy("docs", 800))
        .await
        .unwrap_err();
    assert!(matches!(err, AppError::LlmError(_)));
}

#[tokio::test]
async fn an_unknown_tool_is_named_back_to_the_model() {
    let mut db = FakeSearch::default();
    let got = execute_tool(&mut db, &call("delete_all", "{}"), &policy("docs", 800))
        .await
        .unwrap();
    assert_eq!(got, "Unknown tool: delete_all");
}

#[tokio::test]
async fn a_database_failure_propagates() {
    let mut db = FakeSearch {
        fail: true,
        ..Default::default()
    };
    let err = execute_tool(
        &mut db,
        &call("search_docs", r#"{"query":"q"}"#),
        &policy("docs", 800),
    )
    .await
    .unwrap_err();
    assert!(matches!(err, AppError::Internal(_)));
}
