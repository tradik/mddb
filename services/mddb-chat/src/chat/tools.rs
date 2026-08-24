use regex::Regex;
use serde::Deserialize;
use tracing::debug;

use crate::error::AppError;
use crate::grpc::client::MddbClient;
use crate::llm::provider::{ToolCall, ToolDef, ToolFunction};

/// Strip markdown formatting to give LLM clean plain text
fn strip_markdown(input: &str) -> String {
    let mut s = input.to_string();
    // Remove code fences (```lang ... ```)
    let code_fence = Regex::new(r"(?s)```[a-zA-Z]*\n?.*?```").unwrap();
    s = code_fence
        .replace_all(&s, "[code block removed]")
        .to_string();
    // Remove inline code
    let inline_code = Regex::new(r"`([^`]+)`").unwrap();
    s = inline_code.replace_all(&s, "$1").to_string();
    // Remove images ![alt](url)
    let images = Regex::new(r"!\[([^\]]*)\]\([^)]+\)").unwrap();
    s = images.replace_all(&s, "$1").to_string();
    // Convert links [text](url) → text
    let links = Regex::new(r"\[([^\]]+)\]\([^)]+\)").unwrap();
    s = links.replace_all(&s, "$1").to_string();
    // Remove HTML tags
    let html = Regex::new(r"<[^>]+>").unwrap();
    s = html.replace_all(&s, "").to_string();
    // Remove headers (# ... )
    let headers = Regex::new(r"(?m)^#{1,6}\s+").unwrap();
    s = headers.replace_all(&s, "").to_string();
    // Remove bold/italic markers
    s = s.replace("**", "").replace("__", "");
    s = s.replace('*', "").replace('_', " ");
    // Remove horizontal rules
    let hr = Regex::new(r"(?m)^-{3,}$|^={3,}$|^\*{3,}$").unwrap();
    s = hr.replace_all(&s, "").to_string();
    // Remove blockquote markers
    let bq = Regex::new(r"(?m)^>\s?").unwrap();
    s = bq.replace_all(&s, "").to_string();
    // Remove list markers
    let list = Regex::new(r"(?m)^[\s]*[-*+]\s").unwrap();
    s = list.replace_all(&s, "").to_string();
    // Collapse multiple blank lines
    let blanks = Regex::new(r"\n{3,}").unwrap();
    s = blanks.replace_all(&s, "\n\n").to_string();
    s.trim().to_string()
}

/// Define the tools available to the LLM
pub fn tool_definitions() -> Vec<ToolDef> {
    vec![
        ToolDef {
            tool_type: "function".to_string(),
            function: ToolFunction {
                name: "search_docs".to_string(),
                description: "Search the documentation database for relevant information. Use this when the user asks a question about MDDB features, configuration, API, or usage.".to_string(),
                parameters: serde_json::json!({
                    "type": "object",
                    "properties": {
                        "query": {
                            "type": "string",
                            "description": "The search query to find relevant documentation"
                        },
                        "collection": {
                            "type": "string",
                            "description": "The collection to search in (default: docs)",
                            "default": "docs"
                        }
                    },
                    "required": ["query"]
                }),
            },
        },
        ToolDef {
            tool_type: "function".to_string(),
            function: ToolFunction {
                name: "get_document".to_string(),
                description: "Get a specific document by its key from the database. Use this when you know the exact document key and need its full content.".to_string(),
                parameters: serde_json::json!({
                    "type": "object",
                    "properties": {
                        "key": {
                            "type": "string",
                            "description": "The document key (e.g. 'api', 'quickstart', 'grpc')"
                        },
                        "collection": {
                            "type": "string",
                            "description": "The collection name (default: docs)",
                            "default": "docs"
                        }
                    },
                    "required": ["key"]
                }),
            },
        },
    ]
}

#[derive(Deserialize)]
struct SearchArgs {
    query: String,
    collection: Option<String>,
}

#[derive(Deserialize)]
struct GetDocArgs {
    key: String,
    collection: Option<String>,
}

/// Execute a tool call and return the result as a string
pub async fn execute_tool(
    client: &mut MddbClient,
    tool_call: &ToolCall,
    default_collection: &str,
) -> Result<String, AppError> {
    debug!(
        tool = tool_call.function.name,
        args = tool_call.function.arguments,
        "executing tool"
    );

    match tool_call.function.name.as_str() {
        "search_docs" => {
            let args: SearchArgs = serde_json::from_str(&tool_call.function.arguments)
                .map_err(|e| AppError::LlmError(format!("invalid tool args: {e}")))?;

            let collection = args.collection.as_deref().unwrap_or(default_collection);
            let results = client.search(&args.query, collection).await?;

            if results.is_empty() {
                return Ok("No results found for this query.".to_string());
            }

            let mut output = String::new();
            for (i, r) in results.iter().enumerate() {
                let clean = strip_markdown(&r.content);
                let truncated = if clean.len() > 800 {
                    format!("{}...", &clean[..800])
                } else {
                    clean
                };
                output.push_str(&format!(
                    "Result {} (key: {}, score: {:.2})\n{}\n\n---\n\n",
                    i + 1,
                    r.key,
                    r.score,
                    truncated,
                ));
            }
            Ok(output)
        }
        "get_document" => {
            let args: GetDocArgs = serde_json::from_str(&tool_call.function.arguments)
                .map_err(|e| AppError::LlmError(format!("invalid tool args: {e}")))?;

            let collection = args.collection.as_deref().unwrap_or(default_collection);
            let results = client.search(&args.key, collection).await?;

            // Try to find exact match
            if let Some(doc) = results.iter().find(|r| r.key == args.key) {
                let clean = strip_markdown(&doc.content);
                let content = if clean.len() > 4000 {
                    format!("{}...\n\n[Document truncated]", &clean[..4000])
                } else {
                    clean
                };
                Ok(format!("Document: {}\n\n{}", doc.key, content))
            } else if !results.is_empty() {
                // Return best match
                let doc = &results[0];
                let clean = strip_markdown(&doc.content);
                let content = if clean.len() > 4000 {
                    format!("{}...\n\n[Document truncated]", &clean[..4000])
                } else {
                    clean
                };
                Ok(format!(
                    "Document '{}' not found. Closest match: {}\n\n{}",
                    args.key, doc.key, content
                ))
            } else {
                Ok(format!(
                    "Document '{}' not found in collection '{}'.",
                    args.key, collection
                ))
            }
        }
        other => Ok(format!("Unknown tool: {other}")),
    }
}
