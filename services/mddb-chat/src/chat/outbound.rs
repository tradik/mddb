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
