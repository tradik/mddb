use std::sync::Arc;

use axum::extract::ws::WebSocketUpgrade;
use axum::extract::{ConnectInfo, State};
use axum::http::HeaderMap;
use axum::response::IntoResponse;
use std::net::SocketAddr;

use crate::chat::handler::handle_ws;
use crate::security::client_ip;
use crate::state::AppState;

pub async fn ws_handler(
    ws: WebSocketUpgrade,
    State(state): State<Arc<AppState>>,
    ConnectInfo(addr): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
) -> impl IntoResponse {
    // SEC-014: the TCP peer unless a configured trusted proxy vouches for
    // someone else. See security/client_ip.rs for why the header alone is not
    // enough.
    let forwarded = headers
        .get("x-forwarded-for")
        .and_then(|value| value.to_str().ok());
    let client_ip = client_ip::resolve(addr, forwarded, &state.trusted_proxies);

    ws.on_upgrade(move |socket| handle_ws(socket, state, client_ip))
}
