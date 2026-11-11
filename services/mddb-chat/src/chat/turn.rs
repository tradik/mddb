//! One turn: a visitor's message in, the model's answer out.

use std::time::Instant;

use futures::StreamExt;
use tracing::{debug, error};

use crate::chat::conversation;
use crate::chat::mcp::McpToolset;
use crate::chat::outbound::{Sender, send_error, send_json};
use crate::chat::scenario::{self, ActiveScenario, Resolved};
use crate::chat::trace::TurnTrace;
use crate::chat::{prompt, tools};
use crate::config::LlmConfig;
use crate::error::AppError;
use crate::grpc::client::MddbClient;
use crate::grpc::client::proto::ChatMcpServerStateProto;
use crate::llm::provider::{ApiMsg, CallParams, ChatResponse, TokenUsage};
use crate::session::conversation::ConversationRef;
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

    // #401: the server answers before mddbd does. Checked before the message
    // is recorded, so a turn refused for this does not count as one.
    let mut mddb_client = state.mddb.client().await.ok_or(AppError::MddbUnavailable)?;

    let (scenario_name, scenario, history, stored) = {
        let mut session = state
            .session_manager
            .get_session_mut(session_id)
            .ok_or(AppError::SessionNotFound)?;

        // #403: the scenario is looked up by name on every turn and kept for
        // the whole of it. A reload from mddbd therefore reaches an open
        // session at its next message, and never in the middle of an answer.
        let scenario = match scenario::resolve(&state.config, &state.scenarios, &session.scenario) {
            Resolved::Active(scenario) => scenario,
            Resolved::Disabled => return Err(AppError::ScenarioDisabled),
            // The scenario was validated at join; the generic prompt is the
            // fallback for one that was removed underneath a live session.
            Resolved::Unknown => ActiveScenario::generic(&state.config),
        };

        // TEST-001: a scenario's max_turns was parsed and never consulted, so
        // a demo capped at ten turns served an unbounded conversation — and an
        // unbounded LLM bill. Checked before the message is recorded, so a
        // refused turn does not count against the limit it was refused by.
        if !scenario.allows_another_turn(session.user_turns) {
            return Err(AppError::TurnLimitReached(
                scenario.max_turns.unwrap_or(session.user_turns),
            ));
        }

        // RAG-005: the second limit, and the one a budget actually runs out
        // in. max_turns caps how many turns a session takes; it says nothing
        // about what they cost, and one turn carrying a large RAG context
        // through several tool-calling rounds can be worth ten short ones.
        // Checked before the message is recorded, for the same reason.
        if !session.within_token_budget(scenario.token_budget) {
            return Err(AppError::TokenBudgetExceeded(session.total_tokens_used));
        }

        // #403: the scenario's own pace for one session, next to the
        // per-address limit the handler has already applied.
        if !session
            .pace
            .admit(scenario.messages_per_minute, Instant::now())
        {
            return Err(AppError::SessionRateLimited(scenario.messages_per_minute));
        }

        session.add_message(MessageRole::User, content.clone());
        session.trim_history(state.config.session.max_history_length);

        (
            session.scenario.clone(),
            scenario,
            session.history.clone(),
            session.conversation.clone(),
        )
    };

    // #405: a signed user's question is stored as soon as it is accepted, so
    // a turn that fails half way still leaves it in the conversation.
    if let Some(to) = &stored {
        conversation::record(&mut mddb_client, to, conversation::user_message(&content)).await;
    }

    // #399: the question is accepted, so it gets an id and is reported. The
    // trace follows the turn and becomes the `answer` event at the end.
    let mut trace = TurnTrace::start();
    state.webhook_dispatcher.dispatch(WebhookPayload::message(
        session_id,
        &scenario_name,
        &trace.message_id,
        &content,
    ));

    let (system_prompt, mcp_states) =
        system_prompt(&mut mddb_client, &scenario_name, &scenario).await;
    let mut api_messages = prompt::build_api_messages(&system_prompt, &history);
    // #396: the tools see only what this scenario allows, and the schema says
    // so, so a well-behaved model never asks for anything else.
    let tool_policy =
        tools::ToolPolicy::for_scenario(&scenario, state.config.mddb.result_max_chars).with_mcp(
            external_tools(state, &scenario_name, &scenario, &mcp_states),
        );
    let tool_defs = tools::tool_definitions(&tool_policy);
    trace.offer_mcp(&tool_policy.mcp);

    // #403: what the `answer` event reports is the model that was asked, which
    // a scenario may have changed.
    let llm = LlmConfig {
        model: scenario.llm.model_or(&state.config.llm.model).to_string(),
        ..state.config.llm.clone()
    };
    let closing = Closing {
        session_id,
        scenario: &scenario_name,
        llm: &llm,
        stored: stored.as_ref(),
    };

    // Accumulated across every round of this turn and charged to the session
    // once, whichever way the loop ends (RAG-005).
    let mut spent = TokenUsage::default();

    // Agentic loop: let the model call tools until it produces an answer.
    for round in 0..MAX_TOOL_ROUNDS {
        debug!(round = round, "tool-calling round");

        let turn = state
            .llm_provider
            .chat_with_tools(&api_messages, &tool_defs, &scenario.llm)
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
                    trace.record_tool(tc, &scenario.default_collection);
                    let result = tools::execute_tool(&mut mddb_client, tc, &tool_policy)
                        .await
                        .unwrap_or_else(|e| format!("Tool error: {e}"));
                    let result_len = result.len();
                    debug!(tool = tc.function.name, result_len, "tool result");
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
                finish(
                    state,
                    out,
                    &mut mddb_client,
                    &trace,
                    &closing,
                    (text, spent),
                )
                .await;
                return Ok(());
            }
        }
    }

    debug!("max tool rounds reached, streaming final response");
    let answer = stream_final_answer(state, out, &api_messages, &scenario.llm).await?;
    // RAG-005: the tool-calling rounds are charged here too, so a turn that
    // exhausted MAX_TOOL_ROUNDS costs what it cost. The streaming call itself
    // is NOT counted: its usage arrives in an SSE frame this reader discards
    // along with everything that is not a text delta, so a turn that reaches
    // this path undercounts by its final response. Stated rather than hidden.
    finish(
        state,
        out,
        &mut mddb_client,
        &trace,
        &closing,
        (answer, spent),
    )
    .await;
    Ok(())
}

/// The external MCP tools of one turn (MCP-005): the ones mddbd reports
/// active right now, offered only while mddb-chat's own reading of the `chat`
/// and `chatExternalMcp` features is on, so a switch turned off in mddbd
/// hides them at the next poll even if a preview says otherwise.
fn external_tools(
    state: &AppState,
    name: &str,
    scenario: &ActiveScenario,
    states: &[ChatMcpServerStateProto],
) -> McpToolset {
    let enabled = state.features.chat() && state.features.chat_external_mcp();
    McpToolset::for_turn(name, states, &scenario.mcp.limits, enabled)
}

/// The system prompt of one turn, and the state of its external MCP servers
/// (MCP-005) from the same preview.
///
/// A scenario from mddbd comes with its prompt composed there — operator
/// prompt, guardrails, off-topic and style clauses, the collections'
/// `responsePrompt`s — and nothing is added to it here (#403). It is asked for
/// again on every turn, because an edit of a collection's `responsePrompt`
/// changes the composed prompt without moving the scenario version; when the
/// question fails, the prompt of the last reload serves.
///
/// A TOML scenario is the operator's prompt followed by its collection's
/// (RAG-002): the collection states how answers drawn from it should be
/// formatted, and that instruction travels with the data rather than being
/// repeated in every client's TOML.
async fn system_prompt(
    client: &mut MddbClient,
    name: &str,
    scenario: &ActiveScenario,
) -> (String, Vec<ChatMcpServerStateProto>) {
    if !scenario.composed {
        let collection_prompt = client.response_prompt(&scenario.default_collection).await;
        let prompt = scenario::compose_system_prompt(&scenario.system_prompt, &collection_prompt);
        return (prompt, Vec::new());
    }
    match client.chat_scenario_preview(name).await {
        Ok(preview) => (preview.prompt, preview.mcp_servers),
        Err(e) => {
            debug!(scenario = name, error = %e, "prompt lookup failed; using the one last loaded");
            (scenario.system_prompt.clone(), scenario.mcp.states.clone())
        }
    }
}

/// Streams the model's answer to the visitor as it arrives, returning what
/// was sent. Stops at `max_response_length` or at the first stream error,
/// either of which the visitor is told about.
async fn stream_final_answer(
    state: &AppState,
    out: &mut Sender,
    api_messages: &[ApiMsg],
    params: &CallParams,
) -> Result<String, AppError> {
    let mut stream = state
        .llm_provider
        .chat_stream_raw(api_messages, params)
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

/// Whose turn `finish` closes, and which model answered it.
struct Closing<'a> {
    session_id: &'a str,
    scenario: &'a str,
    llm: &'a LlmConfig,
    /// Where a signed session's answer is stored (#405).
    stored: Option<&'a ConversationRef>,
}

/// Closes a turn: `done` to the visitor with the answer's id (#399), the
/// `answer` webhook, and the answer and its cost recorded in the session —
/// and, for a signed user, in mddbd with its tools, model and tokens (#405).
///
/// The session may be gone by now — the TTL sweep or an `end` from another
/// path — in which case there is nothing left to record into.
async fn finish(
    state: &AppState,
    out: &mut Sender,
    client: &mut MddbClient,
    trace: &TurnTrace,
    closing: &Closing<'_>,
    (answer, spent): (String, TokenUsage),
) {
    let message_id = trace.answer_id.clone();
    send_json(out, &WsOutgoing::Done { message_id }).await;
    state.webhook_dispatcher.dispatch(trace.answer(
        closing.session_id,
        closing.scenario,
        &answer,
        closing.llm,
        spent,
    ));
    if let Some(to) = closing.stored {
        let stored = conversation::answer_message(&answer, trace.tools(), closing.llm, spent);
        conversation::record(client, to, stored).await;
    }

    if let Some(mut session) = state.session_manager.get_session_mut(closing.session_id) {
        session.add_message(MessageRole::Assistant, answer);
        session.add_tokens(spent);
    }
}
