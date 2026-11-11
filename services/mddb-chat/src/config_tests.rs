//! `[identity]` (#405) and `[mddb] http_url`. The older settings are tested
//! next to their definitions in config.rs.

use super::*;
use std::io::Write;

const MINIMAL: &str = r#"
[server]
[mddb]
[llm]
api_url = "http://localhost:11434/v1"
model = "test"
[session]
[security]
"#;

const SECRET_32: &str = "0123456789abcdef0123456789abcdef";

fn load(name: &str, body: &str) -> Result<Config, String> {
    let path =
        std::env::temp_dir().join(format!("mddb-chat-cfg2-{}-{name}.toml", std::process::id()));
    std::fs::File::create(&path)
        .and_then(|mut f| f.write_all(body.as_bytes()))
        .expect("write the config");
    let loaded = Config::load(&path).map_err(|e| e.to_string());
    std::fs::remove_file(&path).ok();
    loaded
}

fn with(section: &str) -> String {
    format!("{MINIMAL}\n{section}\n")
}

fn mddb(grpc_addr: &str, http_url: &str) -> MddbConfig {
    toml::from_str(&format!(
        "grpc_addr = \"{grpc_addr}\"\nhttp_url = \"{http_url}\""
    ))
    .expect("parse")
}

// ---- [identity] ----

#[test]
fn an_old_config_has_identity_off_and_not_required() {
    let cfg = load("identity-default", MINIMAL).expect("loads");
    assert_eq!(cfg.identity, IdentityConfig::default());
    assert!(!cfg.identity.enabled());
    assert!(!cfg.identity.required);
}

#[test]
fn a_secret_of_32_bytes_turns_signed_joins_on() {
    let cfg = load(
        "identity-on",
        &with(&format!(
            "[identity]\nsecret = \"{SECRET_32}\"\nrequired = true"
        )),
    )
    .expect("loads");
    assert!(cfg.identity.enabled());
    assert!(cfg.identity.required);
}

#[test]
fn a_short_secret_is_a_startup_error() {
    let err = load(
        "identity-short",
        &with("[identity]\nsecret = \"too-short\""),
    )
    .expect_err("refused");
    assert!(err.contains("at least 32 bytes"), "{err}");
}

#[test]
fn requiring_identity_without_a_secret_is_a_startup_error() {
    let err = load("identity-required", &with("[identity]\nrequired = true")).expect_err("refused");
    assert!(err.contains("MDDB_CHAT_IDENTITY_SECRET"), "{err}");
}

// One test: the environment is process-global.
#[test]
fn the_secret_comes_from_the_environment_when_the_toml_has_none() {
    unsafe { std::env::set_var("MDDB_CHAT_IDENTITY_SECRET", SECRET_32) };
    let from_env = load("identity-env", MINIMAL);
    let file_wins = load(
        "identity-file",
        &with("[identity]\nsecret = \"ffffffffffffffffffffffffffffffffffff\""),
    );
    unsafe { std::env::remove_var("MDDB_CHAT_IDENTITY_SECRET") };

    assert_eq!(from_env.expect("loads").identity.secret, SECRET_32);
    assert_eq!(
        file_wins.expect("loads").identity.secret,
        "ffffffffffffffffffffffffffffffffffff"
    );
}

// ---- [mddb] http_url ----

#[test]
fn the_default_grpc_port_derives_the_default_http_port() {
    assert_eq!(
        mddb("http://localhost:11024", "").http_base(),
        Ok("http://localhost:11023".to_string())
    );
    assert_eq!(
        mddb("grpc://mddbd.internal:11024/", "").http_base(),
        Ok("http://mddbd.internal:11023".to_string())
    );
    assert_eq!(
        mddb("https://[::1]:11024", "").http_base(),
        Ok("https://[::1]:11023".to_string())
    );
}

#[test]
fn a_port_that_only_contains_11024_is_not_rewritten() {
    // The old string replace turned 111024 into 111023 and a host named
    // a11024 into a11023.
    for addr in ["http://localhost:111024", "http://a11024.test:9000"] {
        let err = mddb(addr, "").http_base().expect_err(addr);
        assert!(err.contains("[mddb] http_url"), "{err}");
    }
}

#[test]
fn any_other_grpc_address_needs_http_url() {
    for addr in ["http://localhost:18024", "http://mddbd", "not a uri at all"] {
        let err = mddb(addr, "").http_base().expect_err(addr);
        assert!(err.contains("[mddb] http_url is required"), "{addr}: {err}");
    }
}

#[test]
fn http_url_wins_and_loses_its_trailing_slash() {
    assert_eq!(
        mddb("http://localhost:18024", "http://localhost:18023/").http_base(),
        Ok("http://localhost:18023".to_string())
    );
    assert_eq!(
        mddb("http://localhost:11024", "https://mddb.example.test").http_base(),
        Ok("https://mddb.example.test".to_string())
    );
}

#[test]
fn credentials_without_a_derivable_http_url_are_a_startup_error() {
    let body = MINIMAL.replace(
        "[mddb]",
        "[mddb]\ngrpc_addr = \"http://localhost:18024\"\nauth_username = \"chat\"",
    );
    let err = load("http-url-missing", &body).expect_err("refused");
    assert!(err.contains("[mddb] http_url is required"), "{err}");

    // Without credentials there is no login, so nothing to derive. Checked
    // without `load`: another test sets MDDB_CHAT_AUTH_USERNAME meanwhile.
    let anonymous = MINIMAL.replace("[mddb]", "[mddb]\ngrpc_addr = \"http://localhost:18024\"");
    let cfg: Config = toml::from_str(&anonymous).expect("parse");
    assert_eq!(cfg.validate(), Ok(()));
}

// The shipped example documents every key; it must stay a config that loads.
#[test]
fn the_example_config_loads_with_identity_off() {
    let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("config.example.toml");
    let cfg = Config::load(&path).expect("config.example.toml loads");
    assert!(!cfg.identity.required);
    assert_eq!(cfg.mddb.http_url, "");
    assert_eq!(
        cfg.get_scenario("support").unwrap().retention_days,
        Some(30)
    );
}
