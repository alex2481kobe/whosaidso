// Warm answers use cached bytes, including files that were skipped, and still see edits.
use std::{fs, path::Path, process::Command, time::{SystemTime, UNIX_EPOCH}};
fn ok(root: &Path, program: &str, args: &[&str]) -> String {
    let mut command = Command::new(program);
    command.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1");
    if program == "git" { command.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    let out = command.args(args).output().unwrap();
    assert!(out.status.success(), "{args:?}: {} {}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr));
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) -> String { ok(root, "git", args) }
fn cli(root: &Path, args: &[&str]) -> String { ok(root, env!("CARGO_BIN_EXE_whosaidso"), args) }
#[test]
fn warm_queries_reuse_bytes_and_notice_changes() {
    let root = std::env::temp_dir().join(format!("speed-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init"]);
    fs::write(root.join("main.rs"), "fn needle() {}\n").unwrap();
    fs::write(root.join("bad.rs"), [0xff, 0xfe]).unwrap();
    fs::write(root.join("guide.md"), "# Needle guide\n").unwrap();
    git(&root, &["add", "."]); git(&root, &["commit", "-m", "fixture"]);
    git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    assert!(cli(&root, &["code", "defs", "needle"]).contains("syntactic"));
    assert!(cli(&root, &["grep", "needle"]).contains("main.rs:1"));
    assert!(cli(&root, &["find", "needle", "guide"]).contains("guide.md"));
    // Removing only fixture blobs makes any repeated cat-file read fail, without using time.
    for path in ["main.rs", "bad.rs", "guide.md"] {
        let blob = git(&root, &["rev-parse", &format!(":{path}")]); let blob = blob.trim();
        fs::remove_file(root.join(".git/objects").join(&blob[..2]).join(&blob[2..])).unwrap();
    }
    assert!(cli(&root, &["code", "defs", "changed_needle"]).contains("syntactic"));
    assert!(cli(&root, &["grep", "needle"]).contains("main.rs:1"));
    assert!(cli(&root, &["find", "needle", "guide"]).contains("guide.md"));
    assert_eq!(cli(&root, &["code", "defs", "needle"]), "main.rs:1  fn needle  [syntactic]\n");
    assert!(cli(&root, &["grep", "needle"]).contains("main.rs:1: fn needle() {}"));
    assert!(cli(&root, &["find", "needle", "guide"]).contains("guide.md"));
    fs::write(root.join("main.rs"), "fn changed_needle() {}\n").unwrap();
    assert!(cli(&root, &["code", "defs", "changed_needle"]).contains("syntactic"));
    assert!(cli(&root, &["grep", "needle"]).contains("main.rs:1"));
    assert!(cli(&root, &["find", "needle", "guide"]).contains("guide.md"));
    assert_eq!(cli(&root, &["code", "defs", "changed_needle"]), "main.rs:1  fn changed_needle  [syntactic]\n");
    fs::remove_dir_all(root).unwrap();
}
