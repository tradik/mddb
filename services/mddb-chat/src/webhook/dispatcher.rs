use hmac::{Hmac, KeyInit, Mac};
use reqwest::Client;
use sha2::Sha256;
use tracing::{error, info};

use crate::config::WebhookConfig;
use crate::webhook::types::WebhookPayload;

type HmacSha256 = Hmac<Sha256>;

pub struct WebhookDispatcher {
    client: Client,
    webhooks: Vec<WebhookConfig>,
    secret: String,
}

impl WebhookDispatcher {
    pub fn new(webhooks: Vec<WebhookConfig>, secret: String) -> Self {
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

        if matching.is_empty() {
            return;
        }

        let client = self.client.clone();
        let secret = self.secret.clone();

        tokio::spawn(async move {
            let body = match serde_json::to_string(&payload) {
                Ok(b) => b,
                Err(e) => {
                    error!("failed to serialize webhook payload: {e}");
                    return;
                }
            };

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
                            error!(
                                url = webhook.url,
                                status = %resp.status(),
                                attempt = attempt + 1,
                                "webhook delivery failed"
                            );
                        }
                        Err(e) => {
                            error!(
                                url = webhook.url,
                                error = %e,
                                attempt = attempt + 1,
                                "webhook delivery error"
                            );
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
