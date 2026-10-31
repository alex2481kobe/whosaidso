mod common;
use common::process::*;
use common::{records as r, *};

#[test]
fn a_fresh_candidate_failure_blocks_a_future_dated_pass() {
    let f = Fixture::new();
    r::check_command(&f.root, &["sh", "-c", "test ! -f broken"]);
    commit(&f.root);
    work(&f.root, "feature", "g");
    f.remote();
    let lane = f.worktree("worker");
    cli(&lane, &["install-hooks"]);
    commit(&lane);
    cli(&lane, &["start", "g"]);
    r::proof(&f.root, &head(&f.root), i64::MAX - 1, 0);
    commit(&f.root);
    git(&f.root, &["push", "origin", "main"]);
    work(&lane, "broken", "none");
    let before = git(&f.root, &["ls-remote", "origin", "refs/heads/main"]);
    bad(
        &lane,
        &["land", "--reason", "existing behavior"],
        "candidate check failed",
    );
    assert_eq!(
        before,
        git(&f.root, &["ls-remote", "origin", "refs/heads/main"])
    );
}

#[test]
fn absolute_check_arguments_use_each_restored_revision() {
    let f = Fixture::new();
    let lane = f.worktree("worker");
    let check = lane.join("check.sh");
    write(
        &check,
        "test -f \"$1\"\nprintf '%s\\n' \"$1\" \"$2\" \"$3\"\ntest -f \"$1\"\n",
    );
    r::check_command(
        &lane,
        &[
            "sh",
            check.to_str().unwrap(),
            lane.join("feature").to_str().unwrap(),
            &format!("--input={}/feature", lane.display()),
            &format!("--linked={}/alias", lane.display()),
        ],
    );
    std::os::unix::fs::symlink("feature", lane.join("alias")).unwrap();
    work(&lane, "feature", "g");
    commit(&lane);
    cli(&lane, &["prove", "g", "--reason", "existing behavior"]);
    let proof = value(&paths(&lane.join(".whosaidso/proofs"))[0]);
    assert_ne!(proof["main"]["exit_code"], 0);
    assert_eq!(proof["exit_code"], 0);
    let output = proof["output_tail"].as_str().unwrap();
    assert!(!output.contains(lane.to_str().unwrap()), "{output}");
    assert!(output.contains("/prove/worktree/alias"), "{output}");
    assert!(output.contains("/prove/worktree/feature"), "{output}");
    assert!(output.contains("--input="), "{output}");
}
