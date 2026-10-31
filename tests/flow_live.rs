mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::{Value, json};
use std::time::Instant;

#[test]
fn live_projection_moves_and_drift_gate_refuses_removed_hop() {
    let f = Fixture::sources(&[("app.rs", "fn screen() { action(); }\nfn action() {}\n")]);
    f.write("app", &json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"screen","type":"screen","symbols":["screen"]},
        {"id":"action","type":"action","symbols":["action"]}
    ],"steps":[{"id":"tap","from":"screen","to":"action","when":"tap"}]}));
    let view = || -> Value { serde_json::from_str(&cli(&f.root, &["flow","view","app"])).unwrap() };
    cli(&f.root, &["flow","drift-check"]);
    let before = view();
    let start = Instant::now();
    write(&f.root.join("app.rs"), "fn screen() {}\nfn action() {}\n");
    let after = view();
    assert_ne!(before["revision"], after["revision"]);
    assert_eq!(after["state"], "drifted");
    assert!(start.elapsed().as_secs_f64() < 3.0);
    bad(&f.root, &["flow","drift-check"], "drifted");
    write(&f.root.join("app.xyz"), "unsupported");
    f.write("unreadable", &json!({"covers":{"paths":["app.xyz"]},"nodes":[],"steps":[]}));
    bad(&f.root, &["flow","drift-check"], "unknown");
}

#[test]
fn precise_edits_are_labelled_stale_while_syntax_moves() {
    let f = Fixture::sources(&[("app.rs", "fn old() {}\n")]);
    common::scip::import(&f.root, &[common::scip::doc("app.rs", "scip-test pkg project 1 old().", "", &[(&[0,3,6],true)])]);
    let view = || -> Value { serde_json::from_str(&cli(&f.root, &["flow","view","$map"])).unwrap() };
    let before = view();
    assert_eq!(before["freshness"], "fresh");
    write(&f.root.join("app.rs"), "fn new() {}\n");
    let after = view();
    assert_eq!(after["freshness"], "stale");
    assert_ne!(before["revision"], after["revision"]);
}
