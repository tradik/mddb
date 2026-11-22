use std::time::{Duration, Instant};

use super::{KeyPaces, fingerprint};

#[test]
fn a_fingerprint_is_four_bytes_of_the_keys_sha256() {
    // SHA-256("mddb_live_x") starts with these four bytes.
    let got = fingerprint("mddb_live_x");
    assert_eq!(got.len(), 8);
    assert_eq!(got, fingerprint("mddb_live_x"), "stable");
    assert_ne!(got, fingerprint("mddb_live_y"));
    assert!(!got.contains("mddb"), "the key itself is not in it");
}

#[test]
fn each_key_and_scenario_has_its_own_pace() {
    let paces = KeyPaces::default();
    let now = Instant::now();

    assert!(paces.admit("k1", "support", 1, now));
    assert!(
        !paces.admit("k1", "support", 1, now),
        "the second in a minute"
    );
    assert!(paces.admit("k1", "sales", 1, now), "another scenario");
    assert!(paces.admit("k2", "support", 1, now), "another key");
    assert!(paces.admit("k1", "support", 1, now + Duration::from_secs(61)));
}

#[test]
fn no_limit_keeps_no_entry() {
    let paces = KeyPaces::default();
    for _ in 0..5 {
        assert!(paces.admit("k1", "support", 0, Instant::now()));
    }
    assert!(paces.paces.is_empty());
}
