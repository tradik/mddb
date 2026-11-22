//! The server's half of a chat connection: sending frames to the visitor.

use axum::extract::ws::{Message, WebSocket};
use futures::SinkExt;
use futures::stream::SplitSink;

use crate::session::types::WsOutgoing;

/// The sending half of an upgraded WebSocket.
pub type Sender = SplitSink<WebSocket, Message>;

/// Sends one frame. A failed send means the socket is gone; the receiving
/// half sees the same closure and ends the connection, so the error is
/// dropped here.
pub async fn send_frame(out: &mut Sender, frame: Message) {
    let _ = out.send(frame).await;
}

/// Sends `msg` as a JSON text frame.
pub async fn send_json(out: &mut Sender, msg: &WsOutgoing) {
    // WsOutgoing is strings and numbers under a tag: it always serialises.
    let json = serde_json::to_string(msg).expect("WsOutgoing serialises");
    send_frame(out, Message::Text(json.into())).await;
}

/// Sends an `error` message to the visitor.
pub async fn send_error(out: &mut Sender, message: &str) {
    send_json(
        out,
        &WsOutgoing::Error {
            message: message.to_string(),
        },
    )
    .await;
}

/// Where a turn's answer goes: the visitor's WebSocket, or an HTTP response
/// of the OpenAI-compatible API (#448). The turn itself (`turn.rs`,
/// `answer.rs`) knows only this trait, so both front doors run one pipeline.
#[async_trait::async_trait]
pub trait Outlet: Send {
    /// A piece of the answer, in order.
    async fn chunk(&mut self, content: String);
    /// Something went wrong after the answer started; what was sent stays.
    async fn error(&mut self, message: &str);
    /// The answer is complete; `message_id` names it for a later rating.
    async fn done(&mut self, message_id: String);
}

#[async_trait::async_trait]
impl Outlet for Sender {
    async fn chunk(&mut self, content: String) {
        send_json(self, &WsOutgoing::Chunk { content }).await;
    }

    async fn error(&mut self, message: &str) {
        send_error(self, message).await;
    }

    async fn done(&mut self, message_id: String) {
        send_json(self, &WsOutgoing::Done { message_id }).await;
    }
}
