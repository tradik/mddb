use super::{DocSearch, ToolPolicy, execute_tool, tool_definitions};
use crate::chat::markdown::ELLIPSIS;
use crate::error::AppError;
use crate::grpc::client::SearchResult;
use crate::llm::provider::{ToolCall, ToolCallFunction};

/// Stands in for mddbd: hands back canned results and remembers what it was
/// asked, so a test can prove a refused call never reached the database.
#[derive(Default)]
pub(super) struct FakeSearch {
    pub(super) results: Vec<SearchResult>,
    pub(super) calls: Vec<(String, String)>,
    pub(super) fail: bool,
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

pub(super) fn hit(key: &str, content: &str) -> SearchResult {
    SearchResult {
        key: key.into(),
        content: content.into(),
        score: 0.5,
    }
}

pub(super) fn call(name: &str, args: &str) -> ToolCall {
    ToolCall {
        id: "call_1".into(),
        function: ToolCallFunction {
            name: name.into(),
            arguments: args.into(),
        },
    }
}

pub(super) fn policy(allowed: &[&str], result_max_chars: usize) -> ToolPolicy {
    ToolPolicy {
        default_collection: allowed[0].into(),
        allowed_collections: allowed.iter().map(|c| c.to_string()).collect(),
        result_max_chars,
        mcp: Default::default(),
    }
}

// #396: the schema tells the model what it may ask for; execute_tool refuses
// the rest before anything reaches the database.

#[test]
fn the_schema_advertises_the_allowed_collections_as_an_enum() {
    let defs = tool_definitions(&policy(&["docs", "faq"], 800));
    let names: Vec<&str> = defs.iter().map(|d| d.function.name.as_str()).collect();
    assert_eq!(names, vec!["search_docs", "get_document"]);

    for def in &defs {
        let collection = &def.function.parameters["properties"]["collection"];
        assert_eq!(collection["enum"], serde_json::json!(["docs", "faq"]));
        assert_eq!(collection["default"], serde_json::json!("docs"));
        assert_eq!(def.tool_type, "function");
    }
    assert_eq!(
        defs[0].function.parameters["required"],
        serde_json::json!(["query"])
    );
    assert_eq!(
        defs[1].function.parameters["required"],
        serde_json::json!(["key"])
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
        &policy(&["docs", "faq"], 800),
    )
    .await
    .unwrap();

    assert_eq!(db.calls, vec![("start".to_string(), "docs".to_string())]);
    assert!(got.starts_with("Result 1 (key: quickstart, score: 0.50)\nStart here.\n"));
}

#[tokio::test]
async fn search_in_an_allowed_collection_is_served() {
    let mut db = FakeSearch::default();
    let got = execute_tool(
        &mut db,
        &call("search_docs", r#"{"query":"refund","collection":"faq"}"#),
        &policy(&["docs", "faq"], 800),
    )
    .await
    .unwrap();

    assert_eq!(db.calls, vec![("refund".to_string(), "faq".to_string())]);
    assert_eq!(got, "No results found for this query.");
}

#[tokio::test]
async fn search_outside_the_allowed_collections_is_refused_before_the_database() {
    let mut db = FakeSearch::default();
    let got = execute_tool(
        &mut db,
        &call("search_docs", r#"{"query":"salaries","collection":"hr"}"#),
        &policy(&["docs", "faq"], 800),
    )
    .await
    .unwrap();

    assert!(db.calls.is_empty(), "the refused call reached the database");
    assert_eq!(
        got,
        "Error: collection 'hr' is not available in this scenario. Available collections: docs, faq."
    );
}

#[tokio::test]
async fn get_document_outside_the_allowed_collections_is_refused_too() {
    let mut db = FakeSearch::default();
    let got = execute_tool(
        &mut db,
        &call("get_document", r#"{"key":"api","collection":"secrets"}"#),
        &policy(&["docs"], 800),
    )
    .await
    .unwrap();

    assert!(db.calls.is_empty());
    assert_eq!(
        got,
        "Error: collection 'secrets' is not available in this scenario. Available collections: docs."
    );
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
        &policy(&["docs"], 800),
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
        &policy(&["docs"], 10),
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
        &policy(&["docs"], 0),
    )
    .await
    .unwrap();

    assert!(got.contains(&content));
    assert!(!got.contains(ELLIPSIS));
}
