//! Tests for the server assembly in `app.rs`.

use std::sync::Arc;
use std::time::Duration;

use crate::app::{
    build_state, connect_mddb, cors_layer, listen_addr, llm_provider, router, run_maintenance,
    serve, spawn_maintenance,
};
use crate::config::Config;
use crate::test_support::{stub_http_server, test_config};

/// A port nothing listens on: reserved, then released.
fn dead_addr() -> String {
    let reserved = std::net::TcpListener::bind("127.0.0.1:0").expect("reserve");
    let addr = reserved.local_addr().expect("addr");
    format!("http://{addr}")
}

async fn live_config() -> Config {
    let addr = stub_http_server().await;
    test_config(&format!("http://{addr}"))
}

// #401. The default: start without mddbd and keep trying.
#[tokio::test]
async fn with_retrying_on_startup_does_not_wait_for_mddbd() {
    let config = test_config(&dead_addr());

    let link = connect_mddb(&config.mddb)
        .await
        .expect("an unreachable mddbd must not fail startup");

    assert!(!link.is_connected().await);
}

// `connect_retry_max_seconds = 0` keeps the old contract for anyone whose
// supervisor relies on the exit.
#[tokio::test]
async fn with_retrying_off_an_unreachable_mddbd_fails_startup() {
    let mut config = test_config(&dead_addr());
    config.mddb.connect_retry_max_seconds = 0;

    let err = connect_mddb(&config.mddb)
        .await
        .err()
        .expect("fail-fast startup succeeded against a dead port");

    assert!(
        err.to_string().contains("failed to connect to mddbd"),
        "unexpected error: {err}"
    );
    assert!(build_state(config).await.is_err());
}

#[tokio::test]
async fn with_retrying_off_a_reachable_mddbd_is_connected_before_serving() {
    let mut config = live_config().await;
    config.mddb.connect_retry_max_seconds = 0;

    let link = connect_mddb(&config.mddb).await.expect("connect");

    assert!(link.is_connected().await);
}

#[tokio::test]
async fn the_provider_follows_the_configuration() {
    let mut config = test_config("http://127.0.0.1:1");

    // Both must build without touching the network; which one was built is
    // visible only in where its requests go, which the provider tests cover.
    for provider in ["openai", "ollama", "anthropic", "claude"] {
        config.llm.provider = provider.to_string();
        let _ = llm_provider(&config);
    }
}

#[tokio::test]
async fn the_state_is_built_from_the_configuration() {
    let mut config = live_config().await;
    config.security.trusted_proxies = vec!["10.0.0.0/8".into()];

    let state = build_state(config).await.expect("build");

    assert!(!state.trusted_proxies.is_empty());
    assert_eq!(state.session_manager.attached_count(), 0);
}

#[tokio::test]
async fn maintenance_reaps_expired_sessions() {
    let mut config = live_config().await;
    config.session.session_ttl_minutes = 0;
    // One webhook listening for session.end, so the dispatch path runs too.
    config.webhooks = vec![crate::config::WebhookConfig {
        url: dead_addr(),
        events: vec!["session.end".into()],
        headers: std::collections::HashMap::new(),
    }];
    let state = build_state(config).await.expect("build");
    state
        .session_manager
        .join("Ada".into(), "assistant".into())
        .await;
    tokio::time::sleep(Duration::from_millis(2)).await;

    assert_eq!(run_maintenance(&state).await, 1);
    assert_eq!(run_maintenance(&state).await, 0);
}

#[tokio::test]
async fn the_maintenance_task_runs_without_being_asked() {
    let mut config = live_config().await;
    config.session.session_ttl_minutes = 0;
    let state = build_state(config).await.expect("build");
    state
        .session_manager
        .join("Ada".into(), "assistant".into())
        .await;
    tokio::time::sleep(Duration::from_millis(2)).await;

    // The first tick of an interval fires at once.
    spawn_maintenance(state.clone());

    for _ in 0..200 {
        if state.session_manager.attached_count() == 0 {
            return;
        }
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    panic!("the maintenance task never reaped the expired session");
}

#[test]
fn the_listen_address_falls_back_to_all_interfaces() {
    let mut server = test_config("http://127.0.0.1:1").server;
    server.port = 11030;

    assert_eq!(listen_addr(&server).to_string(), "127.0.0.1:11030");

    server.host = "not an address".into();
    assert_eq!(listen_addr(&server).to_string(), "0.0.0.0:11030");
}

/// Serves `config` on a free port; returns the base URL and a shutdown handle.
async fn serving(
    config: Config,
) -> (
    String,
    tokio::sync::oneshot::Sender<()>,
    tokio::task::JoinHandle<std::io::Result<()>>,
) {
    let state = build_state(config).await.expect("build");
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind");
    let base = format!("http://{}", listener.local_addr().expect("addr"));
    let (stop, stopped) = tokio::sync::oneshot::channel::<()>();
    let handle = tokio::spawn(serve(listener, router(state), async {
        let _ = stopped.await;
    }));
    (base, stop, handle)
}

// #401 end to end: the port answers while mddbd is still away, and says so.
#[tokio::test]
async fn the_server_answers_503_on_health_while_mddbd_is_away() {
    let (base, stop, handle) = serving(test_config(&dead_addr())).await;

    let response = reqwest::get(format!("{base}/health")).await.expect("get");

    assert_eq!(response.status(), 503);
    let body: serde_json::Value = response.json().await.expect("json");
    assert_eq!(body["mddb_connected"], false);

    stop.send(()).expect("the server is gone");
    handle
        .await
        .expect("join")
        .expect("serve ended in an error");
}

#[tokio::test]
async fn the_server_answers_200_on_health_once_connected_and_stops_when_told() {
    let mut config = live_config().await;
    config.mddb.connect_retry_max_seconds = 0;
    let (base, stop, handle) = serving(config).await;

    let health = reqwest::get(format!("{base}/health")).await.expect("get");
    let info = reqwest::get(format!("{base}/config")).await.expect("get");

    assert_eq!(health.status(), 200);
    assert_eq!(info.status(), 200);

    stop.send(()).expect("the server is gone");
    handle
        .await
        .expect("join")
        .expect("serve ended in an error");
}

#[tokio::test]
async fn cors_follows_the_configured_origins() {
    let mut config = test_config(&dead_addr());
    config.server.cors_origins = vec!["https://docs.example.test".into()];
    let _ = cors_layer(&config.server);
    let (base, stop, handle) = serving(config).await;
    let get = |origin: &'static str| {
        reqwest::Client::new()
            .get(format!("{base}/config"))
            .header("Origin", origin)
            .send()
    };

    let allowed = get("https://docs.example.test").await.expect("get");
    let other = get("https://evil.example.test").await.expect("get");

    let header = |r: &reqwest::Response| {
        r.headers()
            .get("access-control-allow-origin")
            .map(|v| v.to_str().unwrap_or_default().to_string())
    };
    assert_eq!(
        header(&allowed).as_deref(),
        Some("https://docs.example.test")
    );
    assert_eq!(header(&other), None, "an unlisted origin was allowed");

    stop.send(()).expect("the server is gone");
    handle
        .await
        .expect("join")
        .expect("serve ended in an error");

    // And the default still means any origin.
    let any = Arc::new(test_config(&dead_addr()));
    let (base, stop, handle) = serving((*any).clone()).await;
    let response = reqwest::Client::new()
        .get(format!("{base}/config"))
        .header("Origin", "https://anything.example.test")
        .send()
        .await
        .expect("get");
    assert_eq!(header(&response).as_deref(), Some("*"));
    stop.send(()).expect("the server is gone");
    handle
        .await
        .expect("join")
        .expect("serve ended in an error");
}
