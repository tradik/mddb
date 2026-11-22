use axum::http::{HeaderMap, HeaderValue, header};

use super::bearer;

fn with(authorization: &[u8]) -> HeaderMap {
    let mut headers = HeaderMap::new();
    headers.insert(
        header::AUTHORIZATION,
        HeaderValue::from_bytes(authorization).unwrap(),
    );
    headers
}

#[test]
fn the_key_is_the_bearer_token() {
    assert_eq!(bearer(&with(b"Bearer mddb_live_x")), Some("mddb_live_x"));
    assert_eq!(bearer(&with(b"Bearer  padded ")), Some("padded"));
}

#[test]
fn anything_else_is_no_key() {
    assert_eq!(bearer(&HeaderMap::new()), None);
    assert_eq!(bearer(&with(b"Bearer ")), None);
    assert_eq!(bearer(&with(b"Basic dXNlcjpwYXNz")), None);
    assert_eq!(bearer(&with(b"Bearer \xff")), None, "not visible ASCII");
}
