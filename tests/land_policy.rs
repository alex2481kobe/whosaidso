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
    for attached in [false, true] {
        let f = Fixture::new();
        common::records::check_command(&f.root, &["false"]);
        commit(&f.root);
        let base = head(&f.root);
        f.remote();
        let lane = f.worktree("worker");
        work(&lane, "feature", "g");
        if attached {
            attach(&lane, "report");
        } else {
            common::records::proof(&lane, &head(&lane), 1, 0);
        }
        commit(&lane);
        git(&lane, &["push", "--no-verify", "origin", "HEAD:main"]);
        bad(
            &lane,
            &["gate", "--ci", &format!("{base}..HEAD")],
            "queued check failed",
        );
    }
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

#[test]
fn old_archived_evidence_does_not_block_new_work_on_another_machine() {
    let f = Fixture::new();
    let blob = attach(&f.root, "old measurement");
    commit(&f.root);
    cli(&f.root, &["prune"]);
    commit(&f.root);
    std::fs::remove_file(blob).unwrap();
    f.remote();
    cli(&f.root, &["sync"]);
    let other = f.clone("other");
    common::records::goal(&other, "later");
    commit(&other);
    work(&other, "feature", "later");
    cli(&other, &["land", "--reason", "shared behavior check"]);
}

#[test]
fn reconciled_notes_still_qualify_after_the_landing_receipt_folds_them() {
    let f = Fixture::new();
    let note = "---\n{\"type\":\"idea\",\"status\":\"resolved\",\"title\":\"Choice\",\"goal\":\"g\"}\n---\nfirst\n\nlast\n";
    let file = ".whosaidso/notes/choice.md";
    write(&f.root.join(file), note);
    commit(&f.root);
    let base = head(&f.root);
    f.remote();
    let lane = f.worktree("worker");
    write(&lane.join(file), note.replace("first", "ours"));
    commit(&lane);
    work(&lane, "feature", "g");
    write(&f.root.join(file), note.replace("last", "theirs"));
    commit(&f.root);
    git(&f.root, &["push", "origin", "main"]);
    let theirs = hash(&std::fs::read(f.root.join(file)).unwrap());
    let ours = hash(&std::fs::read(lane.join(file)).unwrap());
    cli(&lane, &["note", "reconcile", "choice", &ours, &theirs]);
    commit(&lane);
    cli(&lane, &["land", "--reason", "shared check"]);
    assert!(!lane.join(file).exists());
    cli(&lane, &["gate", "--ci", &format!("{base}..HEAD")]);
}

#[test]
fn queued_work_stays_claimed_until_main_contains_the_receipt() {
    let f = Fixture::new();
    cli(&f.root, &["install-hooks"]);
    commit(&f.root);
    f.remote();
    let lane = f.worktree("worker");
    cli(&lane, &["start", "g"]);
    work(&lane, "feature", "g");
    write(&lane.join(".whosaidso/config.json"), r#"{"land":"branch"}"#);
    commit(&lane);
    let base = head(&f.root);
    cli(&lane, &["land", "--reason", "shared check"]);
    let other = f.clone("other");
    cli(&other, &["install-hooks"]);
    bad(&other, &["start", "g"], "claimed");
    let receipt = head(&lane);
    cli(&lane, &["gate", "--ci", &format!("{base}..{receipt}")]);
    git(&f.root, &["merge", "--ff-only", &receipt]);
    git(&f.root, &["push", "--no-verify", "origin", "main"]);
    cli(&lane, &["land", "--reason", "shared check"]);
    assert!(git(&lane, &["ls-remote", "origin", "refs/whosaidso/claims/*"]).is_empty());
    assert_eq!(head(&lane), receipt);
}
