//! The conversation calls against the fake mddbd, which applies mddbd's
//! rules: another sub's conversation is not found, an ended one refuses
//! appends.

use super::*;
use crate::grpc::testing::FakeMddbd;

fn said(role: &str, content: &str) -> proto::ChatMessageProto {
    proto::ChatMessageProto {
        role: role.to_string(),
        content: content.to_string(),
        ..Default::default()
    }
}

#[tokio::test]
async fn a_conversation_is_created_appended_to_and_read_back_in_order() {
    let fake = FakeMddbd::start().await;
    let mut client = fake.client().await;

    let created = client
        .create_conversation("user-42", "support", "Ada", 30)
        .await
        .expect("create");
    assert_eq!(
        (created.sub.as_str(), created.scenario.as_str()),
        ("user-42", "support")
    );
    assert_eq!(
        (created.display_name.as_str(), created.retention_days),
        ("Ada", 30)
    );

    for text in ["one", "two", "three"] {
        client
            .append_messages(&created.id, "user-42", vec![said("user", text)])
            .await
            .expect("append");
    }

    let last_two = client
        .get_conversation(&created.id, "user-42", 2)
        .await
        .expect("get");
    let contents: Vec<&str> = last_two
        .messages
        .iter()
        .map(|m| m.content.as_str())
        .collect();
    assert_eq!(contents, vec!["two", "three"]);
    assert_eq!(last_two.message_count, 3);

    let all = client
        .get_conversation(&created.id, "user-42", 0)
        .await
        .expect("get all");
    assert_eq!(all.messages.len(), 3);
}

#[tokio::test]
async fn a_listing_is_newest_first_by_scenario_and_without_messages() {
    let fake = FakeMddbd::start().await;
    let mut client = fake.client().await;
    let old = client
        .create_conversation("u", "support", "", 0)
        .await
        .unwrap();
    let new = client
        .create_conversation("u", "support", "", 0)
        .await
        .unwrap();
    client
        .create_conversation("u", "other", "", 0)
        .await
        .unwrap();
    client
        .append_messages(&old.id, "u", vec![said("user", "hi")])
        .await
        .unwrap();

    let listed = client.list_conversations("u", "support", 50).await.unwrap();

    let ids: Vec<&str> = listed.iter().map(|c| c.id.as_str()).collect();
    assert_eq!(ids, vec![old.id.as_str(), new.id.as_str()]);
    assert!(listed.iter().all(|c| c.messages.is_empty()));
    assert_eq!(
        client
            .list_conversations("u", "support", 1)
            .await
            .unwrap()
            .len(),
        1
    );
}

#[tokio::test]
async fn another_users_conversation_is_not_found() {
    let fake = FakeMddbd::start().await;
    let mut client = fake.client().await;
    let theirs = client
        .create_conversation("alice", "support", "", 0)
        .await
        .unwrap();

    let read = client.get_conversation(&theirs.id, "mallory", 0).await;
    let write = client
        .append_messages(&theirs.id, "mallory", vec![said("user", "x")])
        .await;
    let end = client.end_conversation(&theirs.id, "mallory").await;

    for result in [read.map(|_| ()), write, end] {
        let err = result.expect_err("refused").to_string();
        assert!(err.contains("not found"), "{err}");
    }
    assert!(
        client
            .list_conversations("mallory", "", 50)
            .await
            .unwrap()
            .is_empty()
    );
}

#[tokio::test]
async fn an_ended_conversation_refuses_appends() {
    let fake = FakeMddbd::start().await;
    let mut client = fake.client().await;
    let conversation = client
        .create_conversation("u", "support", "", 0)
        .await
        .unwrap();

    client
        .end_conversation(&conversation.id, "u")
        .await
        .expect("end");

    let err = client
        .append_messages(&conversation.id, "u", vec![said("user", "late")])
        .await
        .expect_err("refused");
    assert!(err.to_string().contains("ended"), "{err}");
    assert!(fake.conversation(&conversation.id).unwrap().ended);
}

#[tokio::test]
async fn deleting_a_user_removes_only_theirs() {
    let fake = FakeMddbd::start().await;
    let mut client = fake.client().await;
    for sub in ["u", "u", "someone-else"] {
        client
            .create_conversation(sub, "support", "", 0)
            .await
            .unwrap();
    }

    assert_eq!(client.delete_user_conversations("u").await.unwrap(), 2);
    assert!(fake.conversations_of("u").is_empty());
    assert_eq!(fake.conversations_of("someone-else").len(), 1);
}

#[tokio::test]
async fn a_failing_mddbd_is_an_error_on_every_call() {
    let fake = FakeMddbd::start().await;
    let mut client = fake.client().await;
    fake.fail_with(Some(tonic::Code::Unavailable));

    assert!(client.create_conversation("u", "s", "", 0).await.is_err());
    assert!(client.list_conversations("u", "s", 1).await.is_err());
    assert!(client.delete_user_conversations("u").await.is_err());
}
