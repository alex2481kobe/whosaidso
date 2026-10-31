mod common;
use common::process::*;
use common::{records as r, viewer::Viewer, *};
use std::time::{SystemTime, UNIX_EPOCH};

#[test]
fn viewer_hides_expired_claims_and_keeps_live_claims() {
    let f = Fixture::new();
    let root = &f.root;
    r::goal(root, "h");
    commit(root);
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs();
    for (id, hours) in [("g", 1), ("h", 100)] {
        let body = format!(
            "worker ghost-{id}\nbranch ghost\npath /ghost/tree\nauthor Ghost\nsince {}\n",
            now - hours * 3600
        );
        let oid = input(root, &["hash-object", "-w", "--stdin"], body.as_bytes());
        git(
            root,
            &["update-ref", &format!("refs/whosaidso/claims/{id}"), &oid],
        );
    }
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    let goals = viewer.json("/api/goals", &cookie);
    for goal in goals["goals"].as_array().unwrap() {
        if goal["id"] == "g" {
            assert_eq!(goal["claim"]["author"], "Ghost");
        } else {
            assert_eq!(goal["claim"], serde_json::Value::Null);
        }
    }
}

#[test]
fn viewer_keeps_a_claim_until_its_goal_is_done() {
    let f = Fixture::new();
    let root = &f.root;
    let lane = f.worktree("worker");
    work(&lane, "work", "g");
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs();
    let body =
        format!("worker ghost\nbranch worker\npath /ghost/tree\nauthor Ghost\nsince {now}\n");
    let oid = input(root, &["hash-object", "-w", "--stdin"], body.as_bytes());
    git(root, &["update-ref", "refs/whosaidso/claims/g", &oid]);
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    assert_eq!(
        viewer.json("/api/goals", &cookie)["goals"][0]["claim"]["author"],
        "Ghost"
    );
    git(root, &["merge", "-q", "--no-ff", "worker", "-m", "land"]);
    let goal = viewer.json("/api/goals", &cookie)["goals"][0].clone();
    assert_eq!(goal["state"], "landed");
    assert_eq!(goal["claim"]["author"], "Ghost");
}
