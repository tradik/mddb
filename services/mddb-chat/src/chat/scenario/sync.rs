//! Keeping the scenario store in step with mddbd (#403).
//!
//! mddbd keeps one version counter for all its scenarios. This asks for it
//! every `[scenarios_source] poll_seconds` and reloads the scenarios when it
//! moved, so an edit in mddb-panel reaches the chat without a restart. Open
//! sessions are untouched: a session is bound to a scenario by name, and the
//! next turn looks the name up again.

use std::collections::HashMap;
use std::time::Duration;

use tracing::{debug, info, warn};

use super::{ScenarioStore, StoredScenario};
use crate::error::AppError;
use crate::grpc::client::MddbClient;
use crate::grpc::link::MddbLink;

/// How soon to look again while mddbd has not been reached yet, so the
/// scenarios arrive right after the link does and not a poll interval later.
const NOT_CONNECTED_RETRY: Duration = Duration::from_millis(500);

/// What one look at mddbd came to.
#[derive(Debug, PartialEq, Eq)]
pub enum Sync {
    /// The link to mddbd is not up yet.
    NotConnected,
    /// mddbd did not answer, or is older than 2.19.0 and has no scenario
    /// store. The scenarios already loaded stay in force.
    Failed,
    /// The version has not moved.
    Unchanged,
    /// This many scenarios were loaded.
    Reloaded(usize),
}

/// Looks at mddbd once and reloads the store if its version changed.
pub async fn sync_once(link: &MddbLink, store: &ScenarioStore) -> Sync {
    let Some(mut client) = link.client().await else {
        return Sync::NotConnected;
    };
    match reload(&mut client, store).await {
        Ok(outcome) => outcome,
        Err(AppError::GrpcError(status)) if status.code() == tonic::Code::Unimplemented => {
            debug!("mddbd has no scenario store (older than 2.19.0); the TOML scenarios serve");
            Sync::Failed
        }
        Err(e) => {
            warn!(error = %e, "could not read the scenarios from mddbd; keeping the ones loaded");
            Sync::Failed
        }
    }
}

/// All or nothing: a scenario whose prompt cannot be fetched fails the whole
/// reload, and the next poll tries again, rather than serving a set that
/// mixes two versions.
async fn reload(client: &mut MddbClient, store: &ScenarioStore) -> Result<Sync, AppError> {
    let list = client.list_chat_scenarios().await?;
    if store.version() == Some(list.version) {
        return Ok(Sync::Unchanged);
    }

    let mut scenarios = HashMap::with_capacity(list.scenarios.len());
    for scenario in list.scenarios {
        let prompt = client.chat_scenario_prompt(&scenario.name).await?;
        scenarios.insert(
            scenario.name.clone(),
            StoredScenario::from_proto(scenario, prompt),
        );
    }

    let count = scenarios.len();
    store.replace(list.version, scenarios);
    info!(version = list.version, count, "scenarios loaded from mddbd");
    Ok(Sync::Reloaded(count))
}

/// Polls mddbd every `every`, for as long as the server runs.
pub async fn poll(link: MddbLink, store: ScenarioStore, every: Duration) {
    loop {
        let wait = match sync_once(&link, &store).await {
            Sync::NotConnected => every.min(NOT_CONNECTED_RETRY),
            _ => every,
        };
        tokio::time::sleep(wait).await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::grpc::testing::{FakeMddbd, scenario};

    #[tokio::test]
    async fn one_look_loads_the_scenarios_and_the_next_finds_nothing_new() {
        let mddbd = FakeMddbd::start().await;
        mddbd.put_scenario(scenario("support", &["kb"]), "You are Ada.");
        let link = mddbd.link().await;
        let store = ScenarioStore::default();

        assert_eq!(sync_once(&link, &store).await, Sync::Reloaded(1));
        assert_eq!(sync_once(&link, &store).await, Sync::Unchanged);

        assert_eq!(store.get("support").expect("loaded").prompt, "You are Ada.");
        assert_eq!(mddbd.count("ListChatScenarios"), 2);
        assert_eq!(mddbd.count("GetChatScenarioPrompt"), 1);
        assert_eq!(
            sync_once(&MddbLink::default(), &store).await,
            Sync::NotConnected
        );
    }

    /// Waits until `store` is at `version`.
    async fn at_version(store: &ScenarioStore, version: u64) {
        let reached = async {
            while store.version() != Some(version) {
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        };
        tokio::time::timeout(Duration::from_secs(2), reached)
            .await
            .expect("the store never reached the version");
    }

    #[tokio::test]
    async fn the_poll_loads_once_mddbd_is_reached_and_follows_its_version() {
        let mddbd = FakeMddbd::start().await;
        mddbd.put_scenario(scenario("support", &["kb"]), "You are Ada.");
        let link = MddbLink::default();
        let store = ScenarioStore::default();
        let task = tokio::spawn(poll(link.clone(), store.clone(), Duration::from_millis(10)));

        tokio::time::sleep(Duration::from_millis(30)).await;
        assert_eq!(store.source(), "toml", "nothing to load from yet");

        link.set(mddbd.client().await).await;
        at_version(&store, 1).await;

        mddbd.put_scenario(scenario("support", &["kb"]), "You are Grace.");
        at_version(&store, 2).await;
        assert_eq!(
            store.get("support").expect("loaded").prompt,
            "You are Grace."
        );
        task.abort();
    }
}
