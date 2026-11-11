use super::*;

#[test]
fn only_user_and_assistant_turns_are_restored() {
    let stored = |role: &str, content: &str| proto::ChatMessageProto {
        role: role.to_string(),
        content: content.to_string(),
        created_at: 5,
        ..Default::default()
    };
    let (history, entries) = restore(&[
        stored("user", "q"),
        stored("tool", "raw search output"),
        stored("assistant", "a"),
    ]);

    assert_eq!(history.len(), 2);
    assert!(history.iter().all(|m| m.restored && m.timestamp == 5));
    assert_eq!(history[1].role, MessageRole::Assistant);
    let roles: Vec<&str> = entries.iter().map(|e| e.role.as_str()).collect();
    assert_eq!(roles, vec!["user", "assistant"]);
}

#[test]
fn not_found_becomes_the_clients_error_and_the_rest_pass_through() {
    assert!(matches!(
        store_error(AppError::GrpcError(tonic::Status::not_found("x"))),
        AppError::ConversationNotFound
    ));
    assert!(matches!(
        store_error(AppError::MddbUnavailable),
        AppError::MddbUnavailable
    ));
}

#[test]
fn an_external_tool_is_stored_with_its_server_in_the_name() {
    let llm: crate::config::LlmConfig =
        toml::from_str("provider = \"openai\"\napi_url = \"http://localhost\"\nmodel = \"m\"")
            .expect("a minimal llm config parses");
    let tools = [
        ToolCallRecord {
            name: "search_docs".into(),
            collection: "docs".into(),
            server: None,
        },
        ToolCallRecord {
            name: "get_order".into(),
            collection: String::new(),
            server: Some("orders".into()),
        },
    ];

    let stored = answer_message("a", &tools, &llm, TokenUsage::default());

    let names: Vec<(&str, &str)> = stored
        .tool_calls
        .iter()
        .map(|t| (t.name.as_str(), t.collection.as_str()))
        .collect();
    assert_eq!(
        names,
        vec![("search_docs", "docs"), ("mcp:orders/get_order", "")]
    );
}
