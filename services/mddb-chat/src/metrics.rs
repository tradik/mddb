//! The server's counters, served at `/metrics` in the Prometheus text format.
//!
//! One family so far: `mddb_chat_loop_guard_triggered_total{scenario}`, the
//! answers the loop guard stopped (#413). Counters live as long as the
//! process, as Prometheus expects; a restart starts them at zero.

use std::collections::BTreeMap;
use std::sync::Mutex;

/// The name of the loop guard's counter.
pub const LOOP_GUARD_TRIGGERED: &str = "mddb_chat_loop_guard_triggered_total";

/// Process-wide counters.
#[derive(Debug, Default)]
pub struct Metrics {
    loop_guard: Mutex<BTreeMap<String, u64>>,
}

impl Metrics {
    /// Counts one answer the loop guard stopped in `scenario`.
    pub fn loop_guard_triggered(&self, scenario: &str) {
        let mut counts = self.loop_guard.lock().unwrap_or_else(|e| e.into_inner());
        *counts.entry(scenario.to_string()).or_default() += 1;
    }

    /// Every counter in the Prometheus text exposition format, 0.0.4.
    pub fn render(&self) -> String {
        let counts = self.loop_guard.lock().unwrap_or_else(|e| e.into_inner());
        let mut out = format!(
            "# HELP {LOOP_GUARD_TRIGGERED} Answers stopped by the repetition-loop guard.\n\
             # TYPE {LOOP_GUARD_TRIGGERED} counter\n"
        );
        for (scenario, count) in counts.iter() {
            let label = escape(scenario);
            out.push_str(&format!(
                "{LOOP_GUARD_TRIGGERED}{{scenario=\"{label}\"}} {count}\n"
            ));
        }
        out
    }
}

/// A label value as the text format writes it: backslash, quote and newline
/// escaped.
fn escape(value: &str) -> String {
    value
        .replace('\\', "\\\\")
        .replace('"', "\\\"")
        .replace('\n', "\\n")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_unused_counter_is_declared_with_no_samples() {
        let text = Metrics::default().render();
        assert!(text.contains("# TYPE mddb_chat_loop_guard_triggered_total counter"));
        assert!(!text.contains("{scenario="), "{text}");
    }

    #[test]
    fn each_scenario_counts_on_its_own_with_its_label_escaped() {
        let metrics = Metrics::default();
        metrics.loop_guard_triggered("support");
        metrics.loop_guard_triggered("support");
        metrics.loop_guard_triggered("odd\"name\\\nx");

        let text = metrics.render();
        assert!(text.contains("mddb_chat_loop_guard_triggered_total{scenario=\"support\"} 2\n"));
        assert!(
            text.contains(r#"mddb_chat_loop_guard_triggered_total{scenario="odd\"name\\\nx"} 1"#),
            "{text}"
        );
    }

    #[test]
    fn a_poisoned_lock_still_counts() {
        let metrics = std::sync::Arc::new(Metrics::default());
        let poisoner = metrics.clone();
        let _ = std::thread::spawn(move || {
            let _held = poisoner.loop_guard.lock().unwrap();
            panic!("poison the lock");
        })
        .join();
        metrics.loop_guard_triggered("s");
        assert!(metrics.render().contains("{scenario=\"s\"} 1"));
    }
}
