//! The server modules mddbd has switched on or off (GO-049), as far as the
//! chat cares: `chat` itself, and `chatExternalMcp`.

use super::client::{MddbClient, proto};
use crate::error::AppError;

/// mddbd's name for the chat module. While it is off, mddbd refuses every
/// chat RPC with FAILED_PRECONDITION "feature disabled: chat".
const CHAT: &str = "chat";
/// External MCP servers for chat skills (MCP-005). Requires `chat`.
const CHAT_EXTERNAL_MCP: &str = "chatExternalMcp";

/// The two switches mddb-chat follows, as mddbd reports them in effect.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Features {
    pub chat: bool,
    pub chat_external_mcp: bool,
}

impl Default for Features {
    /// What an mddbd older than the switches behaves like, and what a module
    /// it does not list defaults to: chat on, external MCP off.
    fn default() -> Self {
        Self {
            chat: true,
            chat_external_mcp: false,
        }
    }
}

impl Features {
    /// Reads the effective state (`enabled`) of the two modules out of the
    /// full list; one that is missing keeps its default.
    fn from_list(list: &[proto::FeatureProto]) -> Self {
        let mut features = Self::default();
        for feature in list {
            match feature.name.as_str() {
                CHAT => features.chat = feature.enabled,
                CHAT_EXTERNAL_MCP => features.chat_external_mcp = feature.enabled,
                _ => {}
            }
        }
        features
    }
}

impl MddbClient {
    /// The modules as mddbd has them now. An mddbd without the call
    /// (UNIMPLEMENTED) predates the switches and counts as the defaults; any
    /// other failure is an error, so the caller keeps what it knew.
    pub async fn list_features(&mut self) -> Result<Features, AppError> {
        let request = self.auth_request(proto::ListFeaturesRequest {});
        match self.client.list_features(request).await {
            Ok(response) => Ok(Features::from_list(&response.into_inner().features)),
            Err(status) if status.code() == tonic::Code::Unimplemented => Ok(Features::default()),
            Err(status) => Err(status.into()),
        }
    }
}
