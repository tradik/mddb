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
    GrpcError(tonic::Status),
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
            AppError::TurnLimitReached(max) => {
                write!(f, "this conversation is limited to {max} turns")
            }
            AppError::GrpcError(s) => write!(f, "grpc error: {s}"),
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
            AppError::GrpcError(_) => (StatusCode::BAD_GATEWAY, self.to_string()),
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
            // Waiting helps.
            (AppError::RateLimited, StatusCode::TOO_MANY_REQUESTS),
            (AppError::SessionFull, StatusCode::SERVICE_UNAVAILABLE),
            // Waiting does not: the scenario caps the conversation, so this is
            // a refusal rather than backpressure.
            (AppError::TurnLimitReached(5), StatusCode::FORBIDDEN),
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
