//! What a detection reports (#413): one log line, the
//! `mddb_chat_loop_guard_triggered_total{scenario}` counter, and the
//! `message.loop_stopped` webhook event. None of them carries the text of
//! the answer.

use tracing::warn;

use super::LoopDetected;
use crate::state::AppState;
use crate::webhook::events::LoopStopReport;
use crate::webhook::types::WebhookPayload;

/// Where in a turn a loop can be found.
#[derive(Debug, Clone)]
pub struct StopSite<'a> {
    pub session_id: &'a str,
    pub scenario: &'a str,
    /// The id of the answer being written.
    pub message_id: &'a str,
    /// The model call: a tool-calling round, or the streamed answer.
    pub round: usize,
}

impl StopSite<'_> {
    /// The same site at another model call.
    pub fn at_round(&self, round: usize) -> Self {
        Self {
            round,
            ..self.clone()
        }
    }
}

/// Logs, counts and announces one stopped answer.
pub fn report(state: &AppState, site: &StopSite<'_>, found: &LoopDetected) {
    warn!(
        session = site.session_id,
        scenario = site.scenario,
        round = site.round,
        position = found.position,
        ratio = found.ratio,
        "repetition loop detected; the answer was stopped"
    );
    state.metrics.loop_guard_triggered(site.scenario);
    state
        .webhook_dispatcher
        .dispatch(WebhookPayload::loop_stopped(LoopStopReport {
            session_id: site.session_id,
            scenario: site.scenario,
            message_id: site.message_id,
            round: site.round,
            position: found.position,
            ratio: found.ratio,
        }));
}
