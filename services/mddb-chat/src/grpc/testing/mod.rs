//! A stand-in for mddbd that tests script (#403).
//!
//! One loopback port answers both halves of what `MddbClient` talks to: the
//! HTTP login and the handful of gRPC methods mddb-chat calls. Every other
//! method is unimplemented, the way an mddbd older than the method is.
//! Compiled only for tests.

mod service;

use std::collections::{BTreeMap, HashMap};
use std::net::SocketAddr;
use std::sync::{Arc, Mutex, MutexGuard};

use crate::config::MddbConfig;
use crate::grpc::client::{MddbClient, proto};
use crate::grpc::link::MddbLink;

/// What `POST /v1/auth/login` does.
#[derive(Clone)]
pub enum Login {
    Token(String),
    /// 401 with a body.
    Refused,
    /// 200 with something that is not the login answer.
    NotJson,
    /// The connection is dropped before any answer.
    HangUp,
}

/// One gRPC call the fake received.
#[derive(Debug, Clone)]
pub struct Call {
    pub method: String,
    pub authorization: Option<String>,
    /// The request message, as `Debug` prints it.
    pub request: String,
}

pub(super) struct FakeState {
    pub version: u64,
    pub scenarios: BTreeMap<String, proto::ChatScenarioProto>,
    pub prompts: HashMap<String, String>,
    pub collections: HashMap<String, proto::CollectionConfigProto>,
    /// What every search returns: key, content, score. An empty key is a hit
    /// without a document.
    pub hits: Vec<(String, String, f32)>,
    /// When set, every gRPC call fails with this code.
    pub failing: Option<tonic::Code>,
    pub login: Login,
    pub calls: Vec<Call>,
}

pub(super) type Shared = Arc<Mutex<FakeState>>;

#[derive(Clone)]
pub struct FakeMddbd {
    pub addr: SocketAddr,
    state: Shared,
}

impl FakeMddbd {
    pub async fn start() -> Self {
        let state = Arc::new(Mutex::new(FakeState {
            version: 0,
            scenarios: BTreeMap::new(),
            prompts: HashMap::new(),
            collections: HashMap::new(),
            hits: Vec::new(),
            failing: None,
            login: Login::Token("fake-token".to_string()),
            calls: Vec::new(),
        }));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind the fake mddbd");
        let addr = listener.local_addr().expect("local addr");
        let app = service::router(state.clone());
        tokio::spawn(async move { axum::serve(listener, app).await });
        Self { addr, state }
    }

    fn state(&self) -> MutexGuard<'_, FakeState> {
        self.state.lock().expect("fake mddbd state")
    }

    pub fn grpc_addr(&self) -> String {
        format!("http://{}", self.addr)
    }

    /// The `[mddb]` section pointing at this fake, without credentials.
    pub fn config(&self) -> MddbConfig {
        toml::from_str(&format!("grpc_addr = \"{}\"", self.grpc_addr()))
            .expect("a minimal mddb config parses")
    }

    pub async fn client(&self) -> MddbClient {
        MddbClient::connect(self.config())
            .await
            .expect("connect to the fake mddbd")
    }

    pub async fn link(&self) -> MddbLink {
        MddbLink::connected(self.client().await)
    }

    /// Stores a scenario with its composed prompt, as a `PUT` would.
    pub fn put_scenario(&self, scenario: proto::ChatScenarioProto, prompt: &str) {
        let mut state = self.state();
        state.version += 1;
        state
            .prompts
            .insert(scenario.name.clone(), prompt.to_string());
        state.scenarios.insert(scenario.name.clone(), scenario);
    }

    pub fn delete_scenario(&self, name: &str) {
        let mut state = self.state();
        state.version += 1;
        state.scenarios.remove(name);
        state.prompts.remove(name);
    }

    /// Changes what the composed prompt reads without touching the version,
    /// which is what editing a collection's `responsePrompt` does. `None`
    /// makes the prompt lookup fail while the scenario stays listed.
    pub fn set_prompt(&self, name: &str, prompt: Option<&str>) {
        let mut state = self.state();
        match prompt {
            Some(prompt) => state.prompts.insert(name.to_string(), prompt.to_string()),
            None => state.prompts.remove(name),
        };
    }

    pub fn set_collection(&self, name: &str, config: proto::CollectionConfigProto) {
        self.state().collections.insert(name.to_string(), config);
    }

    pub fn set_hits(&self, hits: &[(&str, &str, f32)]) {
        self.state().hits = hits
            .iter()
            .map(|(key, content, score)| (key.to_string(), content.to_string(), *score))
            .collect();
    }

    pub fn fail_with(&self, code: Option<tonic::Code>) {
        self.state().failing = code;
    }

    pub fn set_login(&self, login: Login) {
        self.state().login = login;
    }

    pub fn calls(&self) -> Vec<Call> {
        self.state().calls.clone()
    }

    /// How many times `method` was called.
    pub fn count(&self, method: &str) -> usize {
        self.state()
            .calls
            .iter()
            .filter(|c| c.method == method)
            .count()
    }
}

/// An enabled scenario named `name` over `collections`, everything else at
/// its default.
pub fn scenario(name: &str, collections: &[&str]) -> proto::ChatScenarioProto {
    proto::ChatScenarioProto {
        name: name.to_string(),
        enabled: true,
        allowed_collections: collections.iter().map(|c| c.to_string()).collect(),
        ..Default::default()
    }
}
