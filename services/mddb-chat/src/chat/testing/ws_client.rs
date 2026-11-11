//! A WebSocket client small enough to live in the test tree: the HTTP upgrade
//! and RFC 6455 framing over a raw TCP stream. The crate has no WebSocket
//! client dependency and the tests should not add one for a protocol this
//! simple to speak.

use std::net::SocketAddr;
use std::time::Duration;

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;

const TEXT: u8 = 0x1;
const BINARY: u8 = 0x2;
const CLOSE: u8 = 0x8;
const PING: u8 = 0x9;
pub const PONG: u8 = 0xA;

/// A client frame's mask key. Any key is valid; this one makes the masked
/// bytes differ from the plain ones so a masking bug would show.
const MASK: [u8; 4] = [0x12, 0x34, 0x56, 0x78];

pub struct WsClient {
    stream: TcpStream,
    buf: Vec<u8>,
}

impl WsClient {
    /// Opens the TCP connection and completes the upgrade handshake.
    pub async fn connect(addr: SocketAddr, path: &str) -> Self {
        let mut stream = TcpStream::connect(addr).await.expect("connect");
        let request = format!(
            "GET {path} HTTP/1.1\r\nHost: {addr}\r\nUpgrade: websocket\r\n\
             Connection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\
             Sec-WebSocket-Version: 13\r\n\r\n"
        );
        stream
            .write_all(request.as_bytes())
            .await
            .expect("send upgrade");

        let mut client = Self {
            stream,
            buf: Vec::new(),
        };
        let end = loop {
            if let Some(pos) = client.buf.windows(4).position(|w| w == b"\r\n\r\n") {
                break pos + 4;
            }
            assert!(
                client.fill().await,
                "the server closed during the handshake"
            );
        };
        let head = String::from_utf8_lossy(&client.buf[..end]).to_string();
        assert!(head.starts_with("HTTP/1.1 101"), "upgrade refused: {head}");
        client.buf.drain(..end);
        client
    }

    pub async fn send_text(&mut self, text: &str) {
        self.send_frame(TEXT, text.as_bytes()).await;
    }

    pub async fn send_json(&mut self, value: serde_json::Value) {
        self.send_text(&value.to_string()).await;
    }

    pub async fn send_binary(&mut self, bytes: &[u8]) {
        self.send_frame(BINARY, bytes).await;
    }

    pub async fn send_ping(&mut self) {
        self.send_frame(PING, b"").await;
    }

    pub async fn send_close(&mut self) {
        self.send_frame(CLOSE, b"").await;
    }

    /// A frame with reserved bits set, which the server must treat as a
    /// protocol error.
    pub async fn send_garbage(&mut self) {
        self.stream
            .write_all(&[0xF1, 0x80, 0, 0, 0, 0])
            .await
            .expect("send garbage");
    }

    async fn send_frame(&mut self, opcode: u8, payload: &[u8]) {
        let mut frame = vec![0x80 | opcode];
        // Two length forms are enough: no test sends a frame of 64 KiB.
        let len = u16::try_from(payload.len()).expect("a test frame is under 64 KiB");
        if len < 126 {
            frame.push(0x80 | len as u8);
        } else {
            frame.push(0x80 | 126);
            frame.extend_from_slice(&len.to_be_bytes());
        }
        frame.extend_from_slice(&MASK);
        frame.extend(payload.iter().enumerate().map(|(i, b)| b ^ MASK[i % 4]));
        self.stream.write_all(&frame).await.expect("send frame");
    }

    /// The next frame as (opcode, payload), or `None` once the server has
    /// closed the connection.
    pub async fn recv_frame(&mut self) -> Option<(u8, Vec<u8>)> {
        if !self.need(2).await {
            return None;
        }
        let opcode = self.buf[0] & 0x0F;
        let (mut len, mut header) = ((self.buf[1] & 0x7F) as usize, 2);
        if len == 126 {
            assert!(self.need(4).await);
            len = u16::from_be_bytes([self.buf[2], self.buf[3]]) as usize;
            header = 4;
        }
        assert_ne!(len, 127, "the server sent a frame of 64 KiB or more");
        assert_eq!(self.buf[1] & 0x80, 0, "a server frame must not be masked");
        assert!(self.need(header + len).await, "truncated frame");
        let payload = self.buf[header..header + len].to_vec();
        self.buf.drain(..header + len);
        Some((opcode, payload))
    }

    /// The next text frame parsed as JSON, skipping control frames.
    pub async fn recv_json(&mut self) -> serde_json::Value {
        loop {
            let (opcode, payload) = tokio::time::timeout(Duration::from_secs(5), self.recv_frame())
                .await
                .expect("a frame should arrive within five seconds")
                .expect("the server closed the connection");
            if opcode == TEXT {
                return serde_json::from_slice(&payload).expect("the server sends JSON");
            }
        }
    }

    /// The next frame's opcode, with the same timeout as `recv_json`.
    pub async fn recv_opcode(&mut self) -> Option<u8> {
        tokio::time::timeout(Duration::from_secs(5), self.recv_frame())
            .await
            .expect("a frame should arrive within five seconds")
            .map(|(opcode, _)| opcode)
    }

    /// Reads text frames until a `done` frame, returning the concatenated
    /// chunk text and the `done` frame itself.
    pub async fn recv_answer(&mut self) -> (String, serde_json::Value) {
        let mut text = String::new();
        loop {
            let frame = self.recv_json().await;
            if frame["type"] == "done" {
                return (text, frame);
            }
            assert_eq!(frame["type"], "chunk", "unexpected frame: {frame}");
            text.push_str(frame["content"].as_str().expect("a chunk has content"));
        }
    }

    /// Ensures at least `n` bytes are buffered; false once the peer closed.
    async fn need(&mut self, n: usize) -> bool {
        while self.buf.len() < n {
            if !self.fill().await {
                return false;
            }
        }
        true
    }

    async fn fill(&mut self) -> bool {
        let mut chunk = [0u8; 4096];
        match self.stream.read(&mut chunk).await {
            Ok(0) | Err(_) => false,
            Ok(n) => {
                self.buf.extend_from_slice(&chunk[..n]);
                true
            }
        }
    }
}
