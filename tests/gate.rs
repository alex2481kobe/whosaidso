mod common;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn hooks_keep_independent_worktree_selections() {
    let f = Fixture::new();
    let root = &f.root;

    bad(root, &["start", "g"], "install-hooks");
    cli(root, &["install-hooks"]);
    commit(root);
    let lane = f.worktree("lane");
    cli(&lane, &["start", "g"]);
    cli(root, &["start", "none"]);
    work(&lane, "work", "g");
    assert_eq!(
        git(&lane, &["log", "-1", "--format=%B"])
            .matches("Goal: g")
            .count(),
        1
    );
    git(root, &["commit", "-q", "--allow-empty", "-m", "main work"]);
    assert!(git(root, &["log", "-1", "--format=%B"]).contains("Goal: none"));
    assert!(cli(&lane, &["gate"]).contains("ok"));
}

#[test]
fn gate_rejects_missing_duplicate_and_unknown_trailers() {
    let f = Fixture::new();
    let root = &f.root;

    git(root, &["checkout", "-q", "-b", "worker"]);
    for message in [
        "no trailer",
        "work\n\nGoal: g\nGoal: none",
        "work\n\nGoal: missing",
    ] {
        git(root, &["commit", "-q", "--allow-empty", "-m", message]);
        assert!(
            !command(root, BIN, &["gate"])
                .output()
                .unwrap()
                .status
                .success(),
            "{message}"
        );
    }
}

#[test]
fn gate_accepts_local_tags_commits_and_ignores_merge_commits() {
    let f = Fixture::new();
    let root = &f.root;

    let base = head(root);
    git(root, &["tag", "-a", "local-base", "-m", "base"]);
    git(root, &["checkout", "-q", "-b", "worker"]);
    work(root, "work", "g");
    for base in [
        "main",
        "local-base",
        "refs/tags/local-base",
        base.as_str(),
        "HEAD",
    ] {
        assert!(cli(root, &["gate", base]).contains("ok"));
    }
    git(root, &["checkout", "-q", "main"]);
    work(root, "main-work", "none");
    git(root, &["merge", "-q", "--no-ff", "worker", "-m", "merge"]);
    cli(root, &["gate", "local-base"]);
    git(root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    bad(root, &["gate", "origin/main"], "local branch");
}

#[test]
fn gate_refuses_untracked_modified_deleted_and_staged_records() {
    let f = Fixture::new();
    let root = &f.root;

    for kind in ["phases", "goals", "instruments", "proofs", "asks", "media"] {
        let path = root.join(format!(".whosaidso/{kind}/new.json"));
        write(&path, "{}");
        bad(root, &["gate"], "uncommitted");
        fs::remove_file(path).unwrap();
    }
    let path = root.join(".whosaidso/goals/g.json");
    let original = fs::read(&path).unwrap();
    for staged in [false, true] {
        write(&path, "{}");
        if staged {
            git(root, &["add", path.to_str().unwrap()]);
        }
        bad(root, &["gate"], "uncommitted");
        write(&path, &original);
        git(root, &["add", path.to_str().unwrap()]);
    }
    fs::remove_file(&path).unwrap();
    bad(root, &["gate"], "uncommitted");
    write(&path, original);
    cli(root, &["gate"]);
}

#[test]
fn cache_queries_leave_the_documented_landing_clean() {
    let f = Fixture::new();
    let root = &f.root;

    cli(root, &["install-hooks"]);
    commit(root);
    cli(root, &["start", "g"]);
    git(root, &["checkout", "-q", "-b", "worker"]);
    work(root, "work", "g");
    cli(root, &["find", "work"]);
    cli(root, &["grep", "work"]);
    git(root, &["checkout", "-q", "main"]);
    git(root, &["merge", "-q", "--no-ff", "worker", "-m", "land"]);
    assert_eq!(git(root, &["status", "--porcelain"]), "");
    cli(root, &["prune"]);
}
