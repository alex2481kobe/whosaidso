mod common;
use common::process::*;
use common::{records as r, *};
use serde_json::json;
use std::fs;

#[test]
fn states_follow_work_and_only_landed_proofs() {
    let f = Fixture::new();
    let root = &f.root;

    assert!(cli(root, &["goals"]).contains("open    g"));
    git(root, &["checkout", "-q", "-b", "worker"]);
    work(root, "work", "g");
    assert!(cli(root, &["goals"]).contains("active  g"));
    cli(root, &["prove", "g"]);
    commit(root);
    assert!(!cli(root, &["goals"]).contains("done    g"));
    git(root, &["checkout", "-q", "main"]);
    git(root, &["merge", "-q", "--no-edit", "worker"]);
    assert!(cli(root, &["goals"]).contains("done    g"));
    work(root, "later", "g");
    assert!(cli(root, &["goals"]).contains("stale   g"));
    cli(root, &["prove", "g"]);
    assert!(cli(root, &["goals"]).contains("done    g"));
}

#[test]
fn landed_work_without_evidence_stays_landed() {
    let f = Fixture::new();
    let root = &f.root;

    work(root, "work", "g");
    assert!(cli(root, &["goals"]).contains("landed  g"));
    assert!(!cli(root, &["show", "g"]).contains("proof:"));
}

#[test]
fn record_only_commits_do_not_count_as_work() {
    let f = Fixture::new();
    let root = &f.root;

    let p = r::goal(root, "h");
    git(root, &["add", p.to_str().unwrap()]);
    git(root, &["commit", "-q", "-m", "records", "-m", "Goal: g"]);
    assert!(cli(root, &["goals"]).contains("open    g"));
}

#[test]
fn newest_proof_uses_nanoseconds_then_hash() {
    let f = Fixture::new();
    let root = &f.root;

    let at = head(root);
    r::proof(root, &at, 1, 0);
    r::proof(root, &at, 2, 1);
    assert!(cli(root, &["goals"]).contains("failing g"));
    let pass = r::proof(root, &at, 3, 0);
    let fail = r::proof(root, &at, 3, 1);
    assert!(cli(root, &["show", "g"]).contains(if pass > fail {
        "proof: pass"
    } else {
        "proof: fail"
    }));
}

#[test]
fn proof_requires_current_check_bytes_at_head_and_worktree() {
    let f = Fixture::new();
    let root = &f.root;

    r::proof(root, &head(root), 1, 0);
    assert!(cli(root, &["goals"]).contains("done    g"));
    let path = root.join(".whosaidso/instruments/test.json");
    let original = fs::read(&path).unwrap();
    r::instrument(root, &["false"]);
    assert!(cli(root, &["goals"]).contains("open    g"));
    commit(root);
    write(&path, original);
    assert!(cli(root, &["goals"]).contains("open    g"));
}

#[test]
fn unlanded_or_wrong_check_proofs_cannot_replace_selected_evidence() {
    let f = Fixture::new();
    let root = &f.root;

    let at = head(root);
    r::proof(root, &at, 1, 0);
    git(root, &["checkout", "-q", "-b", "worker"]);
    work(root, "branch", "g");
    let branch = head(root);
    git(root, &["checkout", "-q", "main"]);
    r::proof(root, &branch, 3, 1);
    let p = r::proof(root, &at, 4, 1);
    let mut v = value(&p);
    fs::remove_file(p).unwrap();
    r::put(
        root,
        "instruments",
        "other",
        &json!({"run":["false"],"answers":"other"}),
    );
    v["instrument"] = "other".into();
    r::put(root, "proofs", "", &v);
    assert!(cli(root, &["goals"]).contains("done    g"));
    assert!(cli(root, &["show", "g"]).contains(&at[..7]));
}

#[test]
fn remote_main_never_changes_local_verdict() {
    let f = Fixture::new();
    let root = &f.root;

    let at = head(root);
    work(root, "work", "g");
    r::proof(root, &head(root), 1, 0);
    let before = cli(root, &["goals"]);
    let shown = cli(root, &["show", "g"]);
    git(root, &["update-ref", "refs/remotes/origin/main", &at]);
    assert_eq!(cli(root, &["goals"]), before);
    assert_eq!(cli(root, &["show", "g"]), shown);
}

#[test]
fn idle_branch_work_remains_visible_with_worker_details() {
    let f = Fixture::new();
    let root = &f.root;

    git(root, &["checkout", "-q", "-b", "worker"]);
    write(&root.join("work"), "work");
    git(root, &["add", "work"]);
    success(
        command(
            root,
            "git",
            &["commit", "-q", "-m", "work", "-m", "Goal: g"],
        )
        .env("GIT_COMMITTER_DATE", "2000-01-01T00:00:00Z")
        .output()
        .unwrap(),
    );
    git(root, &["checkout", "-q", "main"]);
    let text = cli(root, &["goals"]);
    assert!(
        text.contains("idle    g") && text.contains("worker") && text.contains("Test"),
        "{text}"
    );
}

#[test]
fn local_branch_names_shadow_remote_branches_and_main_is_never_work() {
    let f = Fixture::new();
    let root = &f.root;
    let at = head(root);
    git(root, &["checkout", "-q", "-b", "worker"]);
    work(root, "work", "g");
    let tip = head(root);
    git(root, &["checkout", "-q", "main"]);
    git(root, &["update-ref", "refs/remotes/origin/worker", &tip]);
    git(root, &["update-ref", "refs/heads/worker", &at]);
    git(root, &["update-ref", "refs/remotes/else/main", &tip]);
    git(root, &["update-ref", "refs/remotes/else/HEAD", &tip]);
    assert!(cli(root, &["goals"]).contains("open    g"));
    git(root, &["branch", "-D", "worker"]);
    let text = cli(root, &["goals"]);
    assert!(
        text.contains("origin/worker") && !text.contains("else/"),
        "{text}"
    );
}
