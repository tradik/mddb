use std::sync::Arc;

use axum::extract::State;
use axum::http::header;
use axum::response::IntoResponse;

use crate::state::AppState;

/// The Prometheus text format's content type.
const CONTENT_TYPE: &str = "text/plain; version=0.0.4; charset=utf-8";

/// `/metrics`: the server's counters for a Prometheus scrape (#413).
pub async fn metrics(State(state): State<Arc<AppState>>) -> impl IntoResponse {
    (
        [(header::CONTENT_TYPE, CONTENT_TYPE)],
        state.metrics.render(),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::llm::openai::OpenAiProvider;
    use crate::test_support::{test_config, test_state_with_link};

    #[tokio::test]
    async fn metrics_are_served_as_prometheus_text() {
        let config = test_config("http://127.0.0.1:1");
        let llm = Arc::new(OpenAiProvider::new(config.llm.clone()));
        let state = test_state_with_link(config, llm, Default::default());
        state.metrics.loop_guard_triggered("support");

        let response = metrics(State(state)).await.into_response();

        assert_eq!(response.headers()[header::CONTENT_TYPE], CONTENT_TYPE);
        let body = axum::body::to_bytes(response.into_body(), usize::MAX)
            .await
            .unwrap();
        let text = String::from_utf8(body.to_vec()).unwrap();
        assert!(text.contains("mddb_chat_loop_guard_triggered_total{scenario=\"support\"} 1"));
    }
}
