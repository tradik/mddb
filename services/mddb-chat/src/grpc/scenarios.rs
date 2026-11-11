//! Chat scenarios as mddbd stores them (#403): the list with the store's
//! version counter, and the system prompt mddbd composes for one of them.

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

    /// The system prompt of `name` as mddbd composes it: operator prompt,
    /// guardrails, off-topic and style clauses, then the `responsePrompt` of
    /// each allowed collection. Composed there and only there, so the panel's
    /// preview and the chat cannot disagree.
    pub async fn chat_scenario_prompt(&mut self, name: &str) -> Result<String, AppError> {
        let request = self.auth_request(proto::GetChatScenarioPromptRequest {
            name: name.to_string(),
        });
        let response = self.client.get_chat_scenario_prompt(request).await?;
        Ok(response.into_inner().prompt)
    }
}
