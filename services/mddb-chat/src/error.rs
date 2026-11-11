use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use serde_json::json;

#[derive(Debug)]
pub enum AppError {
    SessionFull,
    SessionNotFound,
    InvalidMessage(String),
    RateLimited,
    MessageTooLong,
    NameTooLong,
    /// The scenario's `max_turns` has been reached for this session.
    TurnLimitReached(usize),
    /// The session has spent its token budget (RAG-005).
    TokenBudgetExceeded(u64),
    /// The scenario's messages-per-minute limit for one session (#403).
    SessionRateLimited(u32),
    /// The scenario is stored in mddbd with `enabled: false` (#403).
    ScenarioDisabled,
    /// mddbd has the `chat` module switched off (GO-049).
    ChatDisabled,
    GrpcError(tonic::Status),
    /// mddbd has not been reached yet (#401): the server is up, the
    /// knowledge base behind it is not.
    MddbUnavailable,
    /// A join or a request that needs a verified identity did not have one
    /// (#405).
    Unauthorized(String),
    /// No conversation of that id for this user (#405) — or another user's,
    /// which is answered the same way.
    ConversationNotFound,
    /// The conversation was ended; it takes no more messages (#405).
    ConversationEnded,
    LlmError(String),
    Internal(String),
}

impl std::fmt::Display for AppError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            AppError::SessionFull => write!(f, "session queue is full"),
            AppError::SessionNotFound => write!(f, "session not found"),
            AppError::InvalidMessage(msg) => write!(f, "invalid message: {msg}"),
            AppError::RateLimited => write!(f, "rate limited"),
            AppError::MessageTooLong => write!(f, "message too long"),
            AppError::NameTooLong => write!(f, "name too long"),
            AppError::TokenBudgetExceeded(spent) => write!(
                f,
                "this conversation has used its token budget ({spent} tokens)"
            ),
            AppError::TurnLimitReached(max) => {
                write!(f, "this conversation is limited to {max} turns")
            }
            AppError::SessionRateLimited(per_minute) => write!(
                f,
                "this conversation is limited to {per_minute} messages per minute"
            ),
            AppError::ScenarioDisabled => write!(f, "scenario is disabled"),
            AppError::ChatDisabled => write!(f, "chat is disabled on this server"),
            AppError::GrpcError(s) => write!(f, "grpc error: {s}"),
            AppError::MddbUnavailable => write!(
                f,
                "the knowledge base is not connected yet; try again shortly"
            ),
            AppError::Unauthorized(msg) => write!(f, "unauthorized: {msg}"),
            AppError::ConversationNotFound => write!(f, "conversation not found"),
            AppError::ConversationEnded => {
                write!(f, "conversation has ended; start a new one")
            }
            AppError::LlmError(msg) => write!(f, "llm error: {msg}"),
            AppError::Internal(msg) => write!(f, "internal error: {msg}"),
        }
    }
}

impl std::error::Error for AppError {}

impl IntoResponse for AppError {
    fn into_response(self) -> Response {
        let (status, message) = match &self {
            AppError::SessionFull => (StatusCode::SERVICE_UNAVAILABLE, self.to_string()),
            AppError::SessionNotFound => (StatusCode::NOT_FOUND, self.to_string()),
            AppError::InvalidMessage(_) => (StatusCode::BAD_REQUEST, self.to_string()),
            AppError::RateLimited => (StatusCode::TOO_MANY_REQUESTS, self.to_string()),
            AppError::MessageTooLong => (StatusCode::BAD_REQUEST, self.to_string()),
            AppError::NameTooLong => (StatusCode::BAD_REQUEST, self.to_string()),
            // 403 rather than 429: the limit is a property of the scenario and
            // waiting does not lift it, so Retry-After would be a lie.
            AppError::TurnLimitReached(_) => (StatusCode::FORBIDDEN, self.to_string()),
            // Refusal, not backpressure: the conversation is over rather than
            // busy, so there is nothing to retry.
            AppError::TokenBudgetExceeded(_) => (StatusCode::FORBIDDEN, self.to_string()),
            // Waiting helps: the window slides.
            AppError::SessionRateLimited(_) => (StatusCode::TOO_MANY_REQUESTS, self.to_string()),
            // An operator switched it off; asking again changes nothing.
            AppError::ScenarioDisabled => (StatusCode::FORBIDDEN, self.to_string()),
            // The whole service is switched off until an administrator turns
            // it back on, which is what 503 says.
            AppError::ChatDisabled => (StatusCode::SERVICE_UNAVAILABLE, self.to_string()),
            AppError::GrpcError(_) => (StatusCode::BAD_GATEWAY, self.to_string()),
            // Waiting helps: the link comes up on its own.
            AppError::MddbUnavailable => (StatusCode::SERVICE_UNAVAILABLE, self.to_string()),
            AppError::Unauthorized(_) => (StatusCode::UNAUTHORIZED, self.to_string()),
            AppError::ConversationNotFound => (StatusCode::NOT_FOUND, self.to_string()),
            AppError::ConversationEnded => (StatusCode::CONFLICT, self.to_string()),
            AppError::LlmError(_) => (StatusCode::BAD_GATEWAY, self.to_string()),
            AppError::Internal(_) => (StatusCode::INTERNAL_SERVER_ERROR, self.to_string()),
        };

        let body = axum::Json(json!({ "error": message }));
        (status, body).into_response()
    }
}

impl From<tonic::Status> for AppError {
    fn from(status: tonic::Status) -> Self {
        AppError::GrpcError(status)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::body::to_bytes;

    // TEST-001. The status an error maps to is what a client acts on: a
    // retryable failure and a permanent refusal must not look alike.

    #[test]
    fn every_error_says_something_specific() {
        let cases = [
            AppError::SessionFull,
            AppError::SessionNotFound,
            AppError::InvalidMessage("bad shape".into()),
            AppError::RateLimited,
            AppError::MessageTooLong,
            AppError::NameTooLong,
            AppError::TurnLimitReached(10),
            AppError::TokenBudgetExceeded(120_000),
            AppError::SessionRateLimited(20),
            AppError::ScenarioDisabled,
            AppError::MddbUnavailable,
            AppError::Unauthorized("no token".into()),
            AppError::ConversationNotFound,
            AppError::ConversationEnded,
            AppError::LlmError("upstream refused".into()),
            AppError::Internal("boom".into()),
        ];

        for err in cases {
            let text = err.to_string();
            assert!(!text.is_empty(), "an error rendered as an empty string");
            assert!(
                !text.contains("{}") && !text.contains("{:?}"),
                "an unformatted placeholder reached the message: {text}"
            );
        }
    }

    #[test]
    fn the_detail_survives_into_the_message() {
        assert!(
            AppError::InvalidMessage("bad shape".into())
                .to_string()
                .contains("bad shape")
        );
        assert!(
            AppError::LlmError("upstream refused".into())
                .to_string()
                .contains("upstream refused")
        );
        assert!(AppError::TurnLimitReached(10).to_string().contains("10"));
        assert_eq!(
            AppError::SessionRateLimited(20).to_string(),
            "this conversation is limited to 20 messages per minute"
        );
        assert_eq!(
            AppError::ScenarioDisabled.to_string(),
            "scenario is disabled"
        );
        assert_eq!(
            AppError::ChatDisabled.to_string(),
            "chat is disabled on this server"
        );
    }

    #[test]
    fn statuses_separate_the_client_from_the_server() {
        let cases = [
            // The caller can fix these.
            (
                AppError::InvalidMessage("x".into()),
                StatusCode::BAD_REQUEST,
            ),
            (AppError::MessageTooLong, StatusCode::BAD_REQUEST),
            (AppError::NameTooLong, StatusCode::BAD_REQUEST),
            (AppError::SessionNotFound, StatusCode::NOT_FOUND),
            (AppError::Unauthorized("x".into()), StatusCode::UNAUTHORIZED),
            (AppError::ConversationNotFound, StatusCode::NOT_FOUND),
            (AppError::ConversationEnded, StatusCode::CONFLICT),
            // Waiting helps.
            (AppError::RateLimited, StatusCode::TOO_MANY_REQUESTS),
            (
                AppError::SessionRateLimited(20),
                StatusCode::TOO_MANY_REQUESTS,
            ),
            (AppError::SessionFull, StatusCode::SERVICE_UNAVAILABLE),
            (AppError::MddbUnavailable, StatusCode::SERVICE_UNAVAILABLE),
            (AppError::ChatDisabled, StatusCode::SERVICE_UNAVAILABLE),
            // Waiting does not: the scenario caps the conversation, so this is
            // a refusal rather than backpressure.
            (AppError::TurnLimitReached(5), StatusCode::FORBIDDEN),
            (AppError::ScenarioDisabled, StatusCode::FORBIDDEN),
            (
                AppError::TokenBudgetExceeded(120_000),
                StatusCode::FORBIDDEN,
            ),
            // Something behind us failed.
            (AppError::LlmError("x".into()), StatusCode::BAD_GATEWAY),
            (
                AppError::Internal("x".into()),
                StatusCode::INTERNAL_SERVER_ERROR,
            ),
        ];

        for (err, want) in cases {
            let rendered = format!("{err}");
            let status = err.into_response().status();
            assert_eq!(status, want, "{rendered} answered {status}");
        }
    }

    #[tokio::test]
    async fn the_body_is_a_json_object_carrying_the_message() {
        let response = AppError::SessionNotFound.into_response();
        let bytes = to_bytes(response.into_body(), 4096)
            .await
            .expect("read body");
        let body: serde_json::Value = serde_json::from_slice(&bytes).expect("parse body");

        assert_eq!(body["error"], "session not found");
    }

    #[test]
    fn a_grpc_status_converts_without_losing_its_message() {
        let status = tonic::Status::not_found("collection is gone");
        let err: AppError = status.into();

        assert!(err.to_string().contains("collection is gone"));
        // A failure from the database is upstream of us, not our own fault.
        assert_eq!(err.into_response().status(), StatusCode::BAD_GATEWAY);
    }
}
