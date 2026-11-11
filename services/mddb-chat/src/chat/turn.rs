//! One turn: a visitor's message in, the model's answer out.

use futures::StreamExt;
use tracing::{debug, error};

use crate::chat::outbound::{Sender, send_error, send_json};
use crate::chat::trace::TurnTrace;
use crate::chat::{prompt, scenario, tools};
use crate::error::AppError;
use crate::llm::provider::{ApiMsg, ChatResponse, TokenUsage};
use crate::session::types::{MessageRole, WsOutgoing};
use crate::state::AppState;
use crate::webhook::types::WebhookPayload;

const MAX_TOOL_ROUNDS: usize = 5;

/// Answers `content` within `session_id`'s conversation.
pub async fn handle_message(
    state: &AppState,
    out: &mut Sender,
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

        // RAG-005: the second limit, and the one a budget actually runs out
        // in. max_turns caps how many turns a session takes; it says nothing
        // about what they cost, and one turn carrying a large RAG context
        // through several tool-calling rounds can be worth ten short ones.
        // Checked before the message is recorded, for the same reason.
        if !session.within_token_budget(state.config.security.max_tokens_per_session) {
            return Err(AppError::TokenBudgetExceeded(session.total_tokens_used));
        }

        session.add_message(MessageRole::User, content.clone());
        session.trim_history(state.config.session.max_history_length);

        (session.scenario.clone(), session.history.clone())
    };

    // #399: the question is accepted, so it gets an id and is reported. The
    // trace follows the turn and becomes the `answer` event at the end.
    let mut trace = TurnTrace::start();
    state.webhook_dispatcher.dispatch(WebhookPayload::message(
        session_id,
        &scenario_name,
        &trace.message_id,
        &content,
    ));

    // The scenario was validated at join; the generic prompt is the fallback
    // for a configuration that changed underneath a live session.
    let operator_prompt = state.config.get_scenario(&scenario_name).map_or_else(
        || "You are a helpful assistant.".to_string(),
        |s| s.system_prompt.clone(),
    );
    let collection = scenario::get_collection(&state.config, &scenario_name);
    let temperature = scenario::get_temperature(&state.config, &scenario_name);
    let max_tokens = state.config.llm.max_tokens;

    // RAG-002: the collection states how answers drawn from it should be
    // formatted, and that instruction travels with the data rather than being
    // repeated in every client's TOML.
    let mut mddb_client = state.mddb_client.clone();
    let collection_prompt = mddb_client.response_prompt(&collection).await;
    let system_prompt = scenario::compose_system_prompt(&operator_prompt, &collection_prompt);

    let mut api_messages = prompt::build_api_messages(&system_prompt, &history);
    // #396: the tools see only what this scenario allows, and the schema says
    // so, so a well-behaved model never asks for anything else.
    let tool_policy = tools::ToolPolicy::for_scenario(&state.config, &scenario_name);
    let tool_defs = tools::tool_definitions(&tool_policy);

    // Accumulated across every round of this turn and charged to the session
    // once, whichever way the loop ends (RAG-005).
    let mut spent = TokenUsage::default();

    // Agentic loop: let the model call tools until it produces an answer.
    for round in 0..MAX_TOOL_ROUNDS {
        debug!(round = round, "tool-calling round");

        let turn = state
            .llm_provider
            .chat_with_tools(&api_messages, &tool_defs, temperature, max_tokens)
            .await?;

        // RAG-005: charged per round, not per turn. A budget that counted only
        // the round that produced the answer would miss most of what was spent.
        spent += turn.usage;

        match turn.response {
            ChatResponse::ToolCalls { tool_calls } => {
                debug!(count = tool_calls.len(), "LLM requested tool calls");

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

                for tc in &tool_calls {
                    trace.record_tool(tc, &collection);
                    let result = tools::execute_tool(&mut mddb_client, tc, &tool_policy)
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
            }
            ChatResponse::Content(text) => {
                for chunk in text.chars().collect::<Vec<_>>().chunks(50) {
                    let piece: String = chunk.iter().collect();
                    send_json(out, &WsOutgoing::Chunk { content: piece }).await;
                }
                finish(state, out, &trace, session_id, &scenario_name, text, spent).await;
                return Ok(());
            }
        }
    }

    debug!("max tool rounds reached, streaming final response");
    let answer = stream_final_answer(state, out, &api_messages, temperature, max_tokens).await?;
    // RAG-005: the tool-calling rounds are charged here too, so a turn that
    // exhausted MAX_TOOL_ROUNDS costs what it cost. The streaming call itself
    // is NOT counted: its usage arrives in an SSE frame this reader discards
    // along with everything that is not a text delta, so a turn that reaches
    // this path undercounts by its final response. Stated rather than hidden.
    finish(
        state,
        out,
        &trace,
        session_id,
        &scenario_name,
        answer,
        spent,
    )
    .await;
    Ok(())
}

/// Streams the model's answer to the visitor as it arrives, returning what
/// was sent. Stops at `max_response_length` or at the first stream error,
/// either of which the visitor is told about.
async fn stream_final_answer(
    state: &AppState,
    out: &mut Sender,
    api_messages: &[ApiMsg],
    temperature: f32,
    max_tokens: u32,
) -> Result<String, AppError> {
    let mut stream = state
        .llm_provider
        .chat_stream_raw(api_messages, temperature, max_tokens)
        .await?;

    let mut full_response = String::new();
    while let Some(chunk) = stream.next().await {
        match chunk {
            Ok(text) => {
                full_response.push_str(&text);
                if full_response.len() > state.config.session.max_response_length {
                    let content = "\n\n[Response truncated]".to_string();
                    send_json(out, &WsOutgoing::Chunk { content }).await;
                    break;
                }
                send_json(out, &WsOutgoing::Chunk { content: text }).await;
            }
            Err(e) => {
                error!(error = %e, "LLM stream error");
                send_error(out, "error generating response").await;
                break;
            }
        }
    }
    Ok(full_response)
}

/// Closes a turn: `done` to the visitor with the answer's id (#399), the
/// `answer` webhook, and the answer and its cost recorded in the session.
///
/// The session may be gone by now — the TTL sweep or an `end` from another
/// path — in which case there is nothing left to record into.
async fn finish(
    state: &AppState,
    out: &mut Sender,
    trace: &TurnTrace,
    session_id: &str,
    scenario_name: &str,
    answer: String,
    spent: TokenUsage,
) {
    let message_id = trace.answer_id.clone();
    send_json(out, &WsOutgoing::Done { message_id }).await;
    state.webhook_dispatcher.dispatch(trace.answer(
        session_id,
        scenario_name,
        &answer,
        &state.config.llm,
        spent,
    ));

    if let Some(mut session) = state.session_manager.get_session_mut(session_id) {
        session.add_message(MessageRole::Assistant, answer);
        session.add_tokens(spent);
    }
}
