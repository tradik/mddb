use std::sync::Arc;

use axum::Json;
use axum::extract::State;
use axum::http::StatusCode;
use serde_json::{Value, json};

use crate::chat::scenario;
use crate::state::AppState;

/// Readiness: 200 once mddbd has been reached, 503 until then (#401).
///
/// The server starts without mddbd and connects in the background, so the
/// process being up no longer implies it can answer. A probe pointed here
/// keeps traffic away until it can, without restarting the pod.
///
/// `scenarios_source` says where scenarios are read from (#403): `"mddbd"`
/// once its scenario store has been loaded, `"toml"` until then — and for
/// good next to an mddbd older than 2.19.0. `scenarios_version` is the
/// version of that store the loaded scenarios are at, `null` under `"toml"`.
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
        "scenarios_source": state.scenarios.source(),
        "scenarios_version": state.scenarios.version(),
    });
    (code, Json(body))
}

pub async fn config_info(State(state): State<Arc<AppState>>) -> Json<Value> {
    // RUST-001: a scenario has two names — the map key that identifies it and
    // the `name` a TOML sets for people to read. Nothing read the second, so
    // the two silently disagreed whenever an operator set one. The key stays
    // the identifier; `name` is now the label, exposed beside it so a picker
    // can show something other than a slug.
    //
    // #403: the scenarios a visitor can join right now — the TOML ones with
    // mddbd's laid over them, where the label is the scenario's description.
    let catalog = scenario::catalog(&state.config, &state.scenarios);
    let scenarios: Vec<&str> = catalog.keys().map(|k| k.as_str()).collect();
    let scenario_labels: serde_json::Map<String, Value> = catalog
        .iter()
        .map(|(key, label)| (key.clone(), Value::from(label.clone())))
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
    use crate::chat::scenario::sync::sync_once;
    use crate::grpc::client::proto::ChatScenarioProto;
    use crate::grpc::link::MddbLink;
    use crate::grpc::testing::{FakeMddbd, scenario};
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
        assert_eq!(body["scenarios_source"], "toml");
        assert_eq!(body["scenarios_version"], Value::Null);
    }

    // #403: where the scenarios come from, and which version of mddbd's
    // store they are at.
    #[tokio::test]
    async fn health_and_config_follow_the_scenarios_loaded_from_mddbd() {
        let mddbd = FakeMddbd::start().await;
        mddbd.put_scenario(
            ChatScenarioProto {
                description: "Sales desk".to_string(),
                ..scenario("sales", &[])
            },
            "You sell.",
        );
        mddbd.put_scenario(
            ChatScenarioProto {
                enabled: false,
                ..scenario("support", &[])
            },
            "Off.",
        );
        let config = test_config(&mddbd.grpc_addr());
        let state = test_state_with_link(config.clone(), llm(&config), mddbd.link().await);
        sync_once(&state.mddb, &state.scenarios).await;

        let (code, Json(body)) = health(State(state.clone())).await;
        assert_eq!(code, StatusCode::OK);
        assert_eq!(body["scenarios_source"], "mddbd");
        assert_eq!(body["scenarios_version"], 2);

        let Json(body) = config_info(State(state)).await;
        assert_eq!(body["scenarios"], json!(["assistant", "sales"]));
        assert_eq!(body["scenario_labels"]["sales"], "Sales desk");
        assert_eq!(body["scenario_labels"]["assistant"], "Assistant");
        assert_eq!(body["scenario_labels"].get("support"), None);
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
