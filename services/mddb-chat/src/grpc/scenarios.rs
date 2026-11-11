//! Chat scenarios as mddbd stores them (#403): the list with the store's
//! version counter, the system prompt mddbd composes for one of them, and
//! the calls of their external MCP tools (MCP-005).

use super::client::{MddbClient, proto};
use crate::error::AppError;

/// Every scenario the service account may read, and the version they are at.
pub struct ScenarioList {
    /// Grows by one on every create, replace and delete; one number for the
    /// whole store.
    pub version: u64,
    pub scenarios: Vec<proto::ChatScenarioProto>,
}

impl MddbClient {
    /// The stored scenarios. gRPC has no version-only call, so this is also
    /// what the poll for changes asks.
    pub async fn list_chat_scenarios(&mut self) -> Result<ScenarioList, AppError> {
        let request = self.auth_request(proto::ListChatScenariosRequest {});
        let response = self.client.list_chat_scenarios(request).await?.into_inner();
        Ok(ScenarioList {
            version: response.version,
            scenarios: response.scenarios,
        })
    }

    /// The preview of `name`: the system prompt as mddbd composes it —
    /// operator prompt, guardrails, off-topic and style clauses, then the
    /// `responsePrompt` of each allowed collection, composed there and only
    /// there so the panel's preview and the chat cannot disagree — and, since
    /// MCP-005, the state of each external MCP server and tool: which of them
    /// the model may see now. An mddbd older than 2.19.0 sends no servers.
    pub async fn chat_scenario_preview(
        &mut self,
        name: &str,
    ) -> Result<proto::GetChatScenarioPromptResponse, AppError> {
        let request = self.auth_request(proto::GetChatScenarioPromptRequest {
            name: name.to_string(),
        });
        Ok(self
            .client
            .get_chat_scenario_prompt(request)
            .await?
            .into_inner())
    }

    /// Calls an external MCP tool of a scenario through mddbd (MCP-005),
    /// which checks the five switches, attaches the server's credential and
    /// cuts the result. mddb-chat never sees a secret. `deadline` bounds the
    /// whole RPC.
    pub async fn call_chat_mcp_tool(
        &mut self,
        request: proto::CallChatMcpToolRequest,
        deadline: std::time::Duration,
    ) -> Result<proto::CallChatMcpToolResponse, AppError> {
        let mut request = self.auth_request(request);
        request.set_timeout(deadline);
        Ok(self.client.call_chat_mcp_tool(request).await?.into_inner())
    }
}
