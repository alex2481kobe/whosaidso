mod common;
use common::process::*;
use common::{records as r, *};
use std::fs;

#[test]
fn state_queries_batch_git_reads_and_oversized_text_fetches_metadata_only() {
    let f = Fixture::new();
    let root = &f.root;
    let at = head(root);
    for n in 0..4 {
        r::proof(root, &at, n, 0);
        r::media(root, &png([n as u8]), "png", n);
    }
    write(&root.join("needle.txt"), "abcd exact\n");
    write(&root.join("large.dat"), "");
    fs::File::options()
        .write(true)
        .open(root.join("large.dat"))
        .unwrap()
        .set_len(2 * 1024 * 1024)
        .unwrap();
    commit(root);
    let large = git(root, &["rev-parse", ":large.dat"]);
    let real = success(
        command(root, "sh", &["-c", "command -v git"])
            .output()
            .unwrap(),
    );
    let bin = root.join(".git/bin");
    let trace = root.join(".git/trace");
    let requests = root.join(".git/requests");
    executable(
        &bin.join("git"),
        "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TRACE\"\nif test \"$1 $2\" = 'cat-file --batch-command' && test -n \"$REQUESTS\"; then tee \"$REQUESTS\" | \"$REAL_GIT\" \"$@\"; exit $?; fi\nexec \"$REAL_GIT\" \"$@\"\n",
    );
    let path = format!("{}:{}", bin.display(), std::env::var("PATH").unwrap());
    let shown = success(
        command(root, BIN, &["show", "g"])
            .env("PATH", &path)
            .env("REAL_GIT", real.trim())
            .env("TRACE", &trace)
            .output()
            .unwrap(),
    );
    assert!(shown.contains("state: done"));
    let calls = fs::read_to_string(&trace).unwrap();
    for call in ["rev-parse --git-common-dir", "cat-file --batch-command"] {
        assert_eq!(calls.lines().filter(|l| *l == call).count(), 1, "{calls}");
    }
    assert_eq!(
        calls.lines().filter(|l| l.starts_with("rev-list ")).count(),
        1
    );
    assert!(!calls.lines().any(|l| l.starts_with("show ")));
    let found = success(
        command(root, BIN, &["grep", "abcd"])
            .env("PATH", &path)
            .env("REAL_GIT", real.trim())
            .env("TRACE", &trace)
            .env("REQUESTS", &requests)
            .output()
            .unwrap(),
    );
    assert_eq!(found, "needle.txt:1:abcd exact\n");
    let requests = fs::read_to_string(requests).unwrap();
    assert!(requests.contains(&format!("info {}\n", large.trim())));
    assert!(!requests.contains(&format!("contents {}\n", large.trim())));
}

#[test]
fn help_and_default_branch_work_in_a_master_repository() {
    let f = Fixture::new();
    let root = &f.root;
    assert!(cli(root, &["help"]).contains("prove"));
    git(root, &["branch", "-m", "master"]);
    records::put(root, "", "config", &serde_json::json!({"main":"master"}));
    // Config belongs directly under .whosaidso, not a record collection.
    assert!(cli(root, &["goals"]).contains("open    g"));
    cli(root, &["install-hooks"]);
    commit(root);
    cli(root, &["start", "g"]);
    work(root, "work", "g");
    cli(root, &["gate"]);
    cli(root, &["prove", "g"]);
    commit(root);
    cli(root, &["prune"]);
    git(root, &["update-ref", "refs/remotes/origin/master", "HEAD"]);
    git(
        root,
        &[
            "symbolic-ref",
            "refs/remotes/origin/HEAD",
            "refs/remotes/origin/master",
        ],
    );
    write(
        &root.join(".whosaidso/config.json"),
        "{\"main\":\"missing\"}",
    );
    assert!(cli(root, &["goals"]).contains("done    g"));
}
