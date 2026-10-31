mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::json;

#[test]
fn drift_check_prints_a_success_verdict() {
    let f = Fixture::sources(&[("README.md", "Planned app\n")]);
    assert_eq!(
        cli(&f.root, &["flow", "drift-check"]),
        "flow drift-check: passed\n"
    );
    f.write(
        "report",
        &json!({
            "covers":{"paths":["future/**"]},
            "nodes":[{"id":"report","type":"feature","paths":["future/report.rs"]}],
            "steps":[]
        }),
    );
    assert_eq!(
        cli(&f.root, &["flow", "drift-check"]),
        "flow drift-check: passed\n"
    );
}

#[test]
fn flow_check_ends_with_a_valid_verdict() {
    let f = Fixture::empty();
    f.write(
        "report",
        &json!({
            "covers":{"paths":["future/**"]},
            "nodes":[{"id":"report","type":"feature"}],"steps":[]
        }),
    );
    let out = cli(&f.root, &["flow", "check", "report"]);
    assert!(out.contains("report authored"), "{out}");
    assert_eq!(out.lines().last(), Some("flow check: valid"));
}

#[test]
fn flow_check_ends_with_an_invalid_verdict() {
    let f = Fixture::empty();
    write(&f.root.join(".whosaidso/flows/report.json"), "{");
    let out = bad(&f.root, &["flow", "check", "report"], "report broken");
    assert_eq!(out.lines().last(), Some("flow check: invalid"));
}
