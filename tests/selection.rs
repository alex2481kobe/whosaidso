mod common;
use common::process::*;
use common::{records as r, *};
use serde_json::json;

#[test]
fn roadmap_is_unbounded_but_focus_dependencies_and_active_work_are_limited() {
    let f = Fixture::new();
    let root = &f.root;
    cli(root, &["install-hooks"]);
    for n in 0..300 {
        let p = r::goal(root, &format!("later-{n:03}"));
        let mut g = value(&p);
        g["order"] = json!(n + 2);
        r::put(root, "goals", &format!("later-{n:03}"), &g);
    }
    write(
        &root.join(".whosaidso/config.json"),
        r#"{"focus":1,"limits":{"active":1}}"#,
    );
    commit(root);
    cli(root, &["check"]);
    bad(root, &["start", "later-000"], "focus");
    for n in 1..300 {
        std::fs::remove_file(root.join(format!(".whosaidso/goals/later-{n:03}.json"))).unwrap();
    }
    let p = r::goal(root, "child");
    let mut g = value(&p);
    g["parent"] = json!("g");
    g["after"] = json!(["later-000"]);
    r::put(root, "goals", "child", &g);
    bad(root, &["start", "child"], "after later-000");
    g["after"] = json!([]);
    r::put(root, "goals", "child", &g);
    commit(root);
    let lane = f.worktree("lane");
    assert!(cli(root, &["next"]).contains("child"));
    bad(&lane, &["start", "g"], "limits.active");
    cli(root, &["start", "none"]);
    cli(&lane, &["start", "g"]);
}

#[test]
fn next_workers_take_different_goals() {
    let f = Fixture::new();
    let root = &f.root;
    cli(root, &["install-hooks"]);
    for id in ["a", "b"] {
        let p = r::goal(root, id);
        let mut g = value(&p);
        g["parent"] = json!("g");
        r::put(root, "goals", id, &g);
    }
    commit(root);
    let lane = f.worktree("lane");
    assert!(cli(root, &["next"]).contains("a"));
    assert!(cli(&lane, &["next"]).contains("b"));
}

#[test]
fn assignments_name_the_agent_and_allow_the_named_branch() {
    let f = Fixture::new();
    let root = &f.root;
    cli(root, &["install-hooks"]);
    for id in ["a", "b"] {
        let p = r::goal(root, id);
        let mut g = value(&p);
        g["parent"] = json!("g");
        r::put(root, "goals", id, &g);
    }
    commit(root);
    let lane = f.worktree("lane");
    cli(root, &["assign", "a", "agent:alice"]);
    failure(
        command(&lane, BIN, &["start", "a"])
            .env("WHOSAIDSO_AGENT", "bob")
            .output()
            .unwrap(),
        "alice",
    );
    success(
        command(&lane, BIN, &["start", "a"])
            .env("WHOSAIDSO_AGENT", "alice")
            .output()
            .unwrap(),
    );
    cli(root, &["assign", "b", "branch:lane"]);
    bad(root, &["start", "b"], "lane");
    cli(&lane, &["start", "b"]);
}
