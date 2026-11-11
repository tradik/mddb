use std::sync::Arc;

use axum::Json;
use axum::extract::State;
use axum::http::StatusCode;
use serde_json::{Value, json};

use crate::state::AppState;

/// Readiness: 200 once mddbd has been reached, 503 until then (#401).
///
/// The server starts without mddbd and connects in the background, so the
/// process being up no longer implies it can answer. A probe pointed here
/// keeps traffic away until it can, without restarting the pod.
pub async fn health(State(state): State<Arc<AppState>>) -> (StatusCode, Json<Value>) {
    let connected = state.mddb.is_connected().await;
    let (code, status) = if connected {
        (StatusCode::OK, "ok")
    } else {
        (StatusCode::SERVICE_UNAVAILABLE, "starting")
    };
    let body = json!({
        "status": status,
        "service": "mddb-chat",
        "mddb_connected": connected,
        "active_sessions": state.session_manager.attached_count(),
        "detached_sessions": state.session_manager.detached_count(),
        "queue_length": state.session_manager.queue_len().await,
        "max_concurrent": state.config.session.max_concurrent,
    });
    (code, Json(body))
}

pub async fn config_info(State(state): State<Arc<AppState>>) -> Json<Value> {
    let scenarios: Vec<&str> = state.config.scenarios.keys().map(|k| k.as_str()).collect();
    // RUST-001: a scenario has two names — the map key that identifies it and
    // the `name` a TOML sets for people to read. Nothing read the second, so
    // the two silently disagreed whenever an operator set one. The key stays
    // the identifier; `name` is now the label, exposed beside it so a picker
    // can show something other than a slug.
    let scenario_labels: serde_json::Map<String, Value> = state
        .config
        .scenarios
        .iter()
        .map(|(key, scenario)| (key.clone(), Value::from(scenario.name.clone())))
        .collect();
    Json(json!({
        "scenarios": scenarios,
        "scenario_labels": scenario_labels,
        "max_concurrent": state.config.session.max_concurrent,
        "queue_size": state.config.session.queue_size,
        "max_message_length": state.config.security.max_message_length,
        "name_max_chars": state.config.session.name_max_chars,
    }))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::grpc::link::MddbLink;
    use crate::llm::openai::OpenAiProvider;
    use crate::test_support::{stub_http_server, test_config, test_state, test_state_with_link};

    fn llm(config: &crate::config::Config) -> Arc<OpenAiProvider> {
        Arc::new(OpenAiProvider::new(config.llm.clone()))
    }

    // #401: the pod is alive, the knowledge base is not there yet.
    #[tokio::test]
    async fn health_is_503_until_mddbd_is_reached() {
        let config = test_config("http://127.0.0.1:1");
        let state = test_state_with_link(config.clone(), llm(&config), MddbLink::default());

        let (code, Json(body)) = health(State(state)).await;

        assert_eq!(code, StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(body["status"], "starting");
        assert_eq!(body["mddb_connected"], false);
        assert_eq!(body["service"], "mddb-chat");
    }

    #[tokio::test]
    async fn health_is_200_with_session_counts_once_connected() {
        let addr = stub_http_server().await;
        let config = test_config(&format!("http://{addr}"));
        let state = test_state(config.clone(), llm(&config)).await;
        let id = crate::session::manager_tests::admitted(
            state
                .session_manager
                .join("Ada".into(), "assistant".into())
                .await,
        );
        state
            .session_manager
            .join("Grace".into(), "assistant".into())
            .await;
        state.session_manager.detach(&id).await;

        let (code, Json(body)) = health(State(state)).await;

        assert_eq!(code, StatusCode::OK);
        assert_eq!(body["status"], "ok");
        assert_eq!(body["mddb_connected"], true);
        assert_eq!(body["active_sessions"], 1);
        assert_eq!(body["detached_sessions"], 1);
        assert_eq!(body["queue_length"], 0);
        assert_eq!(body["max_concurrent"], 2);
    }

    #[tokio::test]
    async fn config_info_lists_scenarios_with_their_labels_and_the_limits() {
        let config = test_config("http://127.0.0.1:1");
        let state = test_state_with_link(config.clone(), llm(&config), MddbLink::default());

        let Json(body) = config_info(State(state)).await;

        let mut scenarios: Vec<&str> = body["scenarios"]
            .as_array()
            .expect("array")
            .iter()
            .filter_map(Value::as_str)
            .collect();
        scenarios.sort_unstable();
        assert_eq!(scenarios, vec!["assistant", "support"]);
        assert_eq!(body["scenario_labels"]["support"], "Support");
        assert_eq!(body["queue_size"], 2);
        assert_eq!(body["max_message_length"], 2000);
        assert_eq!(body["name_max_chars"], 40);
    }
}
