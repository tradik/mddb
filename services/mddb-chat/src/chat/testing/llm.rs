//! A scripted `LlmProvider`: the tests decide what the model says.

use std::collections::VecDeque;
use std::sync::Mutex;

use crate::error::AppError;
use crate::llm::provider::{
    ApiMsg, CallParams, ChatResponse, ChatTurn, ChunkStream, LlmProvider, TokenUsage, ToolCall,
    ToolCallFunction, ToolDef,
};

/// The chunks a scripted stream yields, or the error the call itself fails with.
type StreamScript = Result<Vec<Result<String, AppError>>, AppError>;

#[derive(Default)]
pub struct ScriptedLlm {
    turns: Mutex<VecDeque<Result<ChatTurn, AppError>>>,
    /// What `chat_stream_raw` yields.
    stream: Mutex<Option<StreamScript>>,
    /// Every request the handler sent, for asserting on the prompt.
    pub requests: Mutex<Vec<Vec<ApiMsg>>>,
    /// The parameters of every call, streaming ones included (#403).
    pub params: Mutex<Vec<CallParams>>,
    /// The tools offered with every non-streaming call.
    pub tools: Mutex<Vec<Vec<ToolDef>>>,
}

impl ScriptedLlm {
    fn push(&self, turn: Result<ChatTurn, AppError>) -> &Self {
        self.turns.lock().expect("script lock").push_back(turn);
        self
    }

    /// The next call answers with text and the given usage.
    pub fn answer(&self, text: &str, input: u64, output: u64) -> &Self {
        self.push(Ok(ChatTurn {
            response: ChatResponse::Content(text.to_string()),
            usage: TokenUsage { input, output },
        }))
    }

    /// The next call asks for one tool.
    pub fn tool_call(&self, name: &str, arguments: &str) -> &Self {
        self.push(Ok(ChatTurn {
            response: ChatResponse::ToolCalls {
                tool_calls: vec![ToolCall {
                    id: format!("call-{}", self.turns.lock().expect("script lock").len()),
                    function: ToolCallFunction {
                        name: name.to_string(),
                        arguments: arguments.to_string(),
                    },
                }],
            },
            usage: TokenUsage {
                input: 7,
                output: 3,
            },
        }))
    }

    /// The next call fails.
    pub fn fail(&self, message: &str) -> &Self {
        self.push(Err(AppError::LlmError(message.to_string())))
    }

    /// What the streaming fallback yields after the tool rounds run out.
    pub fn stream(&self, chunks: Vec<Result<String, AppError>>) -> &Self {
        *self.stream.lock().expect("stream lock") = Some(Ok(chunks));
        self
    }

    /// The streaming call itself fails.
    pub fn fail_stream(&self, message: &str) -> &Self {
        *self.stream.lock().expect("stream lock") =
            Some(Err(AppError::LlmError(message.to_string())));
        self
    }
}

#[async_trait::async_trait]
impl LlmProvider for ScriptedLlm {
    async fn chat_with_tools(
        &self,
        messages: &[ApiMsg],
        tools: &[ToolDef],
        params: &CallParams,
    ) -> Result<ChatTurn, AppError> {
        self.params
            .lock()
            .expect("params lock")
            .push(params.clone());
        self.tools.lock().expect("tools lock").push(tools.to_vec());
        self.requests
            .lock()
            .expect("requests lock")
            .push(messages.to_vec());
        self.turns
            .lock()
            .expect("script lock")
            .pop_front()
            .expect("the test scripted a turn for every model call")
    }

    async fn chat_stream_raw(
        &self,
        _messages: &[ApiMsg],
        params: &CallParams,
    ) -> Result<ChunkStream, AppError> {
        self.params
            .lock()
            .expect("params lock")
            .push(params.clone());
        let chunks = self
            .stream
            .lock()
            .expect("stream lock")
            .take()
            .expect("the test scripted the stream")?;
        Ok(Box::pin(futures::stream::iter(chunks)))
    }
}
