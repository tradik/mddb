//! The tools the model may call: `search_docs` and `get_document`, and the
//! scenario's external MCP tools (MCP-005), all bounded by
//! the scenario's [`ToolPolicy`].

use std::borrow::Cow;
use std::time::Duration;

use serde::Deserialize;
use tracing::debug;

use crate::chat::markdown::{clip, strip_markdown};
use crate::chat::mcp;
pub use crate::chat::policy::ToolPolicy;
use crate::error::AppError;
use crate::grpc::client::SearchResult;
use crate::grpc::client::proto::{CallChatMcpToolRequest, CallChatMcpToolResponse};
use crate::llm::provider::{ToolCall, ToolDef, ToolFunction};

/// Characters of a whole document `get_document` hands back before cutting.
pub const DOCUMENT_MAX_CHARS: usize = 4000;

/// What the tools need from the database. `MddbClient` is the real one; tests
/// stand in a fake, so a tool's behaviour can be checked without mddbd.
#[async_trait::async_trait]
pub trait DocSearch: Send {
    async fn search(
        &mut self,
        query: &str,
        collection: &str,
    ) -> Result<Vec<SearchResult>, AppError>;

    /// Calls an external MCP tool through mddbd (MCP-005), waiting at most
    /// `deadline`. A client that cannot reach one answers UNIMPLEMENTED,
    /// which the model is told as a short error.
    async fn call_mcp_tool(
        &mut self,
        _request: CallChatMcpToolRequest,
        _deadline: Duration,
    ) -> Result<CallChatMcpToolResponse, AppError> {
        Err(AppError::GrpcError(tonic::Status::unimplemented(
            "no external tools here",
        )))
    }
}

/// Define the tools available to the LLM for one scenario.
pub fn tool_definitions(policy: &ToolPolicy) -> Vec<ToolDef> {
    let tool = |name: &str, description: &str, parameters: serde_json::Value| ToolDef {
        tool_type: "function".to_string(),
        function: ToolFunction {
            name: name.to_string(),
            description: description.to_string(),
            parameters,
        },
    };
    let mut tools = vec![
        tool(
            "search_docs",
            "Search the documentation database for relevant information. Use this when the user asks a question about MDDB features, configuration, API, or usage.",
            serde_json::json!({
                "type": "object",
                "properties": {
                    "query": {
                        "type": "string",
                        "description": "The search query to find relevant documentation"
                    },
                    "collection": policy.collection_schema("The collection to search in"),
                },
                "required": ["query"]
            }),
        ),
        tool(
            "get_document",
            "Get a specific document by its key from the database. Use this when you know the exact document key and need its full content.",
            serde_json::json!({
                "type": "object",
                "properties": {
                    "key": {
                        "type": "string",
                        "description": "The document key (e.g. 'api', 'quickstart', 'grpc')"
                    },
                    "collection": policy.collection_schema("The collection name"),
                },
                "required": ["key"]
            }),
        ),
    ];
    // MCP-005: the scenario's external tools, after the built-in ones.
    tools.extend(policy.mcp.definitions());
    tools
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

fn parse_args<T: for<'de> Deserialize<'de>>(tool_call: &ToolCall) -> Result<T, AppError> {
    serde_json::from_str(&tool_call.function.arguments)
        .map_err(|e| AppError::LlmError(format!("invalid tool args: {e}")))
}

/// Execute a tool call and return the result as a string for the model.
pub async fn execute_tool<S: DocSearch>(
    client: &mut S,
    tool_call: &ToolCall,
    policy: &ToolPolicy,
) -> Result<String, AppError> {
    let tool = tool_call.function.name.as_str();
    debug!(tool, args = tool_call.function.arguments, "executing tool");

    match tool {
        "search_docs" => {
            let args: SearchArgs = parse_args(tool_call)?;
            match policy.resolve(tool, args.collection.as_deref()) {
                Ok(collection) => search_docs(client, &args.query, collection, policy).await,
                Err(refusal) => Ok(refusal),
            }
        }
        "get_document" => {
            let args: GetDocArgs = parse_args(tool_call)?;
            match policy.resolve(tool, args.collection.as_deref()) {
                Ok(collection) => get_document(client, &args.key, collection).await,
                Err(refusal) => Ok(refusal),
            }
        }
        external if mcp::is_mcp_name(external) => match policy.mcp_tool(external) {
            Ok(offered) => {
                Ok(mcp::call::execute(client, tool_call, offered, &policy.mcp.scenario).await)
            }
            Err(refusal) => Ok(refusal),
        },
        other => Ok(format!("Unknown tool: {other}")),
    }
}

/// Every hit, each one whole when it fits `result_max_chars` (#398).
async fn search_docs<S: DocSearch>(
    client: &mut S,
    query: &str,
    collection: &str,
    policy: &ToolPolicy,
) -> Result<String, AppError> {
    let results = client.search(query, collection).await?;
    if results.is_empty() {
        return Ok("No results found for this query.".to_string());
    }

    let mut output = String::new();
    for (i, r) in results.iter().enumerate() {
        let clean = strip_markdown(&r.content);
        output.push_str(&format!(
            "Result {} (key: {}, score: {:.2})\n{}\n\n---\n\n",
            i + 1,
            r.key,
            r.score,
            clip(&clean, policy.result_max_chars),
        ));
    }
    Ok(output)
}

/// The document with that key, else the closest match, else a miss.
async fn get_document<S: DocSearch>(
    client: &mut S,
    key: &str,
    collection: &str,
) -> Result<String, AppError> {
    let results = client.search(key, collection).await?;

    if let Some(doc) = results.iter().find(|r| r.key == key) {
        Ok(format!("Document: {}\n\n{}", doc.key, document_body(doc)))
    } else if let Some(doc) = results.first() {
        Ok(format!(
            "Document '{}' not found. Closest match: {}\n\n{}",
            key,
            doc.key,
            document_body(doc)
        ))
    } else {
        Ok(format!(
            "Document '{key}' not found in collection '{collection}'."
        ))
    }
}

fn document_body(doc: &SearchResult) -> String {
    let clean = strip_markdown(&doc.content);
    match clip(&clean, DOCUMENT_MAX_CHARS) {
        Cow::Borrowed(_) => clean,
        Cow::Owned(cut) => format!("{cut}\n\n[Document truncated]"),
    }
}

#[cfg(test)]
mod document_tests;
#[cfg(test)]
mod mcp_tests;
#[cfg(test)]
mod tests;
