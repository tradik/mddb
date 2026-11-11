//! A session's life on one connection: join, resume, end, and what happens
//! when the socket goes away (#402).

use tracing::info;

use crate::chat::handler::Connection;
use crate::chat::outbound::{Sender, send_error, send_json};
use crate::chat::scenario::{self, Resolved};
use crate::error::AppError;
use crate::session::manager::{JoinResult, ResumeError};
use crate::session::types::WsOutgoing;
use crate::webhook::types::WebhookPayload;

impl Connection {
    /// Starts a new session for this connection.
    ///
    /// A connection that already holds a session detaches it first, so the
    /// old one can still be resumed and no longer counts toward
    /// `max_concurrent`. Before #402 it was simply forgotten, and held its
    /// slot until the TTL sweep found it.
    pub(super) async fn join(&mut self, out: &mut Sender, name: String, scenario: String) {
        self.detach_session().await;
        match self.admit(out, name, scenario).await {
            Ok(id) => self.session_id = Some(id),
            Err(e) => send_error(out, &e.to_string()).await,
        }
    }

    async fn admit(
        &self,
        out: &mut Sender,
        name: String,
        scenario_name: String,
    ) -> Result<String, AppError> {
        let state = &self.state;
        let name = state
            .sanitizer
            .sanitize_name(&name, state.config.session.name_max_chars)?;

        // #403: mddbd's scenario of that name if it has one, else the TOML's.
        // One that mddbd holds switched off is refused outright — the TOML
        // scenario of the same name does not stand in for it.
        match scenario::resolve(&state.config, &state.scenarios, &scenario_name) {
            Resolved::Active(_) => {}
            Resolved::Disabled => return Err(AppError::ScenarioDisabled),
            Resolved::Unknown => {
                return Err(AppError::InvalidMessage(format!(
                    "unknown scenario: {scenario_name}"
                )));
            }
        }

        if !state.rate_limiter.check(&self.client_ip) {
            return Err(AppError::RateLimited);
        }

        let session_id = match state
            .session_manager
            .join(name.clone(), scenario_name.clone())
            .await
        {
            JoinResult::Admitted { session_id } => session_id,
            JoinResult::Queued { position, admitted } => {
                info!(name = name, position = position, "user queued");
                send_json(out, &WsOutgoing::Queued { position }).await;

                // The manager creates the session and sends its id here. This
                // used to wake up and call join() again, which created a second
                // session for the same visitor — see QueueEntry::admitted. An
                // error means the manager was dropped, which only happens at
                // shutdown.
                admitted.await.map_err(|_| AppError::SessionFull)?
            }
            JoinResult::Full => {
                state
                    .webhook_dispatcher
                    .dispatch(WebhookPayload::queue_full(
                        state.session_manager.queue_len().await,
                        state.session_manager.attached_count(),
                    ));
                return Err(AppError::SessionFull);
            }
        };

        info!(
            session_id = session_id,
            name = name,
            scenario = scenario_name,
            "session started"
        );
        state
            .webhook_dispatcher
            .dispatch(WebhookPayload::session_start(
                &session_id,
                &name,
                &scenario_name,
            ));
        send_json(
            out,
            &WsOutgoing::Session {
                id: session_id.clone(),
                scenario: scenario_name,
            },
        )
        .await;
        Ok(session_id)
    }

    /// Reattaches a session left behind by an earlier connection (#402).
    ///
    /// Rate limited like a join: a reconnect storm must not be a way around
    /// the per-address budget. Resuming the session this connection already
    /// holds is harmless and answered the same way.
    pub(super) async fn resume(&mut self, out: &mut Sender, session_id: String) {
        if !self.state.rate_limiter.check(&self.client_ip) {
            send_error(out, &AppError::RateLimited.to_string()).await;
            return;
        }

        let outcome = if self.session_id.as_deref() == Some(session_id.as_str()) {
            // Nothing to reattach — unless the TTL sweep took the session
            // from under this connection in the meantime.
            self.state
                .session_manager
                .get_session(&session_id)
                .map(|s| s.scenario.clone())
                .ok_or(ResumeError::NotFound)
        } else {
            self.detach_session().await;
            self.state.session_manager.resume(&session_id)
        };

        match outcome {
            Ok(scenario) => {
                info!(session_id = session_id, "session resumed");
                self.session_id = Some(session_id.clone());
                send_json(
                    out,
                    &WsOutgoing::Session {
                        id: session_id,
                        scenario,
                    },
                )
                .await;
            }
            Err(e) => {
                self.session_id = None;
                send_error(out, &e.to_string()).await;
            }
        }
    }

    /// Ends the session for good: removed, and `session.end` fired.
    pub(super) async fn end(&mut self, out: &mut Sender) {
        if let Some(sid) = self.session_id.take() {
            let (name, scenario) = self.describe(&sid);
            self.state.session_manager.remove_session(&sid).await;
            self.state
                .webhook_dispatcher
                .dispatch(WebhookPayload::session_end(&sid, &name, &scenario));
            info!(session_id = sid, name = name, "session ended by user");
        }
        send_json(out, &WsOutgoing::Ended).await;
    }

    /// Lets go of the session without ending it (#402).
    ///
    /// Called when the socket closes, and before a join or resume replaces
    /// the session this connection holds. The session stays resumable until
    /// its TTL; `session.end` is not fired, because the conversation may well
    /// continue on the next connection.
    pub(super) async fn detach_session(&mut self) {
        if let Some(sid) = self.session_id.take()
            && self.state.session_manager.detach(&sid).await
        {
            info!(
                session_id = sid,
                "session detached; resumable until its TTL"
            );
        }
    }

    /// The visitor's name and scenario, or blanks if the session is gone.
    fn describe(&self, session_id: &str) -> (String, String) {
        self.state
            .session_manager
            .get_session(session_id)
            .map(|s| (s.name.clone(), s.scenario.clone()))
            .unwrap_or_default()
    }
}
