// Working-tree visibility and concurrent indexing in an isolated Git project.
use std::{fs, path::Path, process::{Command, Output, Stdio}, time::{Duration, SystemTime, UNIX_EPOCH}};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args); c
}
fn run(root: &Path, program: &str, args: &[&str]) -> Output { command(root, program, args).output().unwrap() }
fn ok(root: &Path, program: &str, args: &[&str]) -> String {
    let out = run(root, program, args);
    assert!(out.status.success(), "{args:?}: {} {}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr));
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) { ok(root, "git", args); }
fn cli(root: &Path, args: &[&str]) -> String { ok(root, env!("CARGO_BIN_EXE_whosaidso"), args) }
fn varint(mut n: usize) -> Vec<u8> { let mut out = Vec::new(); while n >= 128 { out.push(n as u8 | 128); n >>= 7; } out.push(n as u8); out }
fn field(n: usize, value: &[u8]) -> Vec<u8> { let mut out = varint(n * 8 + 2); out.extend(varint(value.len())); out.extend(value); out }
#[test]
fn working_tree_files_are_visible_and_writers_wait() {
    let root = std::env::temp_dir().join(format!("tree-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init"]);
    fs::write(root.join("main.rs"), "fn old_name() {}\n").unwrap();
    for i in 0..200 { fs::write(root.join(format!("file{i}.txt")), "before\n").unwrap(); }
    git(&root, &["add", "."]); git(&root, &["commit", "-m", "fixture"]);
    fs::write(root.join("main.rs"), "fn new_function() { /* editedneedle */ }\n").unwrap();
    assert!(cli(&root, &["grep", "editedneedle"]).contains("main.rs:1"));
    assert!(cli(&root, &["code", "defs", "new_function"]).contains("main.rs:1"));
    fs::write(root.join("new.txt"), "untrackedneedle\n").unwrap();
    assert!(cli(&root, &["grep", "untrackedneedle"]).contains("new.txt:1"));
    fs::write(root.join(".gitignore"), "new.txt\n").unwrap();
    assert!(!cli(&root, &["grep", "untrackedneedle"]).contains("new.txt:1"));
    fs::write(root.join("large.txt"), format!("largeuniqueword{}", "x".repeat(2 * 1024 * 1024))).unwrap();
    git(&root, &["add", "large.txt"]); git(&root, &["commit", "-m", "large"]);
    assert!(cli(&root, &["grep", "largeuniqueword"]).contains("not searched: 1 files"));
    fs::write(root.join("huge.rs"), "fn oversized_code() {}\n").unwrap();
    assert!(cli(&root, &["code", "defs", "oversized_code"]).contains("huge.rs:1"));
    let mut huge = vec![b' '; 64 * 1024 * 1024 + 1]; huge[..23].copy_from_slice(b"fn oversized_code() {}\n");
    fs::write(root.join("huge.rs"), huge).unwrap();
    assert!(cli(&root, &["code", "defs", "oversized_code"]).contains("none recorded"));
    git(&root, &["add", "huge.rs"]); git(&root, &["commit", "-m", "over code file limit"]);
    fs::remove_file(root.join("huge.rs")).unwrap();
    assert!(cli(&root, &["code", "defs", "oversized_code"]).contains("none recorded"));
    #[cfg(target_os = "linux")] { // macOS file systems refuse names that are not UTF-8.
        use std::{ffi::OsString, os::unix::ffi::OsStringExt};
        let name = OsString::from_vec(b"invalid-\xff.txt".to_vec());
        fs::write(root.join(&name), "invalid filename\n").unwrap();
        git(&root, &["add", "--all"]); git(&root, &["commit", "-m", "non utf8"]);
        assert!(cli(&root, &["grep", "editedneedle"]).contains("main.rs:1"));
        assert!(cli(&root, &["code", "defs", "new_function"]).contains("main.rs:1"));
    }
    let symbol = b"scip-test pkg project 1 new_function().";
    let mut occ = field(1, &[0, 3, 15]); occ.extend(field(2, symbol)); occ.extend([24, 1]);
    let mut doc = field(1, b"main.rs"); doc.extend(field(2, &occ));
    let mut scip = field(1, &[]); scip.extend(field(2, &doc));
    fs::write(root.join("index.scip"), scip).unwrap();
    fs::write(root.join("main.rs"), "/* imported edit */ fn new_function() {}\n").unwrap();
    cli(&root, &["code", "import-scip", "index.scip"]);
    assert!(cli(&root, &["code", "defs", "new_function"]).contains("[syntactic]"));
    fs::write(root.join("main.rs"), "fn new_function() { /* later edit */ }\n").unwrap();
    assert!(cli(&root, &["code", "defs", "new_function"]).contains("[syntactic]"));
    cli(&root, &["grep", "editedneedle"]);
    for i in 0..200 { fs::write(root.join(format!("file{i}.txt")), format!("changed {i}\n")).unwrap(); }
    let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    db.execute_batch("BEGIN IMMEDIATE").unwrap();
    let mut children: Vec<_> = (0..2).map(|_| command(&root, env!("CARGO_BIN_EXE_whosaidso"), &["grep", "changed", "--in", "file0.txt"])
        .stdout(Stdio::piped()).stderr(Stdio::piped()).spawn().unwrap()).collect();
    std::thread::sleep(Duration::from_millis(300));
    for child in &mut children { assert!(child.try_wait().unwrap().is_none(), "writer must wait for the held SQLite lock"); }
    db.execute_batch("COMMIT").unwrap(); drop(db);
    for child in children { let out = child.wait_with_output().unwrap(); assert!(out.status.success(), "{out:?}"); assert!(String::from_utf8_lossy(&out.stdout).contains("file0.txt:1: changed 0")); }
    fs::remove_dir_all(root).unwrap();
}
