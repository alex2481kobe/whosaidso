// Tracked text search in a synthetic Git project.
use std::{fs, path::Path, process::{Command, Output}, time::{SystemTime, UNIX_EPOCH}};
fn run(root: &Path, program: &str, args: &[&str]) -> Output {
    let mut command = Command::new(program);
    command.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1");
    if program == "git" { command.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    command.args(args).output().unwrap()
}
fn ok(root: &Path, program: &str, args: &[&str]) -> String {
    let out = run(root, program, args);
    assert!(out.status.success(), "{args:?}: {} {}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr));
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) { ok(root, "git", args); }
fn cli(root: &Path, args: &[&str]) -> String { ok(root, env!("CARGO_BIN_EXE_whosaidso"), args) }
#[test]
fn tracked_text_is_incremental_and_searchable() {
    let root = std::env::temp_dir().join(format!("text-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(root.join("src")).unwrap();
    fs::create_dir_all(root.join("docs")).unwrap();
    git(&root, &["init"]);
    fs::write(root.join("src/wall.cpp"), "int studSpacing = 16;\n").unwrap();
    fs::write(root.join("docs/notes.md"), "Stud spacing is set per wall.\n").unwrap();
    fs::write(root.join("big.txt"), vec![b'x'; 2 * 1024 * 1024]).unwrap();
    fs::write(root.join("bin.dat"), b"binary\0data").unwrap();
    for i in 0..25 { fs::write(root.join(format!("repeat{i:02}.txt")), "repeatword\n").unwrap(); }
    fs::create_dir_all(root.join(".whosaidso")).unwrap(); fs::write(root.join(".whosaidso/.gitignore"), "cache/\n").unwrap();
    git(&root, &["add", "."]); git(&root, &["commit", "-m", "fixture"]);
    assert!(cli(&root, &["grep", "studspacing"]).contains("src/wall.cpp:1:"));
    assert!(cli(&root, &["grep", "studspacing"]).contains("src/wall.cpp:1:"));
    assert!(cli(&root, &["grep", "studspacing"]).contains("src/wall.cpp:1:"));
    assert!(cli(&root, &["grep", "Stud", "spacing"]).contains("docs/notes.md:1:"));
    let repeats = cli(&root, &["grep", "repeatword"]);
    assert_eq!(repeats.lines().count(), 22);
    assert!(repeats.contains("not searched: 2 files"));
    assert!(repeats.contains("shown 20 of 25 lines in 25 files"));
    let short = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["grep", "zz"]);
    assert_eq!(short.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&short.stdout).contains("needs at least 3 characters"));
    assert!(cli(&root, &["grep", "qqqzzzxx"]).contains("grep: no match"));
    fs::write(root.join("src/wall.cpp"), "int studSpacing = 24;\n").unwrap();
    git(&root, &["add", "src/wall.cpp"]); git(&root, &["commit", "-m", "change"]);
    assert!(cli(&root, &["grep", "studspacing"]).contains("studSpacing = 24"));
    fs::remove_dir_all(root).unwrap();
}
