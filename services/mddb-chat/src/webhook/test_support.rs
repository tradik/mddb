//! A webhook receiver for tests: an HTTP endpoint on loopback that records
//! what the dispatcher posts and answers with a scripted status.

use std::collections::VecDeque;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use axum::Router;
use axum::extract::State;
use axum::http::{HeaderMap, StatusCode};
use axum::routing::post;
use tokio::sync::mpsc;

/// One POST the sink received.
#[derive(Debug)]
pub struct Delivery {
    pub event: String,
    pub signature: String,
    pub raw_body: String,
    pub body: serde_json::Value,
}

struct SinkState {
    deliveries: mpsc::UnboundedSender<Delivery>,
    /// Statuses to answer with, in order; empty means 200.
    statuses: Mutex<VecDeque<u16>>,
}

pub struct WebhookSink {
    pub url: String,
    deliveries: mpsc::UnboundedReceiver<Delivery>,
}

impl WebhookSink {
    /// A sink that accepts everything.
    pub async fn start() -> Self {
        Self::with_statuses(Vec::new()).await
    }

    /// A sink that answers the first requests with `statuses`, then 200.
    pub async fn with_statuses(statuses: Vec<u16>) -> Self {
        enable_logs();
        let (tx, rx) = mpsc::unbounded_channel();
        let state = Arc::new(SinkState {
            deliveries: tx,
            statuses: Mutex::new(statuses.into()),
        });
        let app = Router::new()
            .route("/hook", post(receive))
            .with_state(state);
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind a loopback port");
        let addr = listener.local_addr().expect("local addr");
        tokio::spawn(async move { axum::serve(listener, app).await });
        Self {
            url: format!("http://{addr}/hook"),
            deliveries: rx,
        }
    }

    /// The next delivery, or a panic after five seconds of silence.
    pub async fn next(&mut self) -> Delivery {
        tokio::time::timeout(Duration::from_secs(5), self.deliveries.recv())
            .await
            .expect("a webhook should have arrived within five seconds")
            .expect("the sink is still serving")
    }

    /// Asserts that nothing arrives within a short grace period.
    pub async fn assert_silent(&mut self) {
        tokio::time::sleep(Duration::from_millis(150)).await;
        let stray = self.deliveries.try_recv().ok();
        assert!(stray.is_none(), "unexpected webhook delivery: {stray:?}");
    }
}

/// Turns every log level on for the test process, discarding the output.
///
/// `tracing` does not evaluate a log call's fields while its level is off, so
/// without this the logging lines of the code under test are never run — and
/// a field expression that panics would only be found in production.
pub fn enable_logs() {
    let _ = tracing_subscriber::fmt()
        .with_max_level(tracing::Level::TRACE)
        .with_writer(std::io::sink)
        .try_init();
}

async fn receive(
    State(state): State<Arc<SinkState>>,
    headers: HeaderMap,
    raw_body: String,
) -> StatusCode {
    let header = |name: &str| {
        headers
            .get(name)
            .and_then(|v| v.to_str().ok())
            .unwrap_or_default()
            .to_string()
    };
    let body = serde_json::from_str(&raw_body).unwrap_or(serde_json::Value::Null);
    let _ = state.deliveries.send(Delivery {
        event: header("x-mddb-event"),
        signature: header("x-mddb-signature"),
        raw_body,
        body,
    });
    let status = state
        .statuses
        .lock()
        .expect("sink lock")
        .pop_front()
        .unwrap_or(200);
    StatusCode::from_u16(status).expect("a scripted status is a valid status")
}
