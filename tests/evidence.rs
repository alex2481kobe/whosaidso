mod common;
use common::process::*;
use common::{records as r, *};
use std::fs;

#[test]
fn paired_runs_keep_results_and_restore_the_shared_scratch() {
    let f = Fixture::new();
    let root = &f.root;
    r::check_command(
        root,
        &[
            "sh",
            "-c",
            "echo run >> \"$WHOSAIDSO_OUTPUT/count\"; test ! -f dirt",
        ],
    );
    commit(root);
    bad(root, &["prove", "g"], "--reason");
    let output = root.join(".git/whosaidso/prove/generated");
    assert_eq!(
        fs::read_to_string(output.join("count"))
            .unwrap()
            .lines()
            .count(),
        2
    );
    cli(root, &["prove", "g", "--reason", "behavior unchanged"]);
    assert_eq!(
        fs::read_to_string(output.join("count"))
            .unwrap()
            .lines()
            .count(),
        2
    );
    let p = value(&paths(&root.join(".whosaidso/proofs"))[0]);
    assert_eq!(p["main"]["exit_code"], 0);
    assert_eq!(p["reason"], "behavior unchanged");
    assert!(cli(root, &["show", "g"]).contains("ran"));
    commit(root);
    let scratch = root.join(".git/whosaidso/prove/worktree");
    write(&scratch.join("dirt"), "left by an interrupted check");
    write(&scratch.join(".whosaidso/goals/g.json"), "corrupted");
    work(root, "new-input", "g");
    cli(root, &["prove", "g", "--reason", "behavior unchanged"]);
    assert_eq!(
        fs::read_to_string(output.join("count"))
            .unwrap()
            .lines()
            .count(),
        4
    );
}

#[test]
fn a_new_check_can_fail_on_main_and_still_prove_the_candidate() {
    let f = Fixture::new();
    let root = &f.root;
    assert!(cli(root, &["goals"]).contains("open    g"));
    git(root, &["checkout", "-q", "-b", "worker"]);
    executable(
        &root.join("run"),
        "#!/bin/sh\nprintf actual-output\nexit 7\n",
    );
    r::check_command(root, &["./run"]);
    commit(root);
    assert!(cli(root, &["prove", "g"]).contains("fail"));
    assert!(!cli(root, &["goals"]).contains("done    g"));
    executable(&root.join("run"), "#!/bin/sh\nprintf passed\n");
    commit(root);
    assert!(cli(root, &["prove", "g"]).contains("failed on main"));
    let proofs = paths(&root.join(".whosaidso/proofs"));
    let p = proofs
        .iter()
        .map(|p| value(p))
        .find(|p| p["exit_code"] == 0)
        .unwrap();
    assert_ne!(p["main"]["exit_code"], 0);
    assert!(
        p["main"]["output_tail"]
            .as_str()
            .unwrap()
            .contains("start check")
    );
    commit(root);
    git(root, &["checkout", "-q", "main"]);
    git(root, &["merge", "-q", "worker"]);
    assert!(cli(root, &["goals"]).contains("done    g"));
}

#[test]
#[ignore = "pending any-file media"]
fn attached_text_report_is_gallery_evidence_by_hash() {
    use common::viewer::Viewer;
    let f = Fixture::new();
    let root = &f.root;
    let report = root.parent().unwrap().join("report.txt");
    write(&report, "measured report\n");
    cli(
        root,
        &[
            "evidence",
            "add",
            "g",
            report.to_str().unwrap(),
            "Measured output",
        ],
    );
    let sha = hash(b"measured report\n");
    let viewer = Viewer::start(root);
    let gallery = viewer.json("/api/gallery", &viewer.cookie());
    assert_eq!(gallery["entries"][0]["media_sha256"], sha);
    assert!(cli(root, &["show", "g"]).contains("attached"));
    assert!(cli(root, &["goals"]).contains("done    g"));
}
