use std::sync::Arc;

use axum::extract::ws::{Message, WebSocket};
use futures::{SinkExt, StreamExt};
use tokio::sync::Mutex;
use tracing::{debug, error, info, warn};

use crate::chat::{scenario, tools};
use crate::error::AppError;
use crate::llm::provider::{ApiMsg, ChatResponse};
use crate::session::manager::JoinResult;
use crate::session::types::{MessageRole, WsIncoming, WsOutgoing};
use crate::state::AppState;
use crate::webhook::types::WebhookPayload;

const MAX_TOOL_ROUNDS: usize = 5;

pub async fn handle_ws(socket: WebSocket, state: Arc<AppState>, client_ip: String) {
    let (sender, mut receiver) = socket.split();
    let sender = Arc::new(Mutex::new(sender));
    let mut session_id: Option<String> = None;

    while let Some(msg) = receiver.next().await {
        let msg = match msg {
            Ok(Message::Text(text)) => text,
            Ok(Message::Close(_)) => break,
            Ok(Message::Ping(_)) => {
                let _ = sender.lock().await.send(Message::Pong(vec![].into())).await;
                continue;
            }
            Ok(_) => continue,
            Err(e) => {
                warn!(ip = client_ip, error = %e, "websocket error");
                break;
            }
        };

        let incoming: WsIncoming = match serde_json::from_str(&msg) {
            Ok(m) => m,
            Err(e) => {
                send_error(&sender, &format!("invalid message: {e}")).await;
                continue;
            }
        };

        match incoming {
            WsIncoming::Join { name, scenario } => {
                match handle_join(&state, &sender, &client_ip, name, scenario).await {
                    Ok(id) => session_id = Some(id),
                    Err(e) => {
                        send_error(&sender, &e.to_string()).await;
                    }
                }
            }
            WsIncoming::Message { content } => {
                if let Some(ref sid) = session_id {
                    if !state.rate_limiter.check(&client_ip) {
                        send_error(&sender, "rate limited — please slow down").await;
                        continue;
                    }
                    if let Err(e) = handle_message(&state, &sender, sid, &content).await {
                        send_error(&sender, &e.to_string()).await;
                    }
                } else {
                    send_error(&sender, "must join first").await;
                }
            }
            WsIncoming::Resume { session_id: sid } => {
                if state.session_manager.get_session(&sid).is_some() {
                    session_id = Some(sid.clone());
                    send_json(
                        &sender,
                        &WsOutgoing::Session {
                            id: sid,
                            scenario: String::new(),
                        },
                    )
                    .await;
                } else {
                    send_error(&sender, "session not found or expired").await;
                }
            }
            WsIncoming::Feedback {
                rating,
                question,
                answer,
            } => {
                let sid_str = session_id.as_deref().unwrap_or("unknown");
                let user_name = session_id
                    .as_ref()
                    .and_then(|sid| state.session_manager.get_session(sid))
                    .map(|s| s.name.clone())
                    .unwrap_or_default();

                if rating == "down" {
                    warn!(
                        session_id = sid_str,
                        user = user_name,
                        question = question,
                        answer = answer,
                        "NEGATIVE_FEEDBACK: bad response reported"
                    );
                } else {
                    info!(
                        session_id = sid_str,
                        user = user_name,
                        "positive feedback received"
                    );
                }
            }
            WsIncoming::End => {
                if let Some(sid) = session_id.take() {
                    let (name, scenario_name) = {
                        let session = state.session_manager.get_session(&sid);
                        session
                            .map(|s| (s.name.clone(), s.scenario.clone()))
                            .unwrap_or_default()
                    };
                    state.session_manager.remove_session(&sid).await;
                    state
                        .webhook_dispatcher
                        .dispatch(WebhookPayload::session_end(&sid, &name, &scenario_name));
                    info!(session_id = sid, name = name, "session ended by user");
                }
                send_json(&sender, &WsOutgoing::Ended).await;
            }
            WsIncoming::Ping => {
                send_json(&sender, &WsOutgoing::Pong).await;
            }
        }
    }

    // Cleanup on disconnect
    if let Some(sid) = session_id {
        let (name, scenario_name) = {
            let session = state.session_manager.get_session(&sid);
            session
                .map(|s| (s.name.clone(), s.scenario.clone()))
                .unwrap_or_default()
        };

        state.session_manager.remove_session(&sid).await;
        state
            .webhook_dispatcher
            .dispatch(WebhookPayload::session_end(&sid, &name, &scenario_name));
        info!(session_id = sid, name = name, "session ended");
    }
}

async fn handle_join(
    state: &AppState,
    sender: &Arc<Mutex<futures::stream::SplitSink<WebSocket, Message>>>,
    client_ip: &str,
    name: String,
    scenario_name: String,
) -> Result<String, AppError> {
    let sanitizer = &state.sanitizer;
    let name = sanitizer.sanitize_name(&name, state.config.session.name_max_chars)?;

    if state.config.get_scenario(&scenario_name).is_none() {
        return Err(AppError::InvalidMessage(format!(
            "unknown scenario: {scenario_name}"
        )));
    }

    if !state.rate_limiter.check(client_ip) {
        return Err(AppError::RateLimited);
    }

    match state
        .session_manager
        .join(name.clone(), scenario_name.clone())
        .await
    {
        JoinResult::Admitted { session_id } => {
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
                sender,
                &WsOutgoing::Session {
                    id: session_id.clone(),
                    scenario: scenario_name,
                },
            )
            .await;
            Ok(session_id)
        }
        JoinResult::Queued { position, admitted } => {
            info!(name = name, position = position, "user queued");
            send_json(sender, &WsOutgoing::Queued { position }).await;

            // The manager creates the session and sends its id here. This used
            // to wake up and call join() again, which created a second session
            // for the same visitor — see QueueEntry::admitted.
            match admitted.await {
                Ok(session_id) => {
                    info!(
                        session_id = session_id,
                        name = name,
                        "queued session started"
                    );
                    state
                        .webhook_dispatcher
                        .dispatch(WebhookPayload::session_start(
                            &session_id,
                            &name,
                            &scenario_name,
                        ));
                    send_json(
                        sender,
                        &WsOutgoing::Session {
                            id: session_id.clone(),
                            scenario: scenario_name,
                        },
                    )
                    .await;
                    Ok(session_id)
                }
                Err(_) => Err(AppError::SessionFull),
            }
        }
        JoinResult::Full => {
            state
                .webhook_dispatcher
                .dispatch(WebhookPayload::queue_full(
                    state.session_manager.queue_len().await,
                    state.session_manager.active_count(),
                ));
            Err(AppError::SessionFull)
        }
    }
}

async fn handle_message(
    state: &AppState,
    sender: &Arc<Mutex<futures::stream::SplitSink<WebSocket, Message>>>,
    session_id: &str,
    content: &str,
) -> Result<(), AppError> {
    let content = state.sanitizer.sanitize_message(content)?;

    let (scenario_name, history) = {
        let mut session = state
            .session_manager
            .get_session_mut(session_id)
            .ok_or(AppError::SessionNotFound)?;

        // TEST-001: a scenario's max_turns was parsed and never consulted, so
        // a demo capped at ten turns served an unbounded conversation — and an
        // unbounded LLM bill. Checked before the message is recorded, so a
        // refused turn does not count against the limit it was refused by.
        if let Some(scenario) = state.config.get_scenario(&session.scenario)
            && !scenario.allows_another_turn(session.user_turns)
        {
            return Err(AppError::TurnLimitReached(
                scenario.max_turns.unwrap_or(session.user_turns),
            ));
        }

        session.add_message(MessageRole::User, content.clone());
        session.trim_history(state.config.session.max_history_length);

        (session.scenario.clone(), session.history.clone())
    };

    // Build system prompt (without RAG context — tools will fetch it)
    let system_prompt = if let Some(scenario) = state.config.get_scenario(&scenario_name) {
        scenario.system_prompt.clone()
    } else {
        "You are a helpful assistant.".to_string()
    };

    let collection = scenario::get_collection(&state.config, &scenario_name);
    let temperature = scenario::get_temperature(&state.config, &scenario_name);
    let max_tokens = state.config.llm.max_tokens;

    // RAG-002: the collection states how answers drawn from it should be
    // formatted, and that instruction travels with the data rather than being
    // repeated in every client's TOML.
    let collection_prompt = state.mddb_client.clone().response_prompt(&collection).await;
    let system_prompt = scenario::compose_system_prompt(&system_prompt, &collection_prompt);

    // Build initial API messages
    let mut api_messages: Vec<ApiMsg> = Vec::new();

    // System message with tool instructions
    api_messages.push(ApiMsg {
        role: "system".to_string(),
        content: Some(format!(
            "{}\n\nYou have access to tools to search the documentation database. \
             Use the search_docs tool to find relevant information before answering questions. \
             Always search first, then answer based on what you find.",
            system_prompt
        )),
        tool_calls: None,
        tool_call_id: None,
    });

    // Conversation history
    for msg in &history {
        api_messages.push(ApiMsg {
            role: match msg.role {
                MessageRole::User => "user",
                MessageRole::Assistant => "assistant",
                MessageRole::System => "system",
            }
            .to_string(),
            content: Some(msg.content.clone()),
            tool_calls: None,
            tool_call_id: None,
        });
    }

    let tool_defs = tools::tool_definitions();
    let mut mddb_client = state.mddb_client.clone();

    // Agentic loop: let LLM call tools until it produces a final response
    for round in 0..MAX_TOOL_ROUNDS {
        debug!(round = round, "tool-calling round");

        let response = state
            .llm_provider
            .chat_with_tools(&api_messages, &tool_defs, temperature, max_tokens)
            .await?;

        match response {
            ChatResponse::ToolCalls { tool_calls } => {
                debug!(count = tool_calls.len(), "LLM requested tool calls");

                // Add assistant message with tool_calls
                let tc_json: Vec<serde_json::Value> = tool_calls
                    .iter()
                    .map(|tc| {
                        serde_json::json!({
                            "id": tc.id,
                            "type": "function",
                            "function": {
                                "name": tc.function.name,
                                "arguments": tc.function.arguments,
                            }
                        })
                    })
                    .collect();

                api_messages.push(ApiMsg {
                    role: "assistant".to_string(),
                    content: None,
                    tool_calls: Some(tc_json),
                    tool_call_id: None,
                });

                // Execute each tool and add results
                for tc in &tool_calls {
                    let result = tools::execute_tool(&mut mddb_client, tc, &collection)
                        .await
                        .unwrap_or_else(|e| format!("Tool error: {e}"));

                    debug!(
                        tool = tc.function.name,
                        result_len = result.len(),
                        "tool result"
                    );

                    api_messages.push(ApiMsg {
                        role: "tool".to_string(),
                        content: Some(result),
                        tool_calls: None,
                        tool_call_id: Some(tc.id.clone()),
                    });
                }
                // Continue loop — LLM will process tool results
            }
            ChatResponse::Content(text) => {
                // LLM produced final response without tools — send it
                for chunk in text.chars().collect::<Vec<_>>().chunks(50) {
                    let piece: String = chunk.iter().collect();
                    send_json(sender, &WsOutgoing::Chunk { content: piece }).await;
                }
                send_json(sender, &WsOutgoing::Done).await;

                if let Some(mut session) = state.session_manager.get_session_mut(session_id) {
                    session.add_message(MessageRole::Assistant, text);
                }
                return Ok(());
            }
        }
    }

    // After max rounds, stream the final response
    debug!("max tool rounds reached, streaming final response");

    let mut stream = state
        .llm_provider
        .chat_stream_raw(&api_messages, temperature, max_tokens)
        .await?;

    let mut full_response = String::new();

    while let Some(chunk) = stream.next().await {
        match chunk {
            Ok(text) => {
                full_response.push_str(&text);
                if full_response.len() > state.config.session.max_response_length {
                    send_json(
                        sender,
                        &WsOutgoing::Chunk {
                            content: "\n\n[Response truncated]".to_string(),
                        },
                    )
                    .await;
                    break;
                }
                send_json(sender, &WsOutgoing::Chunk { content: text }).await;
            }
            Err(e) => {
                error!(error = %e, "LLM stream error");
                send_error(sender, "error generating response").await;
                break;
            }
        }
    }

    send_json(sender, &WsOutgoing::Done).await;

    if let Some(mut session) = state.session_manager.get_session_mut(session_id) {
        session.add_message(MessageRole::Assistant, full_response);
    }

    Ok(())
}

async fn send_json<T: serde::Serialize>(
    sender: &Arc<Mutex<futures::stream::SplitSink<WebSocket, Message>>>,
    msg: &T,
) {
    if let Ok(json) = serde_json::to_string(msg) {
        let _ = sender.lock().await.send(Message::Text(json.into())).await;
    }
}

async fn send_error(
    sender: &Arc<Mutex<futures::stream::SplitSink<WebSocket, Message>>>,
    message: &str,
) {
    send_json(
        sender,
        &WsOutgoing::Error {
            message: message.to_string(),
        },
    )
    .await;
}
