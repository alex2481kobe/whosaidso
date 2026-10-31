mod common;
use common::process::*;
use common::{records as r, *};
use std::fs;

#[test]
fn paired_runs_keep_results_and_restore_the_shared_scratch() {
    let f = Fixture::new();
    let root = &f.root;
    let counter = root.join(".git/count");
    let script = format!(
        "echo run >> '{}'; test ! -f dirt && test ! -f \"$WHOSAIDSO_OUTPUT/marker\" && touch \"$WHOSAIDSO_OUTPUT/marker\"",
        counter.display()
    );
    r::check_command(root, &["sh", "-c", &script]);
    commit(root);
    bad(root, &["prove", "g"], "--reason");
    let output = root.join(".git");
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
    assert!(cli(root, &["goals"]).contains("behavior unchanged"));
    assert!(cli(root, &["show", "g"]).contains("ran"));
    commit(root);
    let scratch = root.join(".git/whosaidso/prove/worktree");
    write(&scratch.join("dirt"), "left by an interrupted check");
    write(&scratch.join(".whosaidso/goals/g.json"), "corrupted");
    let keep = head(root);
    git(root, &["branch", "keep"]);
    git(&scratch, &["symbolic-ref", "HEAD", "refs/heads/keep"]);
    work(root, "new-input", "g");
    cli(root, &["prove", "g", "--reason", "behavior unchanged"]);
    assert_eq!(git(root, &["rev-parse", "keep"]).trim(), keep);
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
    work(root, "implemented", "g");
    git(root, &["checkout", "-q", "-b", "worker"]);
    executable(
        &root.join("run"),
        "#!/bin/sh\nprintf actual-output\nexit 7\n",
    );
    r::check_command(root, &["./run"]);
    commit(root);
    assert!(cli(root, &["prove", "g"]).contains("fail"));
    assert!(!cli(root, &["goals"]).contains("done    g"));
    executable(
        &root.join("run"),
        "#!/bin/sh\ntest -f implemented && printf passed\n",
    );
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

#[test]
fn paired_cargo_runs_reuse_builds_with_fresh_generated_output() {
    let f = Fixture::new();
    let root = &f.root;
    write(
        &root.join("Cargo.toml"),
        "[package]\nname = \"check-fixture\"\nversion = \"0.1.0\"\nedition = \"2024\"\n",
    );
    write(
        &root.join("src/main.rs"),
        r#"fn main() {
        let marker = std::path::PathBuf::from(std::env::var_os("WHOSAIDSO_OUTPUT").unwrap()).join("marker");
        assert!(!marker.exists());
        std::fs::write(marker, "fresh").unwrap();
        println!("fresh output");
    }"#,
    );
    write(
        &root.join("build.rs"),
        r#"fn main() {
        use std::io::Write;
        let mut count = std::fs::OpenOptions::new().create(true).append(true)
            .open(std::env::var_os("BUILD_COUNTER").unwrap()).unwrap();
        writeln!(count, "build").unwrap();
        println!("cargo:rerun-if-changed=build.rs");
    }"#,
    );
    r::check_command(root, &["cargo", "run", "--offline", "--quiet"]);
    commit(root);
    let count = root.join(".git/builds");
    success(
        command(root, BIN, &["prove", "g", "--reason", "unchanged behavior"])
            .env("BUILD_COUNTER", &count)
            .output()
            .unwrap(),
    );
    let proof = value(&paths(&root.join(".whosaidso/proofs"))[0]);
    assert_eq!(proof["exit_code"], 0);
    assert_eq!(proof["main"]["exit_code"], 0);
    assert_eq!(proof["output_tail"], "fresh output\n");
    assert_eq!(proof["main"]["output_tail"], "fresh output\n");
    assert_eq!(fs::read_to_string(count).unwrap().lines().count(), 1);

    git(root, &["checkout", "-q", "-b", "candidate"]);
    let source = root.join("src/main.rs");
    write(
        &source,
        fs::read_to_string(&source)
            .unwrap()
            .replace("fresh output", "candidate output"),
    );
    commit(root);
    success(
        command(root, BIN, &["prove", "g", "--reason", "unchanged behavior"])
            .env("BUILD_COUNTER", root.join(".git/builds"))
            .output()
            .unwrap(),
    );
    let proofs: Vec<_> = paths(&root.join(".whosaidso/proofs"))
        .iter()
        .map(|p| value(p))
        .collect();
    let proof = proofs
        .iter()
        .find(|p| p["output_tail"] == "candidate output\n")
        .expect("candidate rebuilt after its source changed");
    assert_eq!(proof["exit_code"], 0);
    assert_eq!(proof["main"]["exit_code"], 0);
    assert_eq!(proof["main"]["output_tail"], "fresh output\n");
}
