//! The server modules mddbd has on or off (GO-049), shared between the poll
//! that reads them and the connections that act on them.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use crate::grpc::features::Features;

/// The last switches read from mddbd; the defaults (chat on, external MCP
/// off) until the first read, and kept as they are when a read fails.
#[derive(Clone)]
pub struct FeatureFlags {
    chat: Arc<AtomicBool>,
    chat_external_mcp: Arc<AtomicBool>,
}

impl Default for FeatureFlags {
    fn default() -> Self {
        let defaults = Features::default();
        Self {
            chat: Arc::new(AtomicBool::new(defaults.chat)),
            chat_external_mcp: Arc::new(AtomicBool::new(defaults.chat_external_mcp)),
        }
    }
}

impl FeatureFlags {
    pub fn set(&self, features: Features) {
        self.chat.store(features.chat, Ordering::Relaxed);
        self.chat_external_mcp
            .store(features.chat_external_mcp, Ordering::Relaxed);
    }

    /// Whether visitors may join. Off: `join` is refused with "chat is
    /// disabled on this server", and the scenarios are not reloaded.
    pub fn chat(&self) -> bool {
        self.chat.load(Ordering::Relaxed)
    }

    /// Whether chat skills may call external MCP servers. Only reported on
    /// /health for now: the switch is in place for MCP-005, which nothing in
    /// mddb-chat implements yet.
    pub fn chat_external_mcp(&self) -> bool {
        self.chat_external_mcp.load(Ordering::Relaxed)
    }
}
