//! The join token a host signs for its signed-in user (#405).
//!
//! `v1.<base64url payload>.<base64url HMAC-SHA256>`, no padding, the
//! signature taken over the ASCII bytes of `"v1." + payload`. The format and
//! its test vector are in docs/API.md, "The join token (host -> mddb-chat)".
//!
//! Verified in the documented order: the shape, then the signature (in
//! constant time), and only then the payload is decoded and read — nothing a
//! forger wrote is parsed before it is known to come from the host.

use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use hmac::{Hmac, KeyInit, Mac};
use serde::Deserialize;
use sha2::Sha256;

/// The longest a token may live: it only opens a socket, so a host minting
/// one for days is a mistake worth refusing.
pub const MAX_LIFETIME_SECONDS: u64 = 24 * 3600;

/// The bytes a claim may hold, as mddbd limits `sub`, `scenario` and
/// `displayName`.
const MAX_CLAIM_BYTES: usize = 256;

/// What a verified token says.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Claims {
    /// The user's opaque id at the host; the conversations' owner.
    pub sub: String,
    /// The display name, `None` when absent or empty.
    pub name: Option<String>,
    /// The only scenario this token may join.
    pub scenario: String,
    /// Expiry, Unix seconds.
    pub exp: u64,
}

/// Why a token was refused.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TokenError {
    Malformed,
    BadSignature,
    Expired,
    TooLong,
    MissingClaim(&'static str),
    InvalidClaim(&'static str),
}

impl std::fmt::Display for TokenError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Malformed => write!(f, "malformed token"),
            Self::BadSignature => write!(f, "bad signature"),
            Self::Expired => write!(f, "token has expired"),
            Self::TooLong => write!(f, "token lives longer than 24 hours"),
            Self::MissingClaim(claim) => write!(f, "missing claim: {claim}"),
            Self::InvalidClaim(claim) => write!(f, "invalid claim: {claim}"),
        }
    }
}

/// The payload as sent; every claim optional so a missing one is reported
/// by name rather than as a parse error. Unknown claims are ignored.
#[derive(Deserialize)]
struct Payload {
    sub: Option<String>,
    name: Option<String>,
    scenario: Option<String>,
    exp: Option<i64>,
}

/// Verifies `token` with `secret` at Unix time `now`.
pub fn verify(token: &str, secret: &[u8], now: u64) -> Result<Claims, TokenError> {
    let mut parts = token.split('.');
    let (Some("v1"), Some(payload_b64), Some(signature_b64), None) =
        (parts.next(), parts.next(), parts.next(), parts.next())
    else {
        return Err(TokenError::Malformed);
    };
    let signature = URL_SAFE_NO_PAD
        .decode(signature_b64)
        .map_err(|_| TokenError::Malformed)?;

    let mut mac = hmac_of(secret);
    mac.update(b"v1.");
    mac.update(payload_b64.as_bytes());
    // verify_slice compares in constant time.
    mac.verify_slice(&signature)
        .map_err(|_| TokenError::BadSignature)?;

    let payload = URL_SAFE_NO_PAD
        .decode(payload_b64)
        .map_err(|_| TokenError::Malformed)?;
    let payload: Payload = serde_json::from_slice(&payload).map_err(|_| TokenError::Malformed)?;

    let exp = payload.exp.ok_or(TokenError::MissingClaim("exp"))?;
    let exp = u64::try_from(exp).unwrap_or(0);
    if exp <= now {
        return Err(TokenError::Expired);
    }
    if exp - now > MAX_LIFETIME_SECONDS {
        return Err(TokenError::TooLong);
    }

    Ok(Claims {
        sub: required(payload.sub, "sub")?,
        name: optional(payload.name, "name")?,
        scenario: required(payload.scenario, "scenario")?,
        exp,
    })
}

/// A claim that must be there and not empty.
fn required(value: Option<String>, claim: &'static str) -> Result<String, TokenError> {
    optional(value, claim)?.ok_or(TokenError::MissingClaim(claim))
}

/// A claim that may be absent; empty counts as absent.
fn optional(value: Option<String>, claim: &'static str) -> Result<Option<String>, TokenError> {
    match value {
        None => Ok(None),
        Some(v) if v.is_empty() => Ok(None),
        Some(v) if v.len() > MAX_CLAIM_BYTES || v.chars().any(char::is_control) => {
            Err(TokenError::InvalidClaim(claim))
        }
        Some(v) => Ok(Some(v)),
    }
}

fn hmac_of(secret: &[u8]) -> Hmac<Sha256> {
    Hmac::<Sha256>::new_from_slice(secret).expect("HMAC takes a key of any size")
}

/// HMAC-SHA256 of `body` under `secret`.
#[cfg(test)]
fn mac(secret: &[u8], body: &str) -> Vec<u8> {
    let mut mac = hmac_of(secret);
    mac.update(body.as_bytes());
    mac.finalize().into_bytes().to_vec()
}

/// Signs `payload_json` the way a host does. Tests only: the server never
/// mints tokens.
#[cfg(test)]
pub fn sign(secret: &[u8], payload_json: &str) -> String {
    let body = format!("v1.{}", URL_SAFE_NO_PAD.encode(payload_json));
    format!("{body}.{}", URL_SAFE_NO_PAD.encode(mac(secret, &body)))
}

#[cfg(test)]
#[path = "token_tests.rs"]
mod tests;
