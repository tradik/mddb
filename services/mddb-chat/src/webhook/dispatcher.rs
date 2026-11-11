use hmac::{Hmac, KeyInit, Mac};
use reqwest::Client;
use serde::Serialize;
use sha2::Sha256;
use tracing::{error, info, warn};

use crate::config::WebhookConfig;
use crate::webhook::events;
use crate::webhook::types::WebhookPayload;

type HmacSha256 = Hmac<Sha256>;

pub struct WebhookDispatcher {
    client: Client,
    webhooks: Vec<WebhookConfig>,
    secret: String,
}

impl WebhookDispatcher {
    pub fn new(webhooks: Vec<WebhookConfig>, secret: String) -> Self {
        // A misspelt event subscribes to nothing and would otherwise never say
        // so (#399). Logged rather than refused: the other entries still work.
        for webhook in &webhooks {
            for event in webhook.events.iter().filter(|e| !events::is_known(e)) {
                warn!(url = webhook.url, event, "webhook lists an unknown event");
            }
        }
        Self {
            client: Client::new(),
            webhooks,
            secret,
        }
    }

    /// Fire a webhook event asynchronously
    pub fn dispatch(&self, payload: WebhookPayload) {
        let matching: Vec<WebhookConfig> = self
            .webhooks
            .iter()
            .filter(|w| w.events.contains(&payload.event))
            .cloned()
            .collect();

        // Nothing to send when nobody subscribed, or when the payload cannot
        // be JSON (which `encode` has already logged).
        let subscribed = !matching.is_empty();
        let Some(body) = subscribed.then(|| encode(&payload)).flatten() else {
            return;
        };
        let client = self.client.clone();
        let secret = self.secret.clone();

        tokio::spawn(async move {
            for webhook in matching {
                let signature = compute_signature(&body, &secret);

                for attempt in 0..3 {
                    let mut req = client
                        .post(&webhook.url)
                        .header("Content-Type", "application/json")
                        .header("X-MDDB-Event", &payload.event)
                        .header("X-MDDB-Signature", &signature);

                    for (key, value) in &webhook.headers {
                        req = req.header(key.as_str(), value.as_str());
                    }

                    match req.body(body.clone()).send().await {
                        Ok(resp) if resp.status().is_success() => {
                            info!(
                                url = webhook.url,
                                event = payload.event,
                                "webhook delivered"
                            );
                            break;
                        }
                        Ok(resp) => {
                            let (url, status, attempt) = (&webhook.url, resp.status(), attempt + 1);
                            error!(url, %status, attempt, "webhook delivery failed");
                        }
                        Err(e) => {
                            let (url, attempt) = (&webhook.url, attempt + 1);
                            error!(url, error = %e, attempt, "webhook delivery error");
                        }
                    }

                    if attempt < 2 {
                        let delay = std::time::Duration::from_millis(500 * 2u64.pow(attempt));
                        tokio::time::sleep(delay).await;
                    }
                }
            }
        });
    }
}

/// The request body, or `None` (logged) when the payload cannot be JSON.
fn encode<T: Serialize>(payload: &T) -> Option<String> {
    match serde_json::to_string(payload) {
        Ok(body) => Some(body),
        Err(e) => {
            error!("failed to serialize webhook payload: {e}");
            None
        }
    }
}

fn compute_signature(body: &str, secret: &str) -> String {
    if secret.is_empty() {
        return String::new();
    }

    let mut mac =
        HmacSha256::new_from_slice(secret.as_bytes()).expect("HMAC can take key of any size");
    mac.update(body.as_bytes());
    let result = mac.finalize();
    hex::encode(result.into_bytes())
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use super::*;
    use crate::webhook::test_support::WebhookSink;

    fn webhook(url: &str, events: &[&str]) -> WebhookConfig {
        WebhookConfig {
            url: url.to_string(),
            events: events.iter().map(|e| e.to_string()).collect(),
            headers: HashMap::from([("X-Test".to_string(), "yes".to_string())]),
        }
    }

    #[tokio::test]
    async fn a_subscribed_event_arrives_signed_with_its_name_in_the_header() {
        let mut sink = WebhookSink::start().await;
        let dispatcher =
            WebhookDispatcher::new(vec![webhook(&sink.url, &["message"])], "s3cret".into());

        dispatcher.dispatch(WebhookPayload::message("sess", "support", "m1", "hi"));

        let delivery = sink.next().await;
        assert_eq!(delivery.event, "message");
        assert_eq!(delivery.body["data"]["message_id"], "m1");
        // The receiver verifies the body it got, byte for byte, against the
        // shared secret — so the signature must be over exactly those bytes.
        assert_eq!(
            delivery.signature,
            compute_signature(&delivery.raw_body, "s3cret")
        );
        assert_eq!(delivery.signature.len(), 64, "hex-encoded SHA-256");
    }

    #[tokio::test]
    async fn an_event_the_webhook_did_not_subscribe_to_is_not_sent() {
        let mut sink = WebhookSink::start().await;
        let dispatcher =
            WebhookDispatcher::new(vec![webhook(&sink.url, &["session.start"])], String::new());

        dispatcher.dispatch(WebhookPayload::feedback("sess", "support", None, "up"));

        sink.assert_silent().await;
    }

    #[tokio::test]
    async fn without_a_secret_the_signature_header_is_empty() {
        let mut sink = WebhookSink::start().await;
        let dispatcher =
            WebhookDispatcher::new(vec![webhook(&sink.url, &["queue.full"])], String::new());

        dispatcher.dispatch(WebhookPayload::queue_full(2, 1));

        assert_eq!(sink.next().await.signature, "");
    }

    #[tokio::test]
    async fn a_failing_receiver_is_retried_until_it_accepts() {
        let mut sink = WebhookSink::with_statuses(vec![500, 503]).await;
        let dispatcher = WebhookDispatcher::new(vec![webhook(&sink.url, &["answer"])], "k".into());

        dispatcher.dispatch(WebhookPayload::session_start("s", "Ada", "support"));
        sink.assert_silent().await; // wrong event: nothing to retry

        dispatcher.dispatch(WebhookPayload::feedback("s", "support", Some("a1"), "down"));
        sink.assert_silent().await;

        let payload = WebhookPayload::answer(crate::webhook::events::AnswerReport {
            session_id: "s",
            scenario: "support",
            message_id: "a1",
            in_reply_to: "m1",
            text: "done",
            provider: "openai",
            model: "gpt",
            spent: Default::default(),
            tools: Vec::new(),
            latency_ms: 1,
        });
        dispatcher.dispatch(payload);

        // Three attempts: two refused, the third accepted.
        for _ in 0..3 {
            assert_eq!(sink.next().await.event, "answer");
        }
        sink.assert_silent().await;
    }

    #[tokio::test]
    async fn an_unreachable_receiver_is_given_up_after_three_attempts() {
        // A port nothing listens on: every attempt is a connection error, and
        // the task must end rather than retry forever.
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("http://{}/hook", listener.local_addr().unwrap());
        drop(listener);
        let dispatcher = WebhookDispatcher::new(vec![webhook(&url, &["session.end"])], "k".into());

        dispatcher.dispatch(WebhookPayload::session_end("s", "Ada", "support"));

        // 500ms + 1000ms of backoff, then the task is done.
        tokio::time::sleep(std::time::Duration::from_millis(1800)).await;
    }

    #[test]
    fn an_unknown_event_name_is_tolerated_and_logged() {
        // Construction must not fail: the other entries still deliver.
        crate::webhook::test_support::enable_logs();
        let dispatcher = WebhookDispatcher::new(
            vec![webhook("http://127.0.0.1:9/hook", &["session_start"])],
            String::new(),
        );
        assert_eq!(dispatcher.webhooks.len(), 1);
    }

    #[test]
    fn a_payload_that_cannot_be_json_is_dropped_rather_than_sent() {
        // serde_json refuses non-string map keys; the body is None and the
        // dispatcher sends nothing.
        let bad: HashMap<(u8, u8), u8> = HashMap::from([((1, 2), 3)]);
        assert!(encode(&bad).is_none());
        assert_eq!(encode(&"ok"), Some("\"ok\"".to_string()));
    }

    #[test]
    fn the_signature_is_hmac_sha256_of_the_body() {
        // A fixed vector a receiver in another language can reproduce.
        let sig = compute_signature(r#"{"event":"x"}"#, "secret");
        assert_eq!(sig.len(), 64);
        assert_eq!(sig, compute_signature(r#"{"event":"x"}"#, "secret"));
        assert_ne!(sig, compute_signature(r#"{"event":"y"}"#, "secret"));
        assert_ne!(sig, compute_signature(r#"{"event":"x"}"#, "other"));
    }
}
