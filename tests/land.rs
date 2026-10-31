mod common;
use common::process::*;
use common::{records as r, *};
use std::fs;

fn ready() -> (Fixture, std::path::PathBuf) {
    let f = Fixture::new();
    r::check_command(&f.root, &["sh", "-c", "test -f feature"]);
    commit(&f.root);
    f.remote();
    let lane = f.worktree("worker");
    cli(&lane, &["install-hooks"]);
    commit(&lane);
    cli(&lane, &["start", "g"]);
    work(&lane, "feature", "g");
    (f, lane)
}

#[test]
fn land_tests_candidate_folds_and_releases_its_claim() {
    let (f, lane) = ready();
    let text = cli(&lane, &["land"]);
    assert!(text.contains("g check changed"));
    let tip = git(&f.root, &["rev-parse", "origin/main"]);
    assert_eq!(tip.trim(), head(&lane));
    assert!(lane.join(".whosaidso/done/g.jsonl").exists());
    assert!(git(&lane, &["for-each-ref", "refs/whosaidso/claims"]).is_empty());
    cli(&lane, &["gate", "main"]);
    assert!(git(&lane, &["status", "--porcelain"]).is_empty());
}

#[test]
fn land_refuses_a_flow_that_drifted_from_its_code() {
    let (_f, lane) = ready();
    write(&lane.join("app.rs"), "fn screen() {}\nfn action() {}\n");
    let plan = serde_json::json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"screen","type":"screen","symbols":["screen"]},
        {"id":"action","type":"action","symbols":["action"]}
    ],"steps":[{"id":"tap","from":"screen","to":"action","when":"tap"}]});
    cli(&lane, &["flow", "put", "app", &plan.to_string()]);
    commit(&lane);
    bad(&lane, &["land"], "drifted");
}

#[test]
fn broken_main_does_not_block_the_fix() {
    for path in [".whosaidso/flows/bad.json", ".whosaidso/done/g.jsonl"] {
        let (f, lane) = ready();
        write(&f.root.join(path), "{}");
        commit(&f.root);
        git(&f.root, &["push", "origin", "main"]);
        git(&lane, &["fetch", "origin"]);
        git(&lane, &["merge", "-q", "origin/main"]);
        fs::remove_file(lane.join(path)).unwrap();
        commit(&lane);
        cli(&lane, &["land"]);
    }
}
#[test]
fn receipt_cannot_change_code_even_with_a_valid_trailer() {
    let (_f, lane) = ready();
    cli(&lane, &["land"]);
    let receipt = head(&lane);
    write(&lane.join("feature"), "untested");
    git(&lane, &["add", "feature"]);
    git(&lane, &["commit", "--amend", "--no-edit", "-q"]);
    assert_ne!(receipt, head(&lane));
    bad(&lane, &["gate", "main"], "receipt");
}

#[test]
fn a_rejected_push_retests_the_new_combination() {
    let (f, lane) = ready();
    work(&f.root, "other", "none");
    let script = format!(
        "if [ \"$WHOSAIDSO_LAND_STEP\" = before-push ] && [ ! -f '{}'/raced ]; then touch '{}'/raced; git -C '{}' push -q origin main; fi",
        f.root.parent().unwrap().display(),
        f.root.parent().unwrap().display(),
        f.root.display()
    );
    let out = success(
        command(&lane, BIN, &["land"])
            .env("WHOSAIDSO_LAND_HOOK", script)
            .output()
            .unwrap(),
    );
    assert!(out.contains("testing again"));
    assert_eq!(out.matches("ran: g pass").count(), 2);
    assert!(lane.join("other").is_file());
    assert_eq!(
        git(&lane, &["rev-parse", "origin/main"]).trim(),
        head(&lane)
    );
}

fn crash_at(steps: &[&str]) {
    for step in steps {
        let (_f, lane) = ready();
        let script = format!("[ \"$WHOSAIDSO_LAND_STEP\" != {step} ] || kill -KILL $PPID");
        assert!(
            !command(&lane, BIN, &["land"])
                .env("WHOSAIDSO_LAND_HOOK", script)
                .output()
                .unwrap()
                .status
                .success()
        );
        cli(&lane, &["land"]);
        assert!(git(&lane, &["status", "--porcelain"]).is_empty());
    }
}

#[test]
fn crashes_while_merging_resume() {
    crash_at(&["lock", "fetch", "merge"]);
}
#[test]
fn crashes_while_checking_resume() {
    crash_at(&["gate", "check", "media"]);
}
#[test]
fn crashes_around_the_receipt_resume() {
    crash_at(&["receipt", "before-push"]);
}

#[test]
fn published_receipt_releases_claim_even_after_a_later_edit() {
    for step in ["published", "released"] {
        let (_f, lane) = ready();
        let script = format!("[ \"$WHOSAIDSO_LAND_STEP\" != {step} ] || kill -KILL $PPID");
        assert!(
            !command(&lane, BIN, &["land"])
                .env("WHOSAIDSO_LAND_HOOK", script)
                .output()
                .unwrap()
                .status
                .success()
        );
        work(&lane, "later", "none");
        cli(&lane, &["land"]);
        assert!(git(&lane, &["ls-remote", "origin", "refs/whosaidso/claims/*"]).is_empty());
    }
}

#[test]
fn unpushed_receipt_fork_with_later_work_is_tested_again() {
    let (_f, lane) = ready();
    assert!(
        !command(&lane, BIN, &["land"])
            .env(
                "WHOSAIDSO_LAND_HOOK",
                "[ \"$WHOSAIDSO_LAND_STEP\" != before-push ] || kill -KILL $PPID"
            )
            .output()
            .unwrap()
            .status
            .success()
    );
    git(&lane, &["checkout", "-q", "-b", "fork"]);
    work(&lane, "later", "g");
    let text = cli(&lane, &["land"]);
    assert!(text.contains("ran: g pass"));
}

#[test]
fn stale_folded_goal_edit_names_reopen() {
    let (f, lane) = ready();
    let stale = f.worktree("stale");
    r::check_command(&stale, &["false"]);
    commit(&stale);
    cli(&lane, &["land"]);
    bad(&stale, &["land"], "reopen g");
}

#[test]
fn clean_note_merge_still_requires_reconciliation_at_landing() {
    let (f, lane) = ready();
    let note = "---\n{\"type\":\"decision\",\"status\":\"open\",\"title\":\"Choice\",\"goal\":\"g\"}\n---\nfirst\n\nlast\n";
    write(&f.root.join(".whosaidso/notes/choice.md"), note);
    commit(&f.root);
    git(&f.root, &["push", "-q", "origin", "main"]);
    git(&lane, &["fetch", "origin"]);
    git(&lane, &["merge", "-q", "origin/main"]);
    write(
        &lane.join(".whosaidso/notes/choice.md"),
        note.replace("first", "ours"),
    );
    commit(&lane);
    write(
        &f.root.join(".whosaidso/notes/choice.md"),
        note.replace("last", "theirs"),
    );
    commit(&f.root);
    git(&f.root, &["push", "-q", "origin", "main"]);
    bad(&lane, &["land"], "note reconcile choice");
}

#[test]
fn published_receipts_do_not_hide_uncommitted_edits() {
    let (_f, lane) = ready();
    cli(&lane, &["land"]);
    write(&lane.join("later"), "new work");
    bad(&lane, &["land"], "commit your changes");
    assert_eq!(fs::read_to_string(lane.join("later")).unwrap(), "new work");
}
