use super::super::{
    DEFAULT_RATIO, LoopDetected, LoopGuardConfig, MARKER, Unit, check, cut_at, stopped_answer,
};
use crate::grpc::client::proto::ChatLoopGuardProto;

#[test]
fn the_guard_is_on_with_conservative_defaults_unless_turned_off() {
    let on = LoopGuardConfig::default()
        .settings()
        .expect("on by default");
    assert_eq!(
        (on.ngram, on.window, on.ratio, on.min_output, on.unit),
        (64, 256, DEFAULT_RATIO, 200, Unit::Chars)
    );

    let words = LoopGuardConfig {
        unit: Some(Unit::Words),
        ..Default::default()
    };
    let words = words.settings().unwrap();
    assert_eq!((words.ngram, words.window, words.min_output), (16, 64, 50));

    let off = LoopGuardConfig {
        enabled: Some(false),
        ..Default::default()
    };
    assert_eq!(off.settings(), None);
    assert_eq!(check(None, &"loop ".repeat(500)), None, "off finds nothing");
}

#[test]
fn settings_that_cannot_work_are_settled_or_refused() {
    let odd = LoopGuardConfig {
        ngram: Some(0),
        window: Some(10),
        ratio: Some(1.5),
        min_output: Some(0),
        ..Default::default()
    };
    let settled = odd.settings().unwrap();
    assert_eq!(settled.ngram, 64, "0 is the default");
    assert_eq!(
        settled.window, 64,
        "a window is never shorter than the n-gram"
    );
    assert_eq!(settled.ratio, DEFAULT_RATIO);
    assert_eq!(settled.min_output, 0);
    assert!(odd.validate().is_err(), "but the TOML may not say so");

    for bad in [
        LoopGuardConfig {
            ratio: Some(0.0),
            ..Default::default()
        },
        LoopGuardConfig {
            window: Some(0),
            ..Default::default()
        },
    ] {
        assert!(bad.validate().is_err(), "{bad:?}");
    }
    let fine = LoopGuardConfig {
        ratio: Some(1.0),
        ..Default::default()
    };
    assert_eq!(fine.validate(), Ok(()));
}

#[test]
fn an_override_changes_only_what_it_names() {
    let base = LoopGuardConfig {
        enabled: Some(true),
        ngram: Some(32),
        window: Some(128),
        ratio: Some(0.7),
        min_output: Some(100),
        unit: Some(Unit::Chars),
    };
    let over = LoopGuardConfig {
        enabled: Some(false),
        unit: Some(Unit::Words),
        ..Default::default()
    };
    let merged = base.overlay(&over);
    assert_eq!(merged.enabled, Some(false));
    assert_eq!(merged.unit, Some(Unit::Words));
    assert_eq!(
        (merged.ngram, merged.window, merged.ratio, merged.min_output),
        (Some(32), Some(128), Some(0.7), Some(100))
    );
    assert_eq!(base.overlay(&LoopGuardConfig::default()), base);
}

#[test]
fn a_scenario_from_mddbd_sets_only_what_is_not_zero() {
    assert_eq!(
        LoopGuardConfig::from_proto(None),
        LoopGuardConfig::default()
    );
    let full = LoopGuardConfig::from_proto(Some(ChatLoopGuardProto {
        enabled: Some(true),
        ngram: 8,
        window: 40,
        ratio: 0.9,
        min_output: 10,
        unit: "words".to_string(),
    }));
    assert_eq!(
        full,
        LoopGuardConfig {
            enabled: Some(true),
            ngram: Some(8),
            window: Some(40),
            ratio: Some(0.9),
            min_output: Some(10),
            unit: Some(Unit::Words),
        }
    );
    let unset = LoopGuardConfig::from_proto(Some(ChatLoopGuardProto {
        ngram: -3,
        unit: "tokens".to_string(),
        ..Default::default()
    }));
    assert_eq!(unset, LoopGuardConfig::default());
    assert_eq!(Unit::parse("chars"), Some(Unit::Chars));
}

#[test]
fn the_toml_table_reads_every_field() {
    let config: LoopGuardConfig = toml::from_str(
        "enabled = true\nngram = 16\nwindow = 256\nratio = 0.8\nmin_output = 200\nunit = \"words\"",
    )
    .unwrap();
    assert_eq!(config.unit, Some(Unit::Words));
    assert_eq!(config.min_output, Some(200));
    assert!(toml::from_str::<LoopGuardConfig>("unit = \"tokens\"").is_err());
}

#[test]
fn a_stopped_answer_keeps_the_text_before_the_loop() {
    let found = |loop_start| LoopDetected {
        loop_start,
        position: 0,
        ratio: 1.0,
    };
    assert_eq!(
        stopped_answer("Answer.  \nloop loop", &found(10)),
        format!("Answer.\n\n{MARKER}")
    );
    assert_eq!(stopped_answer("loop loop", &found(0)), MARKER);
    // Found at the full stop the loop's paragraph also ends with: the
    // sentence keeps it.
    assert_eq!(
        stopped_answer("Once again. Once again. ", &found(10)),
        format!("Once again.\n\n{MARKER}")
    );
    assert_eq!(cut_at("ab ab", &found(99)), 5, "never past the end");
}
