mod common;
use common::process::*;
use common::*;

#[test]
fn landing_line_limit_requires_a_named_override() {
    let f = Fixture::new();
    f.remote();
    let lane = f.worktree("worker");
    work(&lane, "change", "g");
    write(
        &lane.join(".whosaidso/config.json"),
        r#"{"limits":{"land_lines":0}}"#,
    );
    commit(&lane);
    bad(
        &lane,
        &["land", "--reason", "behavior unchanged"],
        "limits.land_lines 0",
    );
    cli(
        &lane,
        &[
            "land",
            "--reason",
            "behavior unchanged",
            "--override",
            "reviewed change",
        ],
    );
    assert!(git(&lane, &["log", "-1", "--format=%B"]).contains("reviewed change"));
}

#[test]
fn a_local_bypass_is_caught_by_the_destination_ci_check() {
    let f = Fixture::new();
    common::records::check_command(&f.root, &["false"]);
    commit(&f.root);
    let base = head(&f.root);
    f.remote();
    let lane = f.worktree("worker");
    work(&lane, "feature", "g");
    common::records::proof(&lane, &head(&lane), 1, 0);
    commit(&lane);
    git(&lane, &["push", "--no-verify", "origin", "HEAD:main"]);
    bad(
        &lane,
        &["gate", "--ci", &format!("{base}..HEAD")],
        "queued check failed",
    );
}

#[test]
fn hook_directs_work_through_land_and_branch_mode_queues_the_receipt() {
    let f = Fixture::new();
    f.remote();
    let base = head(&f.root);
    let lane = f.worktree("worker");
    cli(&lane, &["install-hooks"]);
    commit(&lane);
    work(&lane, "feature", "g");
    failure(
        command(&lane, "git", &["push", "origin", "HEAD:main"])
            .output()
            .unwrap(),
        "whosaidso land",
    );
    write(&lane.join(".whosaidso/config.json"), r#"{"land":"branch"}"#);
    commit(&lane);
    cli(&lane, &["land", "--reason", "unchanged behavior"]);
    assert_eq!(
        git(&lane, &["ls-remote", "origin", "refs/heads/main"])
            .split_whitespace()
            .next(),
        Some(base.as_str())
    );
    assert_eq!(
        git(&lane, &["ls-remote", "origin", "refs/heads/land/g"])
            .split_whitespace()
            .next(),
        Some(head(&lane).as_str())
    );
    cli(&lane, &["gate", "--ci", &format!("{base}..HEAD")]);
}
