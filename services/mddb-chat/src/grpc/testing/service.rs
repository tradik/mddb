//! The fake mddbd's wire side: an axum router that answers the login and
//! routes gRPC paths to scripted answers.

use std::future::{Ready, ready};

use axum::Router;
use axum::body::Body;
use axum::extract::State;
use axum::http::{Request, Response, StatusCode};
use axum::response::IntoResponse;
use axum::routing::post;
use tonic::Status;
use tonic::server::UnaryService;
use tonic_prost::ProstCodec;

use super::{Call, FakeState, Login, Shared, conversations};
use crate::grpc::client::proto;

pub fn router(state: Shared) -> Router {
    Router::new()
        .route("/v1/auth/login", post(login))
        .fallback(grpc)
        .with_state(state)
}

async fn login(State(state): State<Shared>) -> axum::response::Response {
    let mode = state.lock().expect("fake mddbd state").login.clone();
    match mode {
        Login::Token(token) => axum::Json(serde_json::json!({ "token": token })).into_response(),
        Login::Refused => (StatusCode::UNAUTHORIZED, "bad credentials").into_response(),
        Login::NotJson => "not json".into_response(),
        // Unwinding out of the handler drops the connection, which is what a
        // client sees when the server dies mid-request. `resume_unwind` skips
        // the panic hook, so the test output stays clean.
        Login::HangUp => std::panic::resume_unwind(Box::new("hang up")),
    }
}

/// One scripted method: records the call, fails if the test said so, else
/// answers from the state.
struct Scripted<Req, Resp> {
    state: Shared,
    method: String,
    answer: fn(&mut FakeState, Req) -> Result<Resp, Status>,
}

impl<Req: std::fmt::Debug, Resp> UnaryService<Req> for Scripted<Req, Resp> {
    type Response = Resp;
    type Future = Ready<Result<tonic::Response<Resp>, Status>>;

    fn call(&mut self, request: tonic::Request<Req>) -> Self::Future {
        let authorization = request
            .metadata()
            .get("authorization")
            .and_then(|v| v.to_str().ok())
            .map(str::to_string);
        let request = request.into_inner();
        let mut state = self.state.lock().expect("fake mddbd state");
        state.calls.push(Call {
            method: self.method.clone(),
            authorization,
            request: format!("{request:?}"),
        });
        let answer = match state.failing {
            Some(code) => Err(Status::new(code, "scripted failure")),
            None => (self.answer)(&mut state, request),
        };
        ready(answer.map(tonic::Response::new))
    }
}

async fn unary<Req, Resp>(
    state: Shared,
    method: &str,
    request: Request<Body>,
    answer: fn(&mut FakeState, Req) -> Result<Resp, Status>,
) -> Response<Body>
where
    Req: prost::Message + Default + std::fmt::Debug + 'static,
    Resp: prost::Message + 'static,
{
    let scripted = Scripted {
        state,
        method: method.to_string(),
        answer,
    };
    tonic::server::Grpc::new(ProstCodec::<Resp, Req>::default())
        .unary(scripted, request)
        .await
        .map(Body::new)
}

async fn grpc(State(state): State<Shared>, request: Request<Body>) -> Response<Body> {
    let path = request.uri().path().to_string();
    let method = path.trim_start_matches("/mddb.MDDB/");
    match method {
        "ListChatScenarios" => unary(state, method, request, list_scenarios).await,
        "GetChatScenarioPrompt" => unary(state, method, request, scenario_prompt).await,
        "GetCollectionConfig" => unary(state, method, request, collection_config).await,
        "HybridSearch" => unary(state, method, request, hybrid_search).await,
        "VectorSearch" => unary(state, method, request, vector_search).await,
        "FTS" => unary(state, method, request, fts).await,
        "CreateChatConversation" => unary(state, method, request, conversations::create).await,
        "ListChatConversations" => unary(state, method, request, conversations::list).await,
        "GetChatConversation" => unary(state, method, request, conversations::get).await,
        "AppendChatMessages" => unary(state, method, request, conversations::append).await,
        "EndChatConversation" => unary(state, method, request, conversations::end).await,
        "RateChatMessage" => unary(state, method, request, conversations::rate).await,
        "DeleteChatConversationsForUser" => {
            unary(state, method, request, conversations::delete_for_user).await
        }
        "ListFeatures" => unary(state, method, request, super::features::list_features).await,
        "CallChatMcpTool" => unary(state, method, request, super::mcp::call_tool).await,
        _ => Status::unimplemented(format!("unknown method {path}")).into_http(),
    }
}

fn list_scenarios(
    state: &mut FakeState,
    _: proto::ListChatScenariosRequest,
) -> Result<proto::ListChatScenariosResponse, Status> {
    Ok(proto::ListChatScenariosResponse {
        scenarios: state.scenarios.values().cloned().collect(),
        version: state.version,
    })
}

fn scenario_prompt(
    state: &mut FakeState,
    request: proto::GetChatScenarioPromptRequest,
) -> Result<proto::GetChatScenarioPromptResponse, Status> {
    let prompt = state
        .prompts
        .get(&request.name)
        .cloned()
        .ok_or_else(|| Status::not_found("chat scenario not found"))?;
    let mcp_servers = state
        .mcp_states
        .get(&request.name)
        .cloned()
        .unwrap_or_default();
    Ok(proto::GetChatScenarioPromptResponse {
        name: request.name,
        prompt,
        version: state.version,
        mcp_servers,
        // #404 added the prompt's size and missing skills; mddb-chat reads
        // the prompt and, since MCP-005, the servers' states.
        ..Default::default()
    })
}

fn collection_config(
    state: &mut FakeState,
    request: proto::GetCollectionConfigRequest,
) -> Result<proto::GetCollectionConfigResponse, Status> {
    let config = state.collections.get(&request.collection).cloned();
    Ok(proto::GetCollectionConfigResponse {
        configured: config.is_some(),
        collection: request.collection,
        config,
        ..Default::default()
    })
}

/// The scripted hits as documents; an empty key is a hit without one.
fn documents(state: &FakeState) -> impl Iterator<Item = (Option<proto::Document>, f32)> + '_ {
    state.hits.iter().map(|(key, content, score)| {
        let document = (!key.is_empty()).then(|| proto::Document {
            key: key.clone(),
            content_md: content.clone(),
            ..Default::default()
        });
        (document, *score)
    })
}

fn hybrid_search(
    state: &mut FakeState,
    _: proto::HybridSearchRequest,
) -> Result<proto::HybridSearchResponse, Status> {
    let results = documents(state)
        .map(|(document, score)| proto::HybridSearchResult {
            document,
            combined_score: f64::from(score),
            ..Default::default()
        })
        .collect();
    Ok(proto::HybridSearchResponse {
        results,
        ..Default::default()
    })
}

fn vector_search(
    state: &mut FakeState,
    _: proto::VectorSearchRequest,
) -> Result<proto::VectorSearchResponse, Status> {
    let results = documents(state)
        .map(|(document, score)| proto::VectorSearchResult {
            document,
            score,
            ..Default::default()
        })
        .collect();
    Ok(proto::VectorSearchResponse {
        results,
        ..Default::default()
    })
}

fn fts(state: &mut FakeState, _: proto::FtsRequest) -> Result<proto::FtsResponse, Status> {
    let results = documents(state)
        .map(|(document, score)| proto::FtsResult {
            document,
            score: f64::from(score),
            ..Default::default()
        })
        .collect();
    Ok(proto::FtsResponse {
        results,
        ..Default::default()
    })
}
