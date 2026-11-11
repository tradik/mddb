//! The fake mddbd's module switches (GO-049): what `ListFeatures` answers.

use tonic::{Code, Status};

use super::{FakeMddbd, FakeState};
use crate::grpc::client::proto;

/// How the fake answers `ListFeatures`.
#[derive(Clone, Copy)]
pub enum FakeFeatures {
    /// UNIMPLEMENTED, as an mddbd older than the switches does. The default,
    /// so a test that does not script the switches sees such an mddbd.
    Unimplemented,
    /// The call fails with this code.
    Failing(Code),
    /// `chat` and `chatExternalMcp` in effect as given.
    List { chat: bool, chat_external_mcp: bool },
}

impl FakeMddbd {
    pub fn set_features(&self, features: FakeFeatures) {
        self.state().features = features;
    }
}

fn feature(name: &str, enabled: bool) -> proto::FeatureProto {
    proto::FeatureProto {
        name: name.to_string(),
        enabled,
        ..Default::default()
    }
}

pub(super) fn list_features(
    state: &mut FakeState,
    _: proto::ListFeaturesRequest,
) -> Result<proto::ListFeaturesResponse, Status> {
    match state.features {
        FakeFeatures::Unimplemented => Err(Status::unimplemented("unknown method ListFeatures")),
        FakeFeatures::Failing(code) => Err(Status::new(code, "scripted failure")),
        FakeFeatures::List {
            chat,
            chat_external_mcp,
        } => Ok(proto::ListFeaturesResponse {
            features: vec![
                feature("chat", chat),
                feature("chatExternalMcp", chat_external_mcp),
                feature("vector", true),
            ],
            version: 1,
        }),
    }
}
