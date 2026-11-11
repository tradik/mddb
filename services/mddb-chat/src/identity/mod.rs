//! Who a signed-in user is (#405).
//!
//! A host that has already authenticated its user signs a short-lived join
//! token with a secret it shares with mddb-chat; `token.rs` verifies it. The
//! token's `sub` is the only user id the server ever believes: a user id sent
//! by the browser is never read.

pub mod token;

pub use token::Claims;

use crate::config::IdentityConfig;
use crate::error::AppError;

/// The current Unix time in seconds.
pub fn now() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs()
}

/// What a `join` proves: the verified claims of its token, or `None` for an
/// anonymous join where the server allows one.
///
/// A token is only ever accepted for the scenario it names, so a host can
/// hand a user a token for its support chat without opening every other
/// scenario on the same server to them.
pub fn identify(
    config: &IdentityConfig,
    token: Option<&str>,
    scenario: &str,
) -> Result<Option<Claims>, AppError> {
    let unauthorized = |msg: &str| AppError::Unauthorized(msg.to_string());
    let Some(token) = token.filter(|t| !t.is_empty()) else {
        return match config.required {
            true => Err(unauthorized("a signed identity token is required")),
            false => Ok(None),
        };
    };
    if !config.enabled() {
        return Err(unauthorized("signed joins are not enabled on this server"));
    }
    let claims = token::verify(token, config.secret.as_bytes(), now())
        .map_err(|e| AppError::Unauthorized(format!("identity token rejected: {e}")))?;
    if claims.scenario != scenario {
        return Err(unauthorized("the identity token is for another scenario"));
    }
    Ok(Some(claims))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn config(secret: &str, required: bool) -> IdentityConfig {
        IdentityConfig {
            secret: secret.to_string(),
            required,
        }
    }

    const SECRET: &str = "0123456789abcdef0123456789abcdef";

    fn valid(scenario: &str) -> String {
        let exp = now() + 60;
        token::sign(
            SECRET.as_bytes(),
            &format!(r#"{{"sub":"u","scenario":"{scenario}","exp":{exp}}}"#),
        )
    }

    #[test]
    fn no_token_is_anonymous_unless_identity_is_required() {
        assert!(matches!(identify(&config("", false), None, "s"), Ok(None)));
        assert!(matches!(
            identify(&config(SECRET, false), Some(""), "s"),
            Ok(None)
        ));
        let err = identify(&config(SECRET, true), None, "s").unwrap_err();
        assert_eq!(
            err.to_string(),
            "unauthorized: a signed identity token is required"
        );
    }

    #[test]
    fn a_valid_token_for_the_joined_scenario_names_the_user() {
        let claims = identify(&config(SECRET, true), Some(&valid("s")), "s")
            .unwrap()
            .unwrap();
        assert_eq!(claims.sub, "u");
    }

    #[test]
    fn a_token_is_refused_without_a_secret_for_another_scenario_or_when_forged() {
        let refuse = |cfg: IdentityConfig, token: &str| {
            identify(&cfg, Some(token), "s").unwrap_err().to_string()
        };
        assert_eq!(
            refuse(config("", false), &valid("s")),
            "unauthorized: signed joins are not enabled on this server"
        );
        assert_eq!(
            refuse(config(SECRET, false), &valid("other")),
            "unauthorized: the identity token is for another scenario"
        );
        assert_eq!(
            refuse(config(SECRET, false), "v1.x.y"),
            "unauthorized: identity token rejected: malformed token"
        );
    }
}
