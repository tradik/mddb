//! What one turn of the conversation is remembered as, for the `answer`
//! webhook event (#399): the ids of the question and the answer, the tools the
//! model called on the way, and how long it all took.

use std::time::Instant;

use crate::chat::mcp::McpToolset;
use crate::chat::tools::ToolRun;
use crate::chat::turnlog::LoggedRun;
use crate::config::LlmConfig;
use crate::llm::provider::{TokenUsage, ToolCall};
use crate::webhook::events::AnswerReport;
use crate::webhook::types::{ToolCallRecord, WebhookPayload};

#[derive(Debug)]
pub struct TurnTrace {
    /// The id of the visitor's question, named by the `message` event.
    pub message_id: String,
    /// The id of the answer, returned to the client in `done` so that a later
    /// `feedback` can name it.
    pub answer_id: String,
    started: Instant,
    tools: Vec<ToolCallRecord>,
    /// The external tools this turn offers (MCP-005), so that a call of one
    /// is recorded with its server.
    mcp: McpToolset,
    /// What each call in `tools` looked for and found, in the same order (#406).
    runs: Vec<LoggedRun>,
}

impl TurnTrace {
    /// Starts the clock and mints both ids.
    pub fn start() -> Self {
        Self {
            message_id: uuid::Uuid::new_v4().to_string(),
            answer_id: uuid::Uuid::new_v4().to_string(),
            started: Instant::now(),
            tools: Vec::new(),
            mcp: McpToolset::default(),
            runs: Vec::new(),
        }
    }

    /// The external tools of this turn (MCP-005): a call of one is recorded
    /// by its own name with its server, and without a collection.
    pub fn offer_mcp(&mut self, mcp: &McpToolset) {
        self.mcp = mcp.clone();
    }

    /// Remembers one tool call: its name and the collection it was pointed at,
    /// which is `default_collection` unless the arguments name another — the
    /// same rule `tools::run_tool` applies.
    pub fn record_tool(&mut self, call: &ToolCall, default_collection: &str) {
        if let Some(external) = self.mcp.find(&call.function.name) {
            let record = ToolCallRecord {
                name: external.tool.clone(),
                collection: String::new(),
                server: Some(external.server.clone()),
            };
            self.tools.push(record);
            return;
        }
        let collection = serde_json::from_str::<serde_json::Value>(&call.function.arguments)
            .ok()
            .and_then(|args| args.get("collection")?.as_str().map(str::to_string))
            .unwrap_or_else(|| default_collection.to_string());
        self.tools.push(ToolCallRecord {
            name: call.function.name.clone(),
            collection,
            server: None,
        });
    }

    /// Remembers what the call last recorded by `record_tool` found, and how
    /// long it took (#406).
    pub fn record_run(&mut self, run: &ToolRun, duration_ms: u64) {
        self.runs.push(LoggedRun::of(run, duration_ms));
    }

    /// What the calls found, in the order of `tools` (#406).
    pub fn runs(&self) -> &[LoggedRun] {
        &self.runs
    }

    /// The tools called so far, in order; stored with a signed user's answer
    /// (#405).
    pub fn tools(&self) -> &[ToolCallRecord] {
        &self.tools
    }

    /// Milliseconds since the turn started.
    pub fn latency_ms(&self) -> u64 {
        u64::try_from(self.started.elapsed().as_millis()).unwrap_or(u64::MAX)
    }

    /// The `answer` event for this turn.
    pub fn answer(
        &self,
        session_id: &str,
        scenario: &str,
        text: &str,
        llm: &LlmConfig,
        spent: TokenUsage,
    ) -> WebhookPayload {
        WebhookPayload::answer(AnswerReport {
            session_id,
            scenario,
            message_id: &self.answer_id,
            in_reply_to: &self.message_id,
            text,
            provider: &llm.provider,
            model: &llm.model,
            spent,
            tools: self.tools.clone(),
            latency_ms: self.latency_ms(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::llm::provider::ToolCallFunction;

    fn call(name: &str, arguments: &str) -> ToolCall {
        ToolCall {
            id: "call-1".into(),
            function: ToolCallFunction {
                name: name.into(),
                arguments: arguments.into(),
            },
        }
    }

    fn llm() -> LlmConfig {
        toml::from_str(
            r#"
provider = "anthropic"
api_url = "http://localhost"
model = "claude-x"
"#,
        )
        .expect("a minimal llm config parses")
    }

    #[test]
    fn a_turn_mints_two_distinct_ids() {
        let trace = TurnTrace::start();

        assert_ne!(trace.message_id, trace.answer_id);
        assert_eq!(trace.message_id.len(), 36, "uuid v4 as text");
        assert_ne!(
            TurnTrace::start().answer_id,
            trace.answer_id,
            "ids must be unique across turns, not just within one"
        );
    }

    #[test]
    fn a_tool_call_records_the_collection_it_named() {
        let mut trace = TurnTrace::start();

        trace.record_tool(
            &call("search_docs", r#"{"query":"q","collection":"guides"}"#),
            "docs",
        );

        assert_eq!(
            trace.tools,
            vec![ToolCallRecord {
                name: "search_docs".into(),
                collection: "guides".into(),
                server: None,
            }]
        );
    }

    #[test]
    fn a_tool_call_without_a_collection_falls_back_to_the_scenarios() {
        // The same rule run_tool applies: no collection argument means the
        // scenario's collection, so the report says where the search went.
        let mut trace = TurnTrace::start();

        trace.record_tool(&call("search_docs", r#"{"query":"q"}"#), "docs");
        trace.record_tool(&call("get_document", "not json at all"), "docs");
        trace.record_tool(&call("search_docs", r#"{"collection":7}"#), "docs");

        let collections: Vec<&str> = trace.tools.iter().map(|t| t.collection.as_str()).collect();
        assert_eq!(collections, vec!["docs", "docs", "docs"]);
        assert_eq!(trace.tools[1].name, "get_document");
    }

    #[test]
    fn the_answer_event_ties_the_ids_together() {
        let mut trace = TurnTrace::start();
        trace.record_tool(&call("search_docs", "{}"), "docs");
        let spent = TokenUsage {
            input: 10,
            output: 4,
        };

        let payload = trace.answer("sess-1", "support", "the answer", &llm(), spent);
        let v = serde_json::to_value(&payload).unwrap();

        assert_eq!(v["event"], "answer");
        assert_eq!(v["data"]["message_id"], trace.answer_id);
        assert_eq!(v["data"]["in_reply_to"], trace.message_id);
        assert_eq!(v["data"]["session_id"], "sess-1");
        assert_eq!(v["data"]["scenario"], "support");
        assert_eq!(v["data"]["text"], "the answer");
        assert_eq!(v["data"]["provider"], "anthropic");
        assert_eq!(v["data"]["model"], "claude-x");
        assert_eq!(v["data"]["usage"]["input_tokens"], 10);
        assert_eq!(v["data"]["tools"][0]["collection"], "docs");
        assert!(v["data"]["latency_ms"].as_u64().unwrap() < 10_000);
    }

    #[test]
    fn an_external_tool_is_recorded_with_its_server_and_no_collection() {
        use crate::grpc::testing::mcp_state;
        let states = vec![mcp_state("orders", true, &[("get.order", true)])];
        let mcp = McpToolset::for_turn("support", &states, &Default::default(), true, None);
        let mut trace = TurnTrace::start();
        trace.offer_mcp(&mcp);

        trace.record_tool(&call("mcp__orders__get_order", "{}"), "docs");
        trace.record_tool(&call("mcp__orders__invented", "{}"), "docs");
        let v = serde_json::to_value(trace.answer("s", "support", "a", &llm(), Default::default()))
            .unwrap();

        assert_eq!(
            v["data"]["tools"],
            serde_json::json!([
                {"name": "get.order", "server": "orders"},
                {"name": "mcp__orders__invented", "collection": "docs"},
            ])
        );
    }

    #[test]
    fn latency_grows_with_the_clock() {
        let trace = TurnTrace::start();
        std::thread::sleep(std::time::Duration::from_millis(15));

        assert!(trace.latency_ms() >= 15, "got {}", trace.latency_ms());
    }
}
