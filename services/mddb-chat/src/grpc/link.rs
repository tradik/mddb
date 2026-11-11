//! The connection to mddbd as the rest of the server sees it: there, or not
//! there yet (#401).
//!
//! mddb-chat used to connect once at startup and exit on failure. Next to an
//! mddbd that is still starting — the normal order in Kubernetes — that is a
//! crash loop. The server now starts without mddbd, keeps trying in the
//! background with exponential backoff, and reports not-ready on `/health`
//! until the link is up.

use std::sync::Arc;
use std::time::Duration;

use tokio::sync::RwLock;
use tracing::{info, warn};

use crate::config::MddbConfig;
use crate::grpc::client::MddbClient;

/// The first wait between two connection attempts; doubled after each failure.
const FIRST_DELAY: Duration = Duration::from_secs(1);

/// A shared slot for the mddbd client, empty until the first successful
/// connect and login.
#[derive(Clone, Default)]
pub struct MddbLink {
    client: Arc<RwLock<Option<MddbClient>>>,
}

impl MddbLink {
    /// A link that is already up.
    pub fn connected(client: MddbClient) -> Self {
        Self {
            client: Arc::new(RwLock::new(Some(client))),
        }
    }

    pub async fn set(&self, client: MddbClient) {
        *self.client.write().await = Some(client);
    }

    /// A client to work with, or `None` while mddbd has not been reached.
    ///
    /// Each caller gets its own clone: the channel underneath is shared, the
    /// per-collection config cache is not, which is how the handler already
    /// used it.
    pub async fn client(&self) -> Option<MddbClient> {
        self.client.read().await.clone()
    }

    pub async fn is_connected(&self) -> bool {
        self.client.read().await.is_some()
    }
}

/// How long to wait after failed attempt number `attempt` (counted from 0):
/// `first`, doubled each time, never more than `cap`.
pub fn backoff_delay(attempt: u32, first: Duration, cap: Duration) -> Duration {
    let factor = 2u32.checked_pow(attempt).unwrap_or(u32::MAX);
    first.saturating_mul(factor).min(cap)
}

/// Connects and logs in to mddbd, retrying until it works, then fills `link`.
///
/// Every kind of failure is retried, a refused login included: at startup the
/// HTTP API that issues the token may be as absent as the gRPC port, and a
/// wrong password is not made better by exiting — the warning repeats in the
/// log until someone fixes it.
pub async fn connect_with_retry(config: MddbConfig, link: MddbLink) {
    let cap = Duration::from_secs(config.connect_retry_max_seconds);
    retry_until_connected(config, link, FIRST_DELAY, cap).await;
}

async fn retry_until_connected(config: MddbConfig, link: MddbLink, first: Duration, cap: Duration) {
    let mut attempt = 0u32;
    loop {
        match MddbClient::connect(config.clone()).await {
            Ok(client) => {
                link.set(client).await;
                info!(grpc_addr = config.grpc_addr, attempt, "connected to mddbd");
                return;
            }
            Err(e) => {
                let delay = backoff_delay(attempt, first, cap);
                let retry_in_ms = delay.as_millis() as u64;
                warn!(grpc_addr = config.grpc_addr, error = %e, retry_in_ms, "mddbd is not reachable yet");
                tokio::time::sleep(delay).await;
                attempt = attempt.saturating_add(1);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::test_support::{stub_http_server, test_config};

    const MS: Duration = Duration::from_millis(1);

    #[test]
    fn the_delay_doubles_until_it_reaches_the_cap() {
        let secs = |attempt| {
            backoff_delay(attempt, Duration::from_secs(1), Duration::from_secs(30)).as_secs()
        };

        assert_eq!(
            (0..7).map(secs).collect::<Vec<_>>(),
            vec![1, 2, 4, 8, 16, 30, 30]
        );
    }

    #[test]
    fn an_absurd_attempt_count_does_not_overflow() {
        let cap = Duration::from_secs(30);

        assert_eq!(backoff_delay(31, FIRST_DELAY, cap), cap);
        assert_eq!(backoff_delay(u32::MAX, FIRST_DELAY, cap), cap);
    }

    #[tokio::test]
    async fn a_new_link_is_not_connected() {
        let link = MddbLink::default();

        assert!(!link.is_connected().await);
        assert!(link.client().await.is_none());
    }

    #[tokio::test]
    async fn a_link_built_from_a_client_is_connected() {
        let addr = stub_http_server().await;
        let config = test_config(&format!("http://{addr}")).mddb;
        let client = MddbClient::connect(config).await.expect("connect");

        let link = MddbLink::connected(client);

        assert!(link.is_connected().await);
        assert!(link.client().await.is_some());
    }

    // #401: the whole point. mddbd comes up after mddb-chat does.
    #[tokio::test]
    async fn the_link_comes_up_once_mddbd_does() {
        // Reserve a port, then free it: nothing listens there yet.
        let reserved = std::net::TcpListener::bind("127.0.0.1:0").expect("reserve");
        let addr = reserved.local_addr().expect("addr");
        drop(reserved);

        let config = test_config(&format!("http://{addr}")).mddb;
        let link = MddbLink::default();
        let task = tokio::spawn(retry_until_connected(config, link.clone(), 5 * MS, 20 * MS));

        tokio::time::sleep(40 * MS).await;
        assert!(
            !link.is_connected().await,
            "connected to a port nobody listens on"
        );

        let listener = tokio::net::TcpListener::bind(addr).await.expect("bind");
        tokio::spawn(async move { axum::serve(listener, axum::Router::new()).await });

        tokio::time::timeout(Duration::from_secs(5), task)
            .await
            .expect("the retry loop never connected")
            .expect("the retry task panicked");
        assert!(link.is_connected().await);
    }

    #[tokio::test]
    async fn the_public_entry_point_connects_at_the_first_attempt_when_it_can() {
        let addr = stub_http_server().await;
        let config = test_config(&format!("http://{addr}")).mddb;
        let link = MddbLink::default();

        connect_with_retry(config, link.clone()).await;

        assert!(link.is_connected().await);
    }
}
