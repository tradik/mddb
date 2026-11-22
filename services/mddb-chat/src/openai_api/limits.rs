//! The per-key limits of the OpenAI-compatible API (#448).
//!
//! The widget charges its limits to a session and to the visitor's address.
//! Here there is no session, and every user of a client such as Open WebUI
//! arrives from the client's one address, so both are charged to the key:
//! `[security] rate_limit_per_minute` through the shared limiter, and the
//! scenario's `rateLimit.messagesPerMinute` through [`KeyPaces`].

use std::time::Instant;

use dashmap::DashMap;
use sha2::{Digest, Sha256};

use crate::session::pace::MessagePace;

/// A short, stable name for a key: four bytes of its SHA-256 in hex, the
/// form mddbd's key listings and logs use. The key itself is never stored
/// or logged.
pub fn fingerprint(key: &str) -> String {
    hex::encode(&Sha256::digest(key.as_bytes())[..4])
}

/// The scenario message pace of each key, one per key and scenario.
#[derive(Debug, Default)]
pub struct KeyPaces {
    paces: DashMap<String, MessagePace>,
}

impl KeyPaces {
    /// Whether the key with `fingerprint` may send one more message to
    /// `scenario` at `now`, counting it when it may. 0 means no limit, and
    /// no entry is kept for it.
    pub fn admit(&self, fingerprint: &str, scenario: &str, per_minute: u32, now: Instant) -> bool {
        if per_minute == 0 {
            return true;
        }
        self.paces
            .entry(format!("{fingerprint}/{scenario}"))
            .or_default()
            .admit(per_minute, now)
    }
}

#[cfg(test)]
#[path = "limits_tests.rs"]
mod tests;
