use super::*;

const SECRET: &[u8] = b"test-secret-0123456789abcdef0123456789abcdef";
/// Five minutes before the documented token expires.
const NOW: u64 = 1_900_000_000 - 300;

/// The vector of docs/API.md, "The join token (host -> mddb-chat)". A host
/// written from that page must be accepted here byte for byte.
const DOC_TOKEN: &str = "v1.eyJzdWIiOiJ1c2VyLTQyIiwibmFtZSI6IkFkYSIsInNjZW5hcmlvIjoic3VwcG9ydCIsImV4cCI6MTkwMDAwMDAwMH0.GlF4jwoVSiQHfdz2PJCcQimFfTfy-kCn-wWGkDd69xI";

fn token(payload: &str) -> String {
    sign(SECRET, payload)
}

#[test]
fn the_documented_test_vector_is_reproduced_and_accepted() {
    let payload = r#"{"sub":"user-42","name":"Ada","scenario":"support","exp":1900000000}"#;
    assert_eq!(sign(SECRET, payload), DOC_TOKEN);

    let claims = verify(DOC_TOKEN, SECRET, NOW).expect("the documented token verifies");
    assert_eq!(
        claims,
        Claims {
            sub: "user-42".into(),
            name: Some("Ada".into()),
            scenario: "support".into(),
            exp: 1_900_000_000,
        }
    );
}

#[test]
fn another_secret_does_not_verify() {
    let other = b"another-secret-0123456789abcdef0123456789";
    assert_eq!(verify(DOC_TOKEN, other, NOW), Err(TokenError::BadSignature));
}

#[test]
fn a_changed_payload_does_not_verify() {
    // Another user's payload under the documented signature.
    let (_, signature) = DOC_TOKEN.rsplit_once('.').unwrap();
    let forged_payload =
        URL_SAFE_NO_PAD.encode(r#"{"sub":"admin","scenario":"support","exp":1900000000}"#);
    let forged = format!("v1.{forged_payload}.{signature}");
    assert_eq!(verify(&forged, SECRET, NOW), Err(TokenError::BadSignature));
}

#[test]
fn the_shape_is_checked_before_anything_else() {
    for bad in [
        "",
        "v1",
        "v1.abc",
        "v1.a.b.c",
        "v2.eyJ9.abc",
        "v1.eyJ9.not base64!",
        "v1.eyJ9.abc=",
    ] {
        assert_eq!(
            verify(bad, SECRET, NOW),
            Err(TokenError::Malformed),
            "{bad:?}"
        );
    }
}

#[test]
fn a_signed_payload_that_is_not_base64_or_json_is_malformed() {
    let body = "v1.%%%";
    let not_base64 = format!("{body}.{}", URL_SAFE_NO_PAD.encode(mac(SECRET, body)));
    assert_eq!(verify(&not_base64, SECRET, NOW), Err(TokenError::Malformed));
    assert_eq!(
        verify(&token("not json"), SECRET, NOW),
        Err(TokenError::Malformed)
    );
    assert_eq!(
        verify(
            &token(r#"{"sub":"u","scenario":"s","exp":"soon"}"#),
            SECRET,
            NOW
        ),
        Err(TokenError::Malformed)
    );
}

#[test]
fn expiry_must_be_in_the_future_and_within_a_day() {
    let at = |exp: u64| token(&format!(r#"{{"sub":"u","scenario":"s","exp":{exp}}}"#));

    assert_eq!(verify(&at(NOW), SECRET, NOW), Err(TokenError::Expired));
    assert_eq!(verify(&at(NOW - 1), SECRET, NOW), Err(TokenError::Expired));
    assert!(verify(&at(NOW + 1), SECRET, NOW).is_ok());
    assert!(verify(&at(NOW + MAX_LIFETIME_SECONDS), SECRET, NOW).is_ok());
    assert_eq!(
        verify(&at(NOW + MAX_LIFETIME_SECONDS + 1), SECRET, NOW),
        Err(TokenError::TooLong)
    );
    // A negative exp is a past one, not a parse failure.
    assert_eq!(
        verify(
            &token(r#"{"sub":"u","scenario":"s","exp":-5}"#),
            SECRET,
            NOW
        ),
        Err(TokenError::Expired)
    );
}

#[test]
fn sub_scenario_and_exp_are_required() {
    let exp = NOW + 60;
    for (payload, claim) in [
        (format!(r#"{{"scenario":"s","exp":{exp}}}"#), "sub"),
        (format!(r#"{{"sub":"","scenario":"s","exp":{exp}}}"#), "sub"),
        (format!(r#"{{"sub":"u","exp":{exp}}}"#), "scenario"),
        (
            format!(r#"{{"sub":"u","scenario":"","exp":{exp}}}"#),
            "scenario",
        ),
        (r#"{"sub":"u","scenario":"s"}"#.to_string(), "exp"),
    ] {
        assert_eq!(
            verify(&token(&payload), SECRET, NOW),
            Err(TokenError::MissingClaim(claim)),
            "{payload}"
        );
    }
}

#[test]
fn claims_with_control_characters_or_over_256_bytes_are_refused() {
    let exp = NOW + 60;
    let long = "x".repeat(257);
    for (payload, claim) in [
        (
            format!(r#"{{"sub":"a\nb","scenario":"s","exp":{exp}}}"#),
            "sub",
        ),
        (
            format!(r#"{{"sub":"{long}","scenario":"s","exp":{exp}}}"#),
            "sub",
        ),
        (
            format!(r#"{{"sub":"u","scenario":"s\u0007","exp":{exp}}}"#),
            "scenario",
        ),
        (
            format!(r#"{{"sub":"u","name":"{long}","scenario":"s","exp":{exp}}}"#),
            "name",
        ),
    ] {
        assert_eq!(
            verify(&token(&payload), SECRET, NOW),
            Err(TokenError::InvalidClaim(claim)),
            "{payload}"
        );
    }
    // Exactly 256 bytes is still a sub.
    let edge = "y".repeat(256);
    let ok = format!(r#"{{"sub":"{edge}","scenario":"s","exp":{exp}}}"#);
    assert_eq!(verify(&token(&ok), SECRET, NOW).unwrap().sub, edge);
}

#[test]
fn unknown_claims_are_ignored_and_an_empty_name_is_none() {
    let exp = NOW + 60;
    let payload = format!(r#"{{"sub":"u","name":"","scenario":"s","exp":{exp},"role":"x"}}"#);
    let claims = verify(&token(&payload), SECRET, NOW).expect("verifies");
    assert_eq!(claims.name, None);
}

#[test]
fn every_error_reads_as_a_reason() {
    let reasons: Vec<String> = [
        TokenError::Malformed,
        TokenError::BadSignature,
        TokenError::Expired,
        TokenError::TooLong,
        TokenError::MissingClaim("sub"),
        TokenError::InvalidClaim("name"),
    ]
    .iter()
    .map(ToString::to_string)
    .collect();
    assert_eq!(
        reasons,
        vec![
            "malformed token",
            "bad signature",
            "token has expired",
            "token lives longer than 24 hours",
            "missing claim: sub",
            "invalid claim: name",
        ]
    );
}
