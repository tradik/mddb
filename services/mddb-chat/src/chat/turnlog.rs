//! The turn log (#406): what a scenario with `logging.enabled` records of
//! every turn — anonymous sessions included — in mddbd's conversation store.
//!
//! A signed user's turns go to that user's conversation (#405), with the
//! answer carrying the tools' queries, result keys and scores, the outcome
//! and the latency. An anonymous session's go to a conversation of its own,
//! `anon:<session id>`, created at its first logged turn and kept for the
//! scenario's `logging.retentionDays`. Redaction is mddbd's: the append only
//! asks for it.

use tracing::warn;

use crate::chat::conversation::{self, user_message};
use crate::chat::tools::ToolRun;
use crate::chat::trace::TurnTrace;
use crate::config::LlmConfig;
use crate::error::AppError;
use crate::grpc::client::{MddbClient, proto};
use crate::llm::provider::TokenUsage;
use crate::session::conversation::ConversationRef;
use crate::state::AppState;
use crate::webhook::types::ToolCallRecord;

/// Outcomes of a logged turn. `refused` is mddbd's to set: it knows the
/// scenario's redirect reply.
pub const ANSWERED: &str = "answered";
pub const NO_RESULTS: &str = "noResults";
pub const ERROR: &str = "error";

/// The prefix of an anonymous session's log conversation.
const ANON_PREFIX: &str = "anon:";

/// A scenario's `logging` settings.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LogPolicy {
    pub enabled: bool,
    /// Days an anonymous session's log is kept; 0 is mddbd's default.
    pub retention_days: u32,
    /// Ask mddbd to redact e-mails, tokens and card-like numbers.
    pub redact: bool,
}

impl Default for LogPolicy {
    /// Off, and redacting should it be turned on.
    fn default() -> Self {
        Self {
            enabled: false,
            retention_days: 0,
            redact: true,
        }
    }
}

impl LogPolicy {
    /// The scenario's settings as mddbd sends them; none is the default.
    pub fn from_proto(logging: Option<proto::ChatLoggingProto>) -> Self {
        logging.map_or_else(Self::default, |l| Self {
            enabled: l.enabled,
            retention_days: u32::try_from(l.retention_days).unwrap_or(0),
            redact: l.redact.unwrap_or(true),
        })
    }
}

/// What one tool call found, next to its name in the trace.
#[derive(Debug, Clone, PartialEq)]
pub struct LoggedRun {
    pub query: String,
    pub result_keys: Vec<String>,
    pub top_score: f64,
    pub duration_ms: u64,
}

impl LoggedRun {
    pub fn of(run: &ToolRun, duration_ms: u64) -> Self {
        Self {
            query: run.query.clone(),
            result_keys: run.result_keys.clone(),
            top_score: run.top_score,
            duration_ms,
        }
    }
}

/// `noResults` when the turn searched and nothing it looked for was found,
/// else `answered`.
pub fn outcome(runs: &[LoggedRun]) -> &'static str {
    let searches = runs.iter().filter(|r| !r.query.is_empty());
    let (searched, found) = searches.fold((false, false), |(_, found), r| {
        (true, found || !r.result_keys.is_empty())
    });
    if searched && !found {
        NO_RESULTS
    } else {
        ANSWERED
    }
}

/// The tools of a turn with what each looked for and found.
pub fn tool_calls(tools: &[ToolCallRecord], runs: &[LoggedRun]) -> Vec<proto::ChatToolCallProto> {
    tools
        .iter()
        .zip(runs)
        .map(|(tool, run)| proto::ChatToolCallProto {
            duration_ms: i64::try_from(run.duration_ms).unwrap_or(i64::MAX),
            query: run.query.clone(),
            result_keys: run.result_keys.clone(),
            top_score: run.top_score,
            ..conversation::stored_tool_call(tool)
        })
        .collect()
}

/// The answer as stored: always under the id the client was given, so a
/// rating can name it; with the turn's details when the turn is logged.
pub fn answer(
    trace: &TurnTrace,
    text: &str,
    (llm, spent): (&LlmConfig, TokenUsage),
    outcome: Option<&str>,
) -> proto::ChatMessageProto {
    let mut message = conversation::answer_message(text, trace.tools(), llm, spent);
    message.id = trace.answer_id.clone();
    if let Some(outcome) = outcome {
        message.tool_calls = tool_calls(trace.tools(), trace.runs());
        message.outcome = outcome.to_string();
        message.latency_ms = i64::try_from(trace.latency_ms()).unwrap_or(i64::MAX);
    }
    message
}

/// Where a turn is written, and how.
pub struct Destination<'a> {
    pub session_id: &'a str,
    pub scenario: &'a str,
    /// A signed session's conversation (#405).
    pub stored: Option<&'a ConversationRef>,
    pub policy: &'a LogPolicy,
}

/// Stores a turn's answer: in a signed session's conversation always, and —
/// when the scenario logs — with the question in an anonymous session's log.
/// A failure is logged and swallowed, as every write of a turn is.
pub async fn record(
    state: &AppState,
    client: &mut MddbClient,
    to: &Destination<'_>,
    question: &str,
    message: proto::ChatMessageProto,
) {
    let redact = to.policy.enabled && to.policy.redact;
    let written = match (to.stored, to.policy.enabled) {
        (Some(stored), _) => {
            client
                .append_redacted(&stored.id, &stored.sub, vec![message], redact)
                .await
        }
        (None, true) => match anonymous_log(state, client, to).await {
            Ok(log) => {
                let messages = vec![user_message(question), message];
                client
                    .append_redacted(&log.id, &log.sub, messages, redact)
                    .await
            }
            Err(e) => Err(e),
        },
        (None, false) => Ok(()),
    };
    if let Err(e) = written {
        warn!(session = to.session_id, error = %e, "could not store a chat turn");
    }
}

/// The anonymous session's log conversation, created at its first turn.
async fn anonymous_log(
    state: &AppState,
    client: &mut MddbClient,
    to: &Destination<'_>,
) -> Result<ConversationRef, AppError> {
    let held = state
        .session_manager
        .get_session(to.session_id)
        .and_then(|s| s.log.clone());
    if let Some(log) = held {
        return Ok(log);
    }
    let sub = format!("{ANON_PREFIX}{}", to.session_id);
    let created = client
        .create_conversation(&sub, to.scenario, "", to.policy.retention_days)
        .await?;
    let log = ConversationRef {
        sub,
        id: created.id,
    };
    if let Some(mut session) = state.session_manager.get_session_mut(to.session_id) {
        session.log = Some(log.clone());
    }
    Ok(log)
}

/// Records a turn that failed after its question was accepted: an `error`
/// answer, for a scenario that logs.
pub async fn record_failure(
    state: &AppState,
    client: &mut MddbClient,
    to: &Destination<'_>,
    (trace, question, llm): (&TurnTrace, &str, &LlmConfig),
    error: &AppError,
) {
    if !to.policy.enabled {
        return;
    }
    let message = answer(
        trace,
        &error.to_string(),
        (llm, TokenUsage::default()),
        Some(ERROR),
    );
    record(state, client, to, question, message).await;
}

#[cfg(test)]
#[path = "turnlog_tests.rs"]
mod tests;
