mod common;
use common::process::*;
use common::{records as r, *};
use std::fs;

#[test]
fn killed_status_snapshot_recovers_its_own_index_lock() {
    let f = Fixture::new();
    let root = &f.root;

    use std::os::unix::process::ExitStatusExt;
    let real = success(
        command(root, "sh", &["-c", "command -v git"])
            .output()
            .unwrap(),
    );
    let bin = root.join(".git/bin");
    executable(
        &bin.join("git"),
        "#!/bin/sh\nif test \"$1\" = --no-optional-locks; then kill -KILL \"$PPID\"; kill -KILL \"$$\"; fi\nexec \"$REAL_GIT\" \"$@\"\n",
    );
    let out = command(root, BIN, &["prune"])
        .env(
            "PATH",
            format!("{}:{}", bin.display(), std::env::var("PATH").unwrap()),
        )
        .env("REAL_GIT", real.trim())
        .output()
        .unwrap();
    assert_eq!(out.status.signal(), Some(9));
    let journal = root.join(".git/whosaidso/prune.journal");
    let lock = root.join(".git/index.lock");
    assert!(journal.exists() && lock.exists());
    assert!(cli(root, &["check"]).contains("unfinished prune"));
    let saved = fs::read(&journal).unwrap();
    let mut v = value(&journal);
    v["pid"] = std::process::id().into();
    write(&journal, v.to_string());
    bad(root, &["prune"], "still running");
    assert!(lock.exists());
    write(&journal, saved);
    cli(root, &["prune"]);
    assert!(!lock.exists() && !journal.exists());
}

#[test]
fn recovery_preserves_a_foreign_live_git_lock() {
    let f = Fixture::new();
    let root = &f.root;

    use std::process::Stdio;
    r::gallery(root, 102, 40000);
    commit(root);
    let out = command(
        root,
        "sh",
        &[
            "-c",
            "ulimit -c 0; ulimit -f 64; exec \"$1\" prune",
            "sh",
            BIN,
        ],
    )
    .output()
    .unwrap();
    assert!(!out.status.success());
    let journal = root.join(".git/whosaidso/prune.journal");
    assert!(journal.exists());
    let mut foreign = command(root, "git", &["update-index", "--index-info"])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let lock = root.join(".git/index.lock");
    wait(&lock);
    bad(root, &["prune"], "another git process");
    assert!(lock.exists() && journal.exists());
    drop(foreign.stdin.take());
    success(foreign.wait_with_output().unwrap());
    cli(root, &["prune"]);
    assert!(!journal.exists());
}

#[test]
#[cfg(debug_assertions)]
fn failed_rollback_keeps_the_journal_until_recovery_succeeds() {
    let f = Fixture::new();
    let root = &f.root;

    let mut candidates = r::gallery(root, 101, 128);
    candidates.sort_by_key(|p| (value(p)["ran_at"].as_i64().unwrap(), p.clone()));
    commit(root);
    let before = snapshot(&root.join(".whosaidso/media"));
    let blocked = &candidates[0];
    let out = command(root, BIN, &["prune"])
        .env("BLOCKED", blocked)
        .env("WHOSAIDSO_PRUNE_HOOK", "mkdir \"$BLOCKED\"; false")
        .output()
        .unwrap();
    assert!(!out.status.success());
    let journal = root.join(".git/whosaidso/prune.journal");
    assert!(journal.exists());
    fs::remove_dir(blocked).unwrap();
    failure(
        command(root, BIN, &["prune"])
            .env("WHOSAIDSO_PRUNE_HOOK", "false")
            .output()
            .unwrap(),
        "nothing was removed",
    );
    assert!(!journal.exists());
    assert_eq!(snapshot(&root.join(".whosaidso/media")), before);
}

#[test]
fn prune_reports_the_failure_that_triggered_rollback() {
    let f = Fixture::new();
    failure(
        command(&f.root, BIN, &["prune"])
            .env("WHOSAIDSO_PRUNE_HOOK", "false")
            .output()
            .unwrap(),
        "prune hook failed",
    );
    write(&f.root.join(".whosaidso/goals/g.json"), "{");
    bad(&f.root, &["goals"], "g.json");
}
