//! `MddbClient` against the fake mddbd: connecting, logging in, the three
//! searches, the per-collection config and the chat scenarios (#403).

use super::*;
use crate::grpc::testing::{FakeMddbd, Login, scenario};

/// The fake's `[mddb]` section with `extra` TOML keys added. The fake
/// answers the login on its gRPC port, so `http_url` names that port.
fn config(fake: &FakeMddbd, extra: &str) -> MddbConfig {
    let addr = fake.grpc_addr();
    toml::from_str(&format!(
        "grpc_addr = \"{addr}\"\nhttp_url = \"{addr}/\"\n{extra}"
    ))
    .expect("the mddb config parses")
}

const CREDENTIALS: &str = "auth_username = \"chat\"\nauth_password = \"secret\"";

async fn connect_error(fake: &FakeMddbd) -> String {
    match MddbClient::connect(config(fake, CREDENTIALS)).await {
        Ok(_) => panic!("the connect should have failed"),
        Err(e) => e.to_string(),
    }
}

fn profile(search_type: &str, top_k: i32) -> proto::CollectionConfigProto {
    proto::CollectionConfigProto {
        response_prompt: "Answer in steps.".to_string(),
        retrieval: Some(proto::RetrievalProfileProto {
            default_search_type: search_type.to_string(),
            top_k,
            ..Default::default()
        }),
        ..Default::default()
    }
}

#[tokio::test]
async fn an_address_nobody_listens_on_is_a_connect_error() {
    let reserved = std::net::TcpListener::bind("127.0.0.1:0").expect("reserve");
    let addr = reserved.local_addr().expect("addr");
    drop(reserved);
    let config: MddbConfig =
        toml::from_str(&format!("grpc_addr = \"http://{addr}\"")).expect("parse");

    let err = MddbClient::connect(config).await.err().expect("an error");

    assert!(
        err.to_string().contains("failed to connect to mddbd"),
        "got {err}"
    );
}

#[tokio::test]
async fn without_a_username_no_login_happens_and_calls_carry_no_token() {
    let fake = FakeMddbd::start().await;
    fake.set_login(Login::HangUp);
    let mut client = fake.client().await;

    client.search("q", "docs").await.expect("search");

    assert_eq!(fake.calls().last().expect("a call").authorization, None);
}

#[tokio::test]
async fn the_login_token_travels_with_every_call() {
    let fake = FakeMddbd::start().await;
    let mut client = MddbClient::connect(config(&fake, CREDENTIALS))
        .await
        .expect("connect");

    client.list_chat_scenarios().await.expect("list");

    assert_eq!(
        fake.calls()[0].authorization.as_deref(),
        Some("Bearer fake-token")
    );
}

#[tokio::test]
async fn a_token_that_cannot_be_a_header_is_left_out() {
    let fake = FakeMddbd::start().await;
    fake.set_login(Login::Token("two\nlines".to_string()));
    let mut client = MddbClient::connect(config(&fake, CREDENTIALS))
        .await
        .expect("connect");

    client.list_chat_scenarios().await.expect("list");

    assert_eq!(fake.calls()[0].authorization, None);
}

#[tokio::test]
async fn a_failed_login_fails_the_connect_and_says_why() {
    let fake = FakeMddbd::start().await;

    fake.set_login(Login::Refused);
    assert_eq!(
        connect_error(&fake).await,
        "internal error: mddbd login failed: bad credentials"
    );

    fake.set_login(Login::NotJson);
    let err = connect_error(&fake).await;
    assert!(err.contains("mddbd login parse error"), "got {err}");

    fake.set_login(Login::HangUp);
    let err = connect_error(&fake).await;
    assert!(err.contains("mddbd login failed"), "got {err}");
}

// The login URL used to be grpc_addr with "11024" replaced by "11023", so on
// any other port the login went to the gRPC port. Now it is `http_url`, and
// without one a non-default port is refused by name.
#[tokio::test]
async fn a_non_default_port_without_http_url_names_the_setting() {
    let fake = FakeMddbd::start().await;
    let config: MddbConfig = toml::from_str(&format!(
        "grpc_addr = \"{}\"\n{CREDENTIALS}",
        fake.grpc_addr()
    ))
    .expect("parse");

    let err = MddbClient::connect(config).await.err().expect("an error");

    assert!(
        err.to_string().contains("[mddb] http_url is required"),
        "got {err}"
    );
}

#[tokio::test]
async fn a_search_is_hybrid_with_five_results_unless_told_otherwise() {
    let fake = FakeMddbd::start().await;
    fake.set_hits(&[("api", "# API", 0.9), ("", "no document", 0.5)]);
    let mut client = fake.client().await;

    let hits = client.search("export", "docs").await.expect("search");

    assert_eq!(hits.len(), 1, "a hit without a document is dropped");
    assert_eq!(hits[0].key, "api");
    assert_eq!(hits[0].content, "# API");
    assert!((hits[0].score - 0.9).abs() < 1e-6);
    let call = fake.calls().pop().expect("a call");
    assert_eq!(call.method, "HybridSearch");
    assert!(call.request.contains("query: \"export\""), "{call:?}");
    assert!(call.request.contains("top_k: 5"), "{call:?}");
}

#[tokio::test]
async fn the_toml_picks_the_search_type_and_size() {
    let fake = FakeMddbd::start().await;
    fake.set_hits(&[("api", "# API", 0.25), ("", "", 0.1)]);
    fake.set_collection("docs", profile("hybrid", 9));

    for (search_type, method, size) in [
        ("vector", "VectorSearch", "top_k: 3"),
        ("fts", "FTS", "limit: 3"),
    ] {
        let toml = format!("search_type = \"{search_type}\"\nsearch_top_k = 3");
        let mut client = MddbClient::connect(config(&fake, &toml))
            .await
            .expect("connect");

        let hits = client.search("q", "docs").await.expect("search");

        assert_eq!(hits.len(), 1);
        assert!((hits[0].score - 0.25).abs() < 1e-6);
        let call = fake.calls().pop().expect("a call");
        assert_eq!(call.method, method);
        assert!(call.request.contains(size), "{call:?}");
    }
    assert_eq!(
        fake.count("GetCollectionConfig"),
        0,
        "with both set in the TOML the profile is not asked for"
    );
}

#[tokio::test]
async fn the_collection_profile_fills_what_the_toml_leaves_out() {
    let fake = FakeMddbd::start().await;
    fake.set_collection("docs", profile("fts", 7));
    fake.set_collection("blank", profile("", 0));
    let mut client = fake.client().await;

    client.search("q", "docs").await.expect("search");
    let call = fake.calls().pop().expect("a call");
    assert_eq!(call.method, "FTS");
    assert!(call.request.contains("limit: 7"), "{call:?}");

    // A profile that says nothing leaves the built-in defaults.
    client.search("q", "blank").await.expect("search");
    let call = fake.calls().pop().expect("a call");
    assert_eq!(call.method, "HybridSearch");
    assert!(call.request.contains("top_k: 5"), "{call:?}");
}

#[tokio::test]
async fn an_unknown_search_type_and_a_failing_mddbd_are_errors() {
    let fake = FakeMddbd::start().await;
    let mut client = MddbClient::connect(config(&fake, "search_type = \"psychic\""))
        .await
        .expect("connect");
    let err = client.search("q", "docs").await.expect_err("an error");
    assert_eq!(
        err.to_string(),
        "internal error: unknown search type: psychic"
    );

    fake.fail_with(Some(tonic::Code::Unavailable));
    for search_type in ["hybrid", "vector", "fts"] {
        let toml = format!("search_type = \"{search_type}\"\nsearch_top_k = 3");
        let mut client = MddbClient::connect(config(&fake, &toml))
            .await
            .expect("connect");
        let err = client.search("q", "docs").await.expect_err("an error");
        assert!(matches!(err, AppError::GrpcError(_)), "got {err}");
    }
}

#[tokio::test]
async fn a_response_prompt_is_asked_for_once_per_collection() {
    let fake = FakeMddbd::start().await;
    fake.set_collection("docs", profile("hybrid", 5));
    let mut client = fake.client().await;

    assert_eq!(client.response_prompt("docs").await, "Answer in steps.");
    assert_eq!(client.response_prompt("docs").await, "Answer in steps.");
    assert_eq!(client.response_prompt("unconfigured").await, "");
    assert_eq!(client.response_prompt("unconfigured").await, "");

    assert_eq!(fake.count("GetCollectionConfig"), 2);
}

#[tokio::test]
async fn a_failed_config_lookup_is_an_empty_prompt_not_an_error() {
    let fake = FakeMddbd::start().await;
    fake.fail_with(Some(tonic::Code::Internal));
    let mut client = fake.client().await;

    assert_eq!(client.response_prompt("docs").await, "");
}

// #403.
#[tokio::test]
async fn scenarios_come_with_the_store_version_and_a_composed_prompt() {
    let fake = FakeMddbd::start().await;
    fake.put_scenario(scenario("support", &["docs"]), "You are Ada.");
    fake.put_scenario(scenario("sales", &[]), "You sell.");
    let mut client = fake.client().await;

    let list = client.list_chat_scenarios().await.expect("list");
    let names: Vec<&str> = list.scenarios.iter().map(|s| s.name.as_str()).collect();
    assert_eq!(list.version, 2);
    assert_eq!(names, vec!["sales", "support"]);

    let prompt = client
        .chat_scenario_prompt("support")
        .await
        .expect("prompt");
    assert_eq!(prompt, "You are Ada.");
    let call = fake.calls().pop().expect("a call");
    assert_eq!(call.method, "GetChatScenarioPrompt");
    assert!(call.request.contains("name: \"support\""), "{call:?}");
}

#[tokio::test]
async fn an_mddbd_without_scenarios_answers_with_a_grpc_error() {
    let fake = FakeMddbd::start().await;
    let mut client = fake.client().await;

    let missing = client.chat_scenario_prompt("nobody").await.err();
    assert!(matches!(missing, Some(AppError::GrpcError(s)) if s.code() == tonic::Code::NotFound));

    fake.fail_with(Some(tonic::Code::Unimplemented));
    let old = client.list_chat_scenarios().await.err();
    assert!(matches!(old, Some(AppError::GrpcError(s)) if s.code() == tonic::Code::Unimplemented));
}

// The fake itself: a method it does not script is unimplemented, as on an
// mddbd that predates it.
#[tokio::test]
async fn the_fake_answers_unimplemented_for_anything_else() {
    let fake = FakeMddbd::start().await;
    let mut client = fake.client().await;

    let status = client
        .client
        .get_chat_scenario(proto::GetChatScenarioRequest {
            name: "support".to_string(),
        })
        .await
        .expect_err("an error");

    assert_eq!(status.code(), tonic::Code::Unimplemented);
    assert!(
        fake.calls().is_empty(),
        "an unscripted call is not recorded"
    );
}
