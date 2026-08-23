use crate::config::Config;
use crate::session::types::{ChatMessage, MessageRole};

/// Join the operator's instruction with the collection's (RAG-002).
///
/// Order is deliberate: the scenario prompt is the operator's policy — who the
/// assistant is and what it may say — while the collection prompt is about the
/// shape of its data. Policy first, so a collection cannot talk its way past it
/// by opening with "ignore your previous instructions"; a later line arguing
/// with an earlier one is a much weaker position than the reverse.
pub fn compose_system_prompt(operator_prompt: &str, collection_prompt: &str) -> String {
    let operator = operator_prompt.trim();
    let collection = collection_prompt.trim();

    match (operator.is_empty(), collection.is_empty()) {
        (_, true) => operator.to_string(),
        (true, false) => collection.to_string(),
        (false, false) => format!("{operator}\n\n{collection}"),
    }
}

/// Build the full message list for the LLM call
pub fn build_messages(
    config: &Config,
    scenario_name: &str,
    collection_prompt: &str,
    context: &str,
    history: &[ChatMessage],
) -> Vec<ChatMessage> {
    let mut messages = Vec::new();

    // System prompt from scenario
    let system_prompt = if let Some(scenario) = config.get_scenario(scenario_name) {
        &scenario.system_prompt
    } else {
        "You are a helpful assistant. Answer questions based on the provided context."
    };

    let mut system_content = compose_system_prompt(system_prompt, collection_prompt);

    // Append RAG context if available
    if !context.is_empty() {
        system_content.push_str("\n\n");
        system_content.push_str("Use the following documentation to answer the user's question. ");
        system_content.push_str("If the answer is not in the documentation, say so honestly.\n\n");
        system_content.push_str(context);
    }

    messages.push(ChatMessage {
        role: MessageRole::System,
        content: system_content,
        timestamp: 0,
    });

    // Conversation history
    messages.extend_from_slice(history);

    messages
}

/// Get the temperature for a scenario (or default)
pub fn get_temperature(config: &Config, scenario_name: &str) -> f32 {
    config
        .get_scenario(scenario_name)
        .and_then(|s| s.temperature)
        .unwrap_or(config.llm.temperature)
}

/// Get the collection to search for a scenario
pub fn get_collection(config: &Config, scenario_name: &str) -> String {
    config
        .get_scenario(scenario_name)
        .and_then(|s| s.allowed_collections.first().cloned())
        .unwrap_or_else(|| config.mddb.default_collection.clone())
}

#[cfg(test)]
mod tests {
    use super::compose_system_prompt;

    #[test]
    fn collection_prompt_follows_the_operator_prompt() {
        let got = compose_system_prompt("You are terse.", "Answer in numbered steps.");
        assert_eq!(got, "You are terse.\n\nAnswer in numbered steps.");
    }

    #[test]
    fn a_collection_without_a_prompt_changes_nothing() {
        assert_eq!(
            compose_system_prompt("You are terse.", ""),
            "You are terse."
        );
        assert_eq!(
            compose_system_prompt("You are terse.", "   "),
            "You are terse."
        );
    }

    #[test]
    fn a_collection_prompt_alone_is_used_as_is() {
        assert_eq!(
            compose_system_prompt("", "Answer in steps."),
            "Answer in steps."
        );
        assert_eq!(compose_system_prompt("", ""), "");
    }

    #[test]
    fn surrounding_whitespace_does_not_leak_into_the_prompt() {
        let got = compose_system_prompt("  operator  ", "\n collection \n");
        assert_eq!(got, "operator\n\ncollection");
    }
}
