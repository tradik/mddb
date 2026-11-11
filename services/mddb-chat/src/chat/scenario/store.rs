//! The scenarios last read from mddbd (#403).

use std::collections::HashMap;
use std::sync::{Arc, RwLock, RwLockReadGuard};

use super::StoredScenario;

/// What one reload produced. Replaced whole, never edited, so a reader sees
/// either the scenarios before a reload or the ones after it.
#[derive(Default)]
struct Snapshot {
    /// mddbd's version counter at the reload; `None` until the first one.
    version: Option<u64>,
    scenarios: HashMap<String, Arc<StoredScenario>>,
}

/// A shared, swappable snapshot of mddbd's scenarios.
///
/// Empty until the first successful load, and for as long as mddbd is
/// unreachable or too old to store scenarios — which is when the TOML
/// scenarios serve alone.
#[derive(Clone, Default)]
pub struct ScenarioStore {
    snapshot: Arc<RwLock<Snapshot>>,
}

impl ScenarioStore {
    /// A panic while the lock was held cannot leave a half-written snapshot —
    /// it is swapped in one assignment — so a poisoned lock is still good.
    fn read(&self) -> RwLockReadGuard<'_, Snapshot> {
        self.snapshot.read().unwrap_or_else(|e| e.into_inner())
    }

    /// Puts a freshly loaded set in place of the previous one.
    pub fn replace(&self, version: u64, scenarios: HashMap<String, StoredScenario>) {
        let scenarios = scenarios
            .into_iter()
            .map(|(name, scenario)| (name, Arc::new(scenario)))
            .collect();
        *self.snapshot.write().unwrap_or_else(|e| e.into_inner()) = Snapshot {
            version: Some(version),
            scenarios,
        };
    }

    pub fn get(&self, name: &str) -> Option<Arc<StoredScenario>> {
        self.read().scenarios.get(name).cloned()
    }

    /// Every stored scenario, disabled ones included.
    pub fn all(&self) -> Vec<(String, Arc<StoredScenario>)> {
        self.read()
            .scenarios
            .iter()
            .map(|(name, scenario)| (name.clone(), scenario.clone()))
            .collect()
    }

    /// The version of mddbd's scenario store this snapshot was read at.
    pub fn version(&self) -> Option<u64> {
        self.read().version
    }

    /// Where scenarios are read from right now: `"mddbd"` once its store has
    /// been loaded, `"toml"` before that.
    pub fn source(&self) -> &'static str {
        match self.version() {
            Some(_) => "mddbd",
            None => "toml",
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // The lock is only ever held for one assignment or one lookup, so a
    // thread that died holding it left a whole snapshot behind.
    #[test]
    fn a_poisoned_lock_still_serves_and_still_reloads() {
        let store = ScenarioStore::default();
        let poisoner = store.clone();
        let died = std::thread::spawn(move || {
            let _held = poisoner.snapshot.write().expect("first to lock");
            std::panic::resume_unwind(Box::new("die holding the lock"));
        })
        .join();
        assert!(died.is_err());
        assert!(store.snapshot.is_poisoned());

        assert_eq!(store.version(), None);
        store.replace(3, HashMap::new());
        assert_eq!(store.version(), Some(3));
    }
}
