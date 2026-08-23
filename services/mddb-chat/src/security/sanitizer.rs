use crate::config::SecurityConfig;
use crate::error::AppError;

pub struct Sanitizer {
    config: SecurityConfig,
}

impl Sanitizer {
    pub fn new(config: SecurityConfig) -> Self {
        Self { config }
    }

    /// Sanitize and validate user message input
    pub fn sanitize_message(&self, input: &str) -> Result<String, AppError> {
        if input.len() > self.config.max_message_length {
            return Err(AppError::MessageTooLong);
        }

        let sanitized = strip_html_tags(input);
        let sanitized = sanitized.trim().to_string();

        if sanitized.is_empty() {
            return Err(AppError::InvalidMessage("empty message".to_string()));
        }

        Ok(sanitized)
    }

    /// Sanitize user name
    pub fn sanitize_name(&self, name: &str, max_chars: usize) -> Result<String, AppError> {
        let trimmed = name.trim();

        if trimmed.is_empty() {
            return Err(AppError::InvalidMessage("name is required".to_string()));
        }

        if trimmed.chars().count() > max_chars {
            return Err(AppError::NameTooLong);
        }

        Ok(strip_html_tags(trimmed))
    }
}

/// Simple HTML tag stripping
fn strip_html_tags(input: &str) -> String {
    let mut result = String::with_capacity(input.len());
    let mut in_tag = false;

    for ch in input.chars() {
        match ch {
            '<' => in_tag = true,
            '>' if in_tag => in_tag = false,
            _ if !in_tag => result.push(ch),
            _ => {}
        }
    }

    result
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_config() -> SecurityConfig {
        SecurityConfig {
            rate_limit_per_minute: 30,
            max_message_length: 2000,
            webhook_secret: String::new(),
            trusted_proxies: Vec::new(),
            max_tokens_per_session: 0,
        }
    }

    #[test]
    fn test_sanitize_message_strips_html() {
        let s = Sanitizer::new(test_config());
        let result = s
            .sanitize_message("hello <script>alert('xss')</script> world")
            .unwrap();
        assert_eq!(result, "hello alert('xss') world");
    }

    #[test]
    fn test_sanitize_message_too_long() {
        let s = Sanitizer::new(test_config());
        let long_msg = "a".repeat(2001);
        assert!(s.sanitize_message(&long_msg).is_err());
    }

    #[test]
    fn test_sanitize_message_empty() {
        let s = Sanitizer::new(test_config());
        assert!(s.sanitize_message("   ").is_err());
    }

    #[test]
    fn test_sanitize_name() {
        let s = Sanitizer::new(test_config());
        assert_eq!(s.sanitize_name("  Jan  ", 50).unwrap(), "Jan");
    }

    #[test]
    fn test_sanitize_name_too_long() {
        let s = Sanitizer::new(test_config());
        let name = "a".repeat(51);
        assert!(s.sanitize_name(&name, 50).is_err());
    }
}
