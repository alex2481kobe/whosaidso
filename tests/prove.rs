mod common;
use common::process::*;
use common::{records as r, *};
use std::fs;

#[test]
fn failure_output_and_exit_status_are_recorded_without_failing_the_command() {
    let f = Fixture::new();
    let root = &f.root;

    r::instrument(
        root,
        &["sh", "-c", "printf stderr >&2; printf stdout; exit 127"],
    );
    commit(root);
    let before = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_nanos();
    assert!(cli(root, &["prove", "g"]).contains(" fail "));
    let p = paths(&root.join(".whosaidso/proofs")).pop().unwrap();
    let v = value(&p);
    assert_eq!(v["exit_code"], 127);
    assert_eq!(v["output_tail"], "stderrstdout");
    assert_eq!(v["output_sha256"], hash(b"stderrstdout"));
    assert!(v["ran_at"].as_u64().unwrap() as u128 >= before);
    assert_eq!(
        p.file_stem().unwrap().to_str().unwrap(),
        hash(&fs::read(&p).unwrap())
    );
}

#[test]
fn direct_arguments_keep_shell_metacharacters_literal() {
    let f = Fixture::new();
    let root = &f.root;

    r::instrument(root, &["printf", "%s", "$HOME;\\q"]);
    commit(root);
    cli(root, &["prove", "g"]);
    let v = value(&paths(&root.join(".whosaidso/proofs"))[0]);
    assert_eq!(v["output_tail"], "$HOME;\\q");
}

#[test]
fn spawn_errors_publish_no_proof() {
    let f = Fixture::new();
    let root = &f.root;

    for (script, mode) in [
        ("#!/missing/interpreter\nexit 0\n", 0o755),
        ("#!/bin/sh\nexit 0\n", 0o644),
    ] {
        use std::os::unix::fs::PermissionsExt;
        write(&root.join("run"), script.replace("\n", "\n"));
        fs::set_permissions(root.join("run"), fs::Permissions::from_mode(mode)).unwrap();
        r::instrument(root, &["./run"]);
        commit(root);
        bad(root, &["prove", "g"], "start instrument");
        assert!(paths(&root.join(".whosaidso/proofs")).is_empty());
    }
    fs::remove_file(root.join("run")).unwrap();
    commit(root);
    bad(root, &["prove", "g"], "unresolved tool");
}

#[test]
fn dirty_or_staged_inputs_refuse_before_running() {
    let f = Fixture::new();
    let root = &f.root;

    for staged in [false, true] {
        write(&root.join("dirty"), "dirty");
        if staged {
            git(root, &["add", "dirty"]);
        }
        bad(root, &["prove", "g"], "commit or stash");
        assert!(paths(&root.join(".whosaidso/proofs")).is_empty());
    }
}

#[test]
fn disabled_or_missing_head_reflog_prevents_proof() {
    let f = Fixture::new();
    let root = &f.root;

    git(root, &["config", "core.logAllRefUpdates", "false"]);
    bad(root, &["prove", "g"], "core.logAllRefUpdates");
    git(root, &["config", "core.logAllRefUpdates", "true"]);
    fs::remove_file(root.join(".git/logs/HEAD")).unwrap();
    bad(root, &["prove", "g"], "core.logAllRefUpdates");
    assert!(paths(&root.join(".whosaidso/proofs")).is_empty());
}

#[test]
fn a_check_that_dirties_the_worktree_publishes_nothing() {
    let f = Fixture::new();
    let root = &f.root;

    r::instrument(root, &["sh", "-c", "printf dirty > dirty"]);
    commit(root);
    bad(
        root,
        &["prove", "g"],
        "working tree changed during instrument",
    );
    assert!(paths(&root.join(".whosaidso/proofs")).is_empty());
}

#[test]
fn head_round_trip_during_check_is_detected() {
    let f = Fixture::new();
    let root = &f.root;

    r::instrument(
        root,
        &[
            "sh",
            "-c",
            "git checkout -q --detach HEAD; git checkout -q main",
        ],
    );
    commit(root);
    bad(root, &["prove", "g"], "HEAD reflog changed");
    assert!(paths(&root.join(".whosaidso/proofs")).is_empty());
}

#[test]
fn head_movement_during_check_is_detected() {
    let f = Fixture::new();
    let root = &f.root;

    r::instrument(
        root,
        &[
            "git",
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.com",
            "commit",
            "-q",
            "--allow-empty",
            "-m",
            "moved",
        ],
    );
    commit(root);
    bad(root, &["prove", "g"], "HEAD changed during instrument");
    assert!(paths(&root.join(".whosaidso/proofs")).is_empty());
}

#[test]
fn repeated_publication_preserves_older_evidence() {
    let f = Fixture::new();
    let root = &f.root;
    cli(root, &["prove", "g"]);
    let before = snapshot(&root.join(".whosaidso/proofs"));
    cli(root, &["prove", "g"]);
    let after = snapshot(&root.join(".whosaidso/proofs"));
    assert_eq!(after.len(), 2);
    assert!(before.iter().all(|(p, b)| after.get(p) == Some(b)));
}

#[test]
fn killed_check_does_not_publish_a_partial_proof() {
    use std::os::unix::process::ExitStatusExt;
    let f = Fixture::new();
    let root = &f.root;
    r::instrument(root, &["sh", "-c", "kill -KILL $PPID"]);
    commit(root);
    let out = command(root, BIN, &["prove", "g"]).output().unwrap();
    assert_eq!(out.status.signal(), Some(9));
    assert!(paths(&root.join(".whosaidso/proofs")).is_empty());
}

#[test]
fn missing_or_non_blob_historical_instruments_cannot_replace_a_proof() {
    let f = Fixture::new();
    let root = &f.root;
    let at = head(root);
    r::proof(root, &at, 1, 0);
    commit(root);
    let path = root.join(".whosaidso/instruments/test.json");
    let bytes = fs::read(&path).unwrap();
    fs::remove_file(&path).unwrap();
    write(&path.join("wrong-kind"), "directory");
    commit(root);
    let wrong = head(root);
    fs::remove_dir_all(&path).unwrap();
    write(&path, bytes);
    commit(root);
    r::proof(root, &wrong, 2, 1);
    r::proof(root, &"0".repeat(40), 3, 1);
    assert!(cli(root, &["goals"]).contains("done    g"));
    assert!(cli(root, &["show", "g"]).contains(&at[..7]));
    bad(root, &["check"], "missing instrument at");
}
