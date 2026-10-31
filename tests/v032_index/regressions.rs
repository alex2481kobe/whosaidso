use super::*;

#[test]
fn branch_snapshot_survives_another_worktrees_head_query() {
    let fixture = Fixture::sources(&[("one.rs", "fn original() {}\n")]);
    let root = &fixture.root;
    let other = fixture.worktree("other");
    fs::write(other.join("one.rs"), "fn different() {}\n").unwrap();
    common::commit(&other);
    let first = cli(root, &["review", "--base", "HEAD"]);
    let before = first
        .lines()
        .next()
        .unwrap()
        .split_whitespace()
        .find_map(|s| s.strip_prefix("before="))
        .unwrap();
    cli(&other, &["review", "--base", "HEAD"]);
    let result = command(
        root,
        BIN,
        &["review", "--base", "HEAD", "--before-snapshot", before],
    )
    .output()
    .unwrap();
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
}

#[test]
fn oversized_reread_cannot_poison_a_smaller_files_facts() {
    let fixture = Fixture::sources(&[("tracked.rs", "fn tracked() {}\n")]);
    let root = &fixture.root;
    let small = "fn restored() {}\n";
    fs::write(root.join("growing.rs"), small).unwrap();
    fs::write(root.join(".git/grow"), "").unwrap();
    let bin = root.join(".git/bin");
    let real = std::env::split_paths(common::process::tool_path())
        .map(|p| p.join("git"))
        .find(|p| p.is_file())
        .unwrap();
    common::executable(
        &bin.join("git"),
        r##"#!/usr/bin/python3
import os, pathlib, sys
root = pathlib.Path.cwd()
if sys.argv[1:2] == ['cat-file'] and (root/'.git/grow').exists():
    with (root/'growing.rs').open('ab') as f: f.truncate(64*1024*1024+1)
    (root/'.git/grow').unlink()
os.execv(os.environ['REAL_GIT'], [os.environ['REAL_GIT'], *sys.argv[1:]])
"##,
    );
    let path = std::env::join_paths(
        std::iter::once(bin).chain(std::env::split_paths(common::process::tool_path())),
    )
    .unwrap();
    let out = command(root, BIN, &["code", "defs", "restored"])
        .env("PATH", path)
        .env("REAL_GIT", real)
        .output()
        .unwrap();
    common::process::success(out);
    assert!(!root.join(".git/grow").exists(), "growth race did not run");
    fs::write(root.join("growing.rs"), small).unwrap();
    let answer = cli(root, &["code", "defs", "restored"]);
    assert!(answer.contains("growing.rs:1"), "{answer}");
}

#[test]
fn changed_header_only_invalidates_that_documents_precise_rows() {
    let fixture = Fixture::sources(&[
        ("api.h", "inline void target() {}\n"),
        (
            "caller.cpp",
            "#include \"api.h\"\nvoid caller() { target(); }\n",
        ),
    ]);
    let root = &fixture.root;
    let target = "scip-test pkg project 1 target().";
    let caller = "scip-test pkg project 1 caller().";
    let mut document = field(1, b"caller.cpp");
    let mut info = field(1, caller.as_bytes());
    let mut relation = field(1, target.as_bytes());
    relation.extend([24, 1]);
    info.extend(field(4, &relation));
    document.extend(field(3, &info));
    common::scip::import(
        root,
        &[
            common::scip::doc("api.h", target, "target", &[(&[0, 12, 18], true)]),
            common::scip::doc("caller.cpp", caller, "caller", &[(&[1, 5, 11], true)]),
            common::scip::doc("caller.cpp", target, "target", &[(&[1, 16, 22], false)]),
            field(2, &document),
        ],
    );
    assert!(cli(root, &["code", "refs", "target"]).contains("[precise]"));
    assert!(
        cli(root, &["code", "callers", "target", "--implementations"])
            .contains("implementations: 1 recorded")
    );
    fs::write(root.join("api.h"), "inline void replacement() {}\n").unwrap();
    let answer = cli(root, &["code", "refs", "target"]);
    assert!(
        answer.contains("caller.cpp:2")
            && answer.contains("[precise]")
            && !answer.contains("[syntactic]"),
        "{answer}"
    );
    let coverage = cli(root, &["code", "callers", "caller", "--implementations"]);
    assert!(
        coverage.contains("precise=1") && coverage.contains("implementations: 1 recorded"),
        "{coverage}"
    );
}

#[test]
fn changed_build_environment_invalidates_the_precise_run() {
    let fixture = Fixture::sources(&[("one.rs", "fn target() {}\n")]);
    let root = &fixture.root;
    common::scip::import(
        root,
        &[common::scip::doc(
            "one.rs",
            "scip-test pkg project 1 target().",
            "target",
            &[(&[0, 3, 9], true)],
        )],
    );
    assert!(cli(root, &["code", "defs", "target"]).contains("[precise]"));
    let unchanged = common::process::success(
        command(root, BIN, &["code", "defs", "target"])
            .env("UNRELATED_AGENT_SESSION", "another-session")
            .env("PWD", "/different-shell")
            .env("GOFLAGS", "-tags=unrelated-language")
            .output()
            .unwrap(),
    );
    assert!(unchanged.contains("[precise]"), "{unchanged}");
    let answer = common::process::success(
        command(root, BIN, &["code", "defs", "target"])
            .env("RUSTFLAGS", "--cfg changed")
            .output()
            .unwrap(),
    );
    assert!(
        answer.contains("[syntactic]")
            && answer.contains("precise documents are stale")
            && !answer.contains("[precise]"),
        "{answer}"
    );
}

#[test]
fn unbound_old_artifact_cannot_hide_a_new_syntax_definition() {
    let fixture = Fixture::sources(&[("one.rs", "fn original() {}\n")]);
    let root = &fixture.root;
    let mut artifact = field(1, &[]);
    artifact.extend(common::scip::doc(
        "one.rs",
        "scip-test pkg project 1 original().",
        "original",
        &[(&[0, 3, 11], true)],
    ));
    fs::write(root.join(".git/old.scip"), artifact).unwrap();
    fs::write(root.join("one.rs"), "fn original() {}\nfn added() {}\n").unwrap();
    cli(root, &["code", "import-scip", ".git/old.scip"]);
    for (name, location) in [("added", "one.rs:2"), ("original", "one.rs:1")] {
        let answer = cli(root, &["code", "defs", name]);
        assert!(
            answer.contains(location)
                && answer.contains("[syntactic]")
                && answer.contains("unknown freshness")
                && !answer.contains("[precise]"),
            "{answer}"
        );
    }
}

#[test]
fn old_branch_snapshots_are_collected_and_their_tokens_expire() {
    let fixture = Fixture::sources(&[("one.rs", "fn original() {}\n")]);
    let root = &fixture.root;
    let commit = common::head(root);
    let first = cli(root, &["review"]);
    let token = first
        .lines()
        .next()
        .unwrap()
        .split_whitespace()
        .find_map(|s| s.strip_prefix("before="))
        .unwrap();
    for i in 0..10 {
        fs::write(root.join("one.rs"), format!("fn changed_{i}() {{}}\n")).unwrap();
        common::commit(root);
        cli(root, &["review"]);
    }
    assert!(
        scalar(
            &database(root),
            "SELECT count(*) FROM generations WHERE worktree IS NULL"
        ) <= 8,
        "committed snapshots grew beyond retention"
    );
    common::process::failure(
        command(
            root,
            BIN,
            &["review", "--base", &commit, "--before-snapshot", token],
        )
        .output()
        .unwrap(),
        "snapshot expired",
    );
    assert!(cli(root, &["code", "defs", "changed_9"]).contains("one.rs:1"));
}
