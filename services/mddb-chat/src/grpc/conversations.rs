//! A signed-in user's conversations as mddbd stores them (#405).
//!
//! Every call names the `sub` of a verified join token; mddbd answers
//! "not found" for a conversation of any other sub, so these methods cannot
//! reach another user's data even when handed the wrong id.

use super::client::{MddbClient, proto};
use crate::error::AppError;

impl MddbClient {
    /// Starts a conversation. `retention_days` 0 is mddbd's default.
    pub async fn create_conversation(
        &mut self,
        sub: &str,
        scenario: &str,
        display_name: &str,
        retention_days: u32,
    ) -> Result<proto::ChatConversationProto, AppError> {
        let request = self.auth_request(proto::CreateChatConversationRequest {
            sub: sub.to_string(),
            scenario: scenario.to_string(),
            display_name: display_name.to_string(),
            retention_days: i32::try_from(retention_days).unwrap_or(i32::MAX),
        });
        Ok(self
            .client
            .create_chat_conversation(request)
            .await?
            .into_inner())
    }

    /// One page of the user's conversations in `scenario`, newest first,
    /// without messages.
    pub async fn list_conversations(
        &mut self,
        sub: &str,
        scenario: &str,
        limit: i32,
    ) -> Result<Vec<proto::ChatConversationProto>, AppError> {
        let request = self.auth_request(proto::ListChatConversationsRequest {
            sub: sub.to_string(),
            scenario: scenario.to_string(),
            limit,
            cursor: String::new(),
        });
        let response = self.client.list_chat_conversations(request).await?;
        Ok(response.into_inner().conversations)
    }

    /// A conversation with its last `limit` messages, oldest first.
    pub async fn get_conversation(
        &mut self,
        id: &str,
        sub: &str,
        limit: usize,
    ) -> Result<proto::ChatConversationProto, AppError> {
        let request = self.auth_request(proto::GetChatConversationRequest {
            id: id.to_string(),
            sub: sub.to_string(),
            limit: i32::try_from(limit).unwrap_or(i32::MAX),
        });
        Ok(self
            .client
            .get_chat_conversation(request)
            .await?
            .into_inner())
    }

    /// Adds messages to a conversation; mddbd gives each its id and time.
    #[cfg(test)]
    pub async fn append_messages(
        &mut self,
        id: &str,
        sub: &str,
        messages: Vec<proto::ChatMessageProto>,
    ) -> Result<(), AppError> {
        self.append_redacted(id, sub, messages, false).await
    }

    /// Adds messages, asking mddbd to redact them — and every later write to
    /// the conversation — when `redact` is set (#406). A message's own id is
    /// kept when it looks like one, so an answer can be rated by the id the
    /// client saw.
    pub async fn append_redacted(
        &mut self,
        id: &str,
        sub: &str,
        messages: Vec<proto::ChatMessageProto>,
        redact: bool,
    ) -> Result<(), AppError> {
        let request = self.auth_request(proto::AppendChatMessagesRequest {
            id: id.to_string(),
            sub: sub.to_string(),
            messages,
            redact,
        });
        self.client.append_chat_messages(request).await?;
        Ok(())
    }

    /// Rates an answer of a conversation, `up` or `down`, with an optional
    /// comment (#406). Rating again replaces the earlier rating.
    pub async fn rate_message(
        &mut self,
        to: (&str, &str),
        message_id: &str,
        rating: &str,
        comment: &str,
    ) -> Result<(), AppError> {
        let (id, sub) = to;
        let request = self.auth_request(proto::RateChatMessageRequest {
            id: id.to_string(),
            sub: sub.to_string(),
            message_id: message_id.to_string(),
            rating: rating.to_string(),
            comment: comment.to_string(),
        });
        self.client.rate_chat_message(request).await?;
        Ok(())
    }

    /// Marks a conversation ended; it stays readable until its retention.
    pub async fn end_conversation(&mut self, id: &str, sub: &str) -> Result<(), AppError> {
        let request = self.auth_request(proto::EndChatConversationRequest {
            id: id.to_string(),
            sub: sub.to_string(),
        });
        self.client.end_chat_conversation(request).await?;
        Ok(())
    }

    /// Removes every conversation of `sub`, returning how many there were.
    pub async fn delete_user_conversations(&mut self, sub: &str) -> Result<i32, AppError> {
        let request = self.auth_request(proto::DeleteChatConversationsForUserRequest {
            sub: sub.to_string(),
        });
        let response = self
            .client
            .delete_chat_conversations_for_user(request)
            .await?;
        Ok(response.into_inner().deleted)
    }
}

#[cfg(test)]
#[path = "conversations_tests.rs"]
mod tests;
