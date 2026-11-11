//! The fake mddbd's conversation store (#405), with mddbd's rules: another
//! sub's conversation is "not found", an ended one refuses appends, a listing
//! is newest first and a read keeps the last `limit` messages.

use tonic::Status;

use super::FakeState;
use crate::grpc::client::proto;

/// The fake's clock: one tick per write, so "newest" is the last written.
fn tick(state: &mut FakeState) -> i64 {
    state.clock += 1;
    state.clock
}

/// The conversation `id` if it belongs to `sub`.
fn owned<'a>(
    state: &'a mut FakeState,
    id: &str,
    sub: &str,
) -> Result<&'a mut proto::ChatConversationProto, Status> {
    state
        .conversations
        .get_mut(id)
        .filter(|c| c.sub == sub)
        .ok_or_else(|| Status::not_found("conversation not found"))
}

fn summary(conversation: &proto::ChatConversationProto) -> proto::ChatConversationProto {
    proto::ChatConversationProto {
        messages: Vec::new(),
        ..conversation.clone()
    }
}

pub fn create(
    state: &mut FakeState,
    request: proto::CreateChatConversationRequest,
) -> Result<proto::ChatConversationProto, Status> {
    let now = tick(state);
    let conversation = proto::ChatConversationProto {
        id: format!("conv-{now}"),
        scenario: request.scenario,
        sub: request.sub,
        display_name: request.display_name,
        created_at: now,
        updated_at: now,
        retention_days: request.retention_days,
        ..Default::default()
    };
    state
        .conversations
        .insert(conversation.id.clone(), conversation.clone());
    Ok(conversation)
}

pub fn list(
    state: &mut FakeState,
    request: proto::ListChatConversationsRequest,
) -> Result<proto::ListChatConversationsResponse, Status> {
    let mut conversations: Vec<_> = state
        .conversations
        .values()
        .filter(|c| c.sub == request.sub)
        .filter(|c| request.scenario.is_empty() || c.scenario == request.scenario)
        .map(summary)
        .collect();
    conversations.sort_by_key(|c| std::cmp::Reverse(c.last_message_at.max(c.created_at)));
    conversations.truncate(usize::try_from(request.limit).unwrap_or(usize::MAX));
    Ok(proto::ListChatConversationsResponse {
        conversations,
        next_cursor: String::new(),
    })
}

pub fn get(
    state: &mut FakeState,
    request: proto::GetChatConversationRequest,
) -> Result<proto::ChatConversationProto, Status> {
    let mut conversation = owned(state, &request.id, &request.sub)?.clone();
    let limit = usize::try_from(request.limit).unwrap_or(0);
    if limit > 0 && conversation.messages.len() > limit {
        let cut = conversation.messages.len() - limit;
        conversation.messages.drain(..cut);
    }
    Ok(conversation)
}

pub fn append(
    state: &mut FakeState,
    request: proto::AppendChatMessagesRequest,
) -> Result<proto::ChatConversationProto, Status> {
    let now = tick(state);
    let conversation = owned(state, &request.id, &request.sub)?;
    if conversation.ended {
        return Err(Status::failed_precondition("conversation has ended"));
    }
    let added: Vec<_> = request
        .messages
        .into_iter()
        .enumerate()
        .map(|(i, m)| proto::ChatMessageProto {
            id: format!("msg-{now}-{i}"),
            created_at: now,
            ..m
        })
        .collect();
    conversation.messages.extend(added.iter().cloned());
    conversation.message_count = i32::try_from(conversation.messages.len()).unwrap_or(i32::MAX);
    conversation.last_message_at = now;
    Ok(proto::ChatConversationProto {
        messages: added,
        ..summary(conversation)
    })
}

pub fn end(
    state: &mut FakeState,
    request: proto::EndChatConversationRequest,
) -> Result<proto::ChatConversationProto, Status> {
    let conversation = owned(state, &request.id, &request.sub)?;
    conversation.ended = true;
    Ok(summary(conversation))
}

pub fn delete_for_user(
    state: &mut FakeState,
    request: proto::DeleteChatConversationsForUserRequest,
) -> Result<proto::DeleteChatConversationsForUserResponse, Status> {
    let before = state.conversations.len();
    state.conversations.retain(|_, c| c.sub != request.sub);
    let deleted = before - state.conversations.len();
    Ok(proto::DeleteChatConversationsForUserResponse {
        deleted: i32::try_from(deleted).unwrap_or(i32::MAX),
        sub: request.sub,
    })
}
