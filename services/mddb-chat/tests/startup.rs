//! Starts the real binary (#401).
//!
//! mddb-chat used to exit with `failed to connect to mddbd: transport error`
//! when mddbd was not reachable yet, which in Kubernetes is a crash loop.
//! These tests run the built executable against a port nothing listens on
//! and check what an orchestrator would see.

use std::io::Write;
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::time::Duration;

/// A loopback port nothing listens on: reserved, then released.
fn free_port() -> u16 {
    let reserved = std::net::TcpListener::bind("127.0.0.1:0").expect("reserve a port");
    reserved.local_addr().expect("local addr").port()
}

fn write_config(name: &str, port: u16, mddb_port: u16, extra_mddb: &str) -> PathBuf {
    let path = std::env::temp_dir().join(format!(
        "mddb-chat-startup-{}-{name}.toml",
        std::process::id()
    ));
    let body = format!(
        r#"
[server]
host = "127.0.0.1"
port = {port}
[mddb]
grpc_addr = "http://127.0.0.1:{mddb_port}"
{extra_mddb}
[llm]
api_url = "http://127.0.0.1:1/v1"
model = "test"
[session]
[security]
"#
    );
    std::fs::File::create(&path)
        .and_then(|mut f| f.write_all(body.as_bytes()))
        .expect("write the config");
    path
}

fn start(config: &PathBuf) -> Child {
    Command::new(env!("CARGO_BIN_EXE_mddb-chat"))
        .env("MDDB_CHAT_CONFIG", config)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("start mddb-chat")
}

#[tokio::test]
async fn it_starts_without_mddbd_reports_not_ready_and_stops_on_sigterm() {
    let port = free_port();
    let config = write_config("retry", port, free_port(), "");
    let mut child = start(&config);

    // Poll until the port answers; a process that exited would never answer.
    let url = format!("http://127.0.0.1:{port}/health");
    let mut response = None;
    for _ in 0..200 {
        if let Ok(r) = reqwest::get(&url).await {
            response = Some(r);
            break;
        }
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    let response = response.expect("mddb-chat never opened its port without mddbd");

    assert_eq!(
        response.status(),
        503,
        "not ready is the honest answer while mddbd is away"
    );
    let body: serde_json::Value = response.json().await.expect("json");
    assert_eq!(body["status"], "starting");
    assert_eq!(body["mddb_connected"], false);
    assert!(
        child.try_wait().expect("try_wait").is_none(),
        "mddb-chat exited instead of retrying"
    );

    // What Kubernetes sends. The process must leave by itself, cleanly.
    let killed = Command::new("kill")
        .args(["-TERM", &child.id().to_string()])
        .status()
        .expect("run kill");
    assert!(killed.success());
    let status = child.wait().expect("wait");
    assert!(status.success(), "SIGTERM ended in {status}");

    std::fs::remove_file(&config).ok();
}

#[test]
fn with_retrying_off_it_exits_as_it_always_did() {
    let config = write_config(
        "failfast",
        free_port(),
        free_port(),
        "connect_retry_max_seconds = 0",
    );

    let output = start(&config).wait_with_output().expect("wait");

    assert!(!output.status.success(), "fail-fast startup did not fail");
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        stderr.contains("failed to connect to mddbd"),
        "unexpected stderr: {stderr}"
    );
    std::fs::remove_file(&config).ok();
}

#[test]
fn without_a_config_file_it_looks_for_config_toml_and_fails_without_one() {
    // No MDDB_CHAT_CONFIG: the default is ./config.toml, and this directory
    // has none.
    let empty = std::env::temp_dir().join(format!("mddb-chat-empty-{}", std::process::id()));
    std::fs::create_dir_all(&empty).expect("create an empty directory");

    let output = Command::new(env!("CARGO_BIN_EXE_mddb-chat"))
        .env_remove("MDDB_CHAT_CONFIG")
        .current_dir(&empty)
        .output()
        .expect("run mddb-chat");

    assert!(!output.status.success());
    std::fs::remove_dir_all(&empty).ok();
}
