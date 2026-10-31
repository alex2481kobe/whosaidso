mod common;
use common::{Fixture as Project, flow::FlowFixture};
use serde_json::{Value, json};
use std::fs;

fn plan() -> Value {
    json!({"covers":{"paths":["future/**"],"boundaries":["api"]},
        "nodes":[{"id":"app","type":"feature"},
                 {"id":"screen","type":"screen","parent":"app","paths":["future/ui.rs"],"symbols":["show"],"refs":["node:api"]},
                 {"id":"api","type":"external","parent":"app","note":"Store API",
                  "risks":[{"id":"outage","note":"Store unavailable","mitigation":{"step":"retry"}}]}],
        "steps":[{"id":"request","from":"screen","to":"api","when":"purchase"},
                 {"id":"retry","from":"api","to":"screen","when":"request fails"}]})
}

#[test]
fn structural_validation_keeps_broken_flows_and_reports_all_problems() {
    let p = Project::empty();
    p.write("paywall", &plan());
    let good = p.run(&["flow", "check"], true);
    assert!(good.contains("paywall authored"), "{good}");
    let mut bad = plan();
    bad["covers"] = json!({"paths":[],"boundaries":[]});
    bad["nodes"][0]["parent"] = json!("screen");
    bad["nodes"][1]["status"] = json!("green");
    bad["nodes"][1]["refs"] = json!(["node:absent"]);
    bad["nodes"][2]["risks"][0]["mitigation"] = json!({"step":"absent"});
    bad["nodes"]
        .as_array_mut()
        .unwrap()
        .push(json!({"id":"api","type":"service","parent":"absent"}));
    bad["steps"][0]["to"] = json!("absent");
    bad["steps"][1]["id"] = json!("request");
    bad["steps"][1]["when"] = json!("");
    bad["steps"][0]["surprise"] = json!(true);
    bad["nodes"][1]["paths"] = json!(42);
    p.write("broken", &bad);
    fs::write(p.root.join(".whosaidso/flows/syntax.json"), "{").unwrap();
    let out = p.run(&["flow", "check"], false);
    for problem in [
        "paywall authored",
        "broken broken",
        "syntax broken",
        "covers",
        "parent cycle",
        "duplicate id",
        "unknown parent",
        "unknown node",
        "unknown step",
        "status",
        "when",
        "surprise",
        "paths",
        "refs",
    ] {
        assert!(out.contains(problem), "missing {problem}: {out}");
    }
    // Goal and note links are opaque ids; validation must not load their records.
    let mut linked = plan();
    linked["nodes"][0]["goal"] = json!("future-goal");
    linked["nodes"][2]["risks"][0]["mitigation"] = json!({"note":"store-policy"});
    p.write("linked", &linked);
    p.run(&["flow", "check", "linked"], true);
}

#[test]
fn builds_and_drills_into_a_paywall_plan_by_cli() {
    let p = Project::empty();
    p.run(
        &[
            "flow",
            "put",
            "paywall",
            "covers",
            r#"{"paths":["app/**","server/**"]}"#,
        ],
        true,
    );
    for node in [
        json!({"id":"paywall","type":"feature"}),
        json!({"id":"screen","type":"screen","parent":"paywall"}),
        json!({"id":"buy","type":"action","parent":"screen"}),
        json!({"id":"store","type":"store","parent":"paywall","note":"SDK errors handled by the server response"}),
        json!({"id":"server","type":"service","parent":"paywall","note":"No additional risk: verification delegated to the store API"}),
        json!({"id":"store-api","type":"external","parent":"paywall","note":"Receipt verification"}),
    ] {
        p.run(&["flow", "put", "paywall", "node", &node.to_string()], true);
    }
    for step in [
        json!({"id":"tap","from":"screen","to":"buy","when":"Buy tapped"}),
        json!({"id":"purchase","from":"buy","to":"store","when":"product selected"}),
        json!({"id":"verify","from":"store","to":"server","when":"receipt received"}),
        json!({"id":"validate","from":"server","to":"store-api","when":"verify receipt signature"}),
        json!({"id":"response","from":"store-api","to":"screen","when":"receipt valid"}),
        json!({"id":"error","from":"store-api","to":"screen","when":"invalid receipt or store unavailable"}),
    ] {
        p.run(&["flow", "put", "paywall", "step", &step.to_string()], true);
    }
    p.run(&["flow", "put", "paywall", "node", r#"{"id":"store-api","type":"external","parent":"paywall","risks":[{"id":"outage","note":"Store unavailable","mitigation":{"step":"error"}}]}"#], true);
    let top = p.run(&["flow", "show", "paywall"], true);
    assert!(top.contains("node paywall type=feature"), "{top}");
    assert!(!top.contains("node screen "), "{top}");
    let down = p.run(&["flow", "show", "paywall", "paywall"], true);
    for item in [
        "node screen ",
        "node store ",
        "node server ",
        "node store-api ",
        "when=invalid receipt",
        "risk outage",
        "mitigation=step:error",
        "frontier=1",
    ] {
        assert!(down.contains(item), "missing {item}: {down}");
    }
    assert!(!down.contains("node buy "), "{down}");
    let screen = p.run(&["flow", "show", "paywall", "screen"], true);
    assert!(screen.contains("node buy "), "{screen}");
    // Replacing a step retains its position, and a failed mutation preserves the file.
    p.run(
        &[
            "flow",
            "put",
            "paywall",
            "step",
            r#"{"id":"tap","from":"screen","to":"buy","when":"purchase tapped"}"#,
        ],
        true,
    );
    let file = p.root.join(".whosaidso/flows/paywall.json");
    let before = fs::read(&file).unwrap();
    let flow: Value = serde_json::from_slice(&before).unwrap();
    assert_eq!(flow["steps"][0]["id"], "tap");
    assert_eq!(flow["steps"][0]["when"], "purchase tapped");
    assert!(
        p.run(&["flow", "rm", "paywall", "step", "error"], false)
            .contains("unknown step")
    );
    p.run(&["flow", "rm", "paywall", "node", "screen"], false);
    p.run(
        &[
            "flow",
            "put",
            "paywall",
            "step",
            r#"{"id":"bad","from":"screen","to":"absent","when":"always"}"#,
        ],
        false,
    );
    p.run(&["flow", "show", "paywall", "absent"], false);
    assert_eq!(fs::read(&file).unwrap(), before);
    p.run(
        &[
            "flow",
            "put",
            "paywall",
            "node",
            r#"{"id":"unused","type":"function","shared":true}"#,
        ],
        true,
    );
    p.run(&["flow", "rm", "paywall", "node", "unused"], true);
    p.run(&["flow", "check"], true);
    let mut broken = flow;
    broken["covers"] = json!({});
    p.write("broken", &broken);
    assert!(
        p.run(&["flow", "show", "broken"], false)
            .contains("broken broken")
    );
    p.run(&["flow", "rm", "broken"], true);
    assert!(!p.root.join(".whosaidso/flows/broken.json").exists());
}
