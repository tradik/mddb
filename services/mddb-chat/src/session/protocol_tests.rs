//! #405: the wire shape of what a signed-in user sends and receives. The
//! tags are the protocol the widget and host apps speak.

use serde_json::json;

use crate::session::conversation::{ConversationSummary, HistoryEntry};
use crate::session::types::{ChatMessage, MessageRole, WsIncoming, WsOutgoing};

fn incoming(value: serde_json::Value) -> WsIncoming {
    serde_json::from_value(value).expect("parse")
}

fn outgoing(msg: &WsOutgoing) -> serde_json::Value {
    serde_json::to_value(msg).expect("serialise")
}

#[test]
fn a_signed_join_carries_its_token_and_conversation_choice() {
    let join = incoming(json!({
        "type": "join", "scenario": "support", "token": "v1.a.b",
        "conversation_id": "c1", "new_conversation": true,
    }));
    let WsIncoming::Join {
        name,
        token,
        conversation_id,
        new_conversation,
        ..
    } = join
    else {
        panic!("not a join");
    };
    assert_eq!(name, "", "a name is optional next to a token");
    assert_eq!(token.as_deref(), Some("v1.a.b"));
    assert_eq!(conversation_id.as_deref(), Some("c1"));
    assert!(new_conversation);
}

#[test]
fn an_old_join_has_none_of_the_new_fields() {
    let join = incoming(json!({"type": "join", "name": "Ada"}));
    assert!(matches!(
        join,
        WsIncoming::Join {
            token: None,
            conversation_id: None,
            new_conversation: false,
            ..
        }
    ));
}

#[test]
fn the_conversation_controls_use_snake_case_tags() {
    assert!(matches!(
        incoming(json!({"type": "list_conversations"})),
        WsIncoming::ListConversations
    ));
    assert!(matches!(
        incoming(json!({"type": "delete_conversations"})),
        WsIncoming::DeleteConversations { confirm: false }
    ));
    assert!(matches!(
        incoming(json!({"type": "delete_conversations", "confirm": true})),
        WsIncoming::DeleteConversations { confirm: true }
    ));
}

#[test]
fn a_session_frame_names_its_conversation_only_when_it_has_one() {
    let anonymous = WsOutgoing::Session {
        id: "s".into(),
        scenario: "a".into(),
        conversation_id: None,
    };
    assert_eq!(
        outgoing(&anonymous),
        json!({"type": "session", "id": "s", "scenario": "a"})
    );
    let signed = WsOutgoing::Session {
        id: "s".into(),
        scenario: "a".into(),
        conversation_id: Some("c".into()),
    };
    assert_eq!(outgoing(&signed)["conversation_id"], "c");
}

#[test]
fn the_new_frames_have_their_documented_shapes() {
    let history = WsOutgoing::History {
        messages: vec![HistoryEntry {
            role: "user".into(),
            content: "hi".into(),
        }],
    };
    assert_eq!(
        outgoing(&history),
        json!({"type": "history", "messages": [{"role": "user", "content": "hi"}]})
    );

    let listed = WsOutgoing::Conversations {
        conversations: vec![ConversationSummary {
            id: "c".into(),
            title: "t".into(),
            created_at: 1,
            last_message_at: 2,
            message_count: 3,
            ended: false,
        }],
    };
    assert_eq!(
        outgoing(&listed),
        json!({"type": "conversations", "conversations": [{
            "id": "c", "title": "t", "created_at": 1, "last_message_at": 2,
            "message_count": 3, "ended": false,
        }]})
    );

    let deleted = WsOutgoing::ConversationsDeleted {
        deleted: 4,
        conversation_id: "n".into(),
    };
    assert_eq!(
        outgoing(&deleted),
        json!({"type": "conversations_deleted", "deleted": 4, "conversation_id": "n"})
    );
}

#[test]
fn a_restored_flag_is_written_only_when_set() {
    let mut message = ChatMessage {
        role: MessageRole::Assistant,
        content: "x".into(),
        timestamp: 0,
        restored: false,
    };
    assert!(
        serde_json::to_value(&message)
            .unwrap()
            .get("restored")
            .is_none()
    );
    message.restored = true;
    assert_eq!(serde_json::to_value(&message).unwrap()["restored"], true);
}
