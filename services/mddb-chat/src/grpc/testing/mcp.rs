//! The fake mddbd's external MCP side (MCP-005): the server states
//! `GetChatScenarioPrompt` reports, and what `CallChatMcpTool` answers.

use tonic::{Code, Status};

use super::{FakeMddbd, FakeState};
use crate::grpc::client::proto;

/// How the fake answers `CallChatMcpTool`.
#[derive(Clone)]
pub enum FakeMcpCall {
    /// An answer, as mddbd sends it after calling the server.
    Answer {
        content: String,
        is_error: bool,
        truncated: bool,
    },
    /// The call fails with this code and message, as mddbd refuses one.
    Refused(Code, String),
}

impl FakeMddbd {
    /// What `GetChatScenarioPrompt` reports as the servers of `scenario`.
    pub fn set_mcp_states(&self, scenario: &str, states: Vec<proto::ChatMcpServerStateProto>) {
        self.state().mcp_states.insert(scenario.to_string(), states);
    }

    /// What every `CallChatMcpTool` answers from now on.
    pub fn set_mcp_call(&self, call: FakeMcpCall) {
        self.state().mcp_call = call;
    }
}

/// A server state as mddbd reports it: `active` as given, and each
/// `(tool, active)` pair as given.
pub fn mcp_state(
    name: &str,
    active: bool,
    tools: &[(&str, bool)],
) -> proto::ChatMcpServerStateProto {
    let reason = |on: bool| if on { "" } else { "switched off" }.to_string();
    proto::ChatMcpServerStateProto {
        name: name.to_string(),
        url: format!("https://{name}.example/mcp"),
        enabled: active,
        active,
        reason: reason(active),
        tools: tools
            .iter()
            .map(|(tool, on)| proto::ChatMcpToolStateProto {
                name: tool.to_string(),
                active: *on,
                reason: reason(*on),
            })
            .collect(),
        forward_identity: false,
    }
}

/// A server of a scenario as mddbd stores it, with its two limits.
pub fn mcp_server(name: &str, timeout_ms: i32, result_max_chars: i32) -> proto::ChatMcpServerProto {
    proto::ChatMcpServerProto {
        name: name.to_string(),
        url: format!("https://{name}.example/mcp"),
        enabled: true,
        timeout_ms,
        result_max_chars,
        ..Default::default()
    }
}

pub(super) fn call_tool(
    state: &mut FakeState,
    request: proto::CallChatMcpToolRequest,
) -> Result<proto::CallChatMcpToolResponse, Status> {
    match state.mcp_call.clone() {
        FakeMcpCall::Answer {
            content,
            is_error,
            truncated,
        } => Ok(proto::CallChatMcpToolResponse {
            scenario: request.scenario,
            server: request.server,
            tool: request.tool,
            content,
            is_error,
            truncated,
            duration_ms: 12,
        }),
        FakeMcpCall::Refused(code, message) => Err(Status::new(code, message)),
    }
}
