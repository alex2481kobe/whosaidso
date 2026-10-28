// Final release regressions in one isolated Git project.
use std::{fs, path::Path, process::{Command, Output}, time::{SystemTime, UNIX_EPOCH}};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args); c
}
fn run(root: &Path, program: &str, args: &[&str]) -> Output { command(root, program, args).output().unwrap() }
fn success(out: Output) -> String {
    assert!(out.status.success(), "{} {}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr));
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) -> String { success(run(root, "git", args)) }
fn cli(root: &Path, args: &[&str]) -> String { success(run(root, env!("CARGO_BIN_EXE_whosaidso"), args)) }
fn write(root: &Path, path: &str, bytes: impl AsRef<[u8]>) {
    let path = root.join(path); fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap();
}
fn field(n: usize, bytes: &[u8]) -> Vec<u8> {
    fn varint(mut n: usize) -> Vec<u8> { let mut b = Vec::new(); while n >= 128 { b.push(n as u8 | 128); n >>= 7; } b.push(n as u8); b }
    let mut b = varint(n * 8 + 2); b.extend(varint(bytes.len())); b.extend(bytes); b
}
#[test]
fn proofs_precise_references_records_and_includers_are_honest() {
    let root = std::env::temp_dir().join(format!("fix4-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init"]);
    write(&root, ".whosaidso/phases/p.json", br#"{"title":"Phase","order":1}"#);
    let goal = br#"{"title":"validatorneedle","done_when":"done","check":"test","broader":"p"}"#;
    write(&root, ".whosaidso/goals/g.json", goal);
    write(&root, ".whosaidso/instruments/test.json", br#"{"run":["true"],"answers":"works"}"#);
    write(&root, "main.rs", "fn main() { old(); }\n");
    git(&root, &["add", "."]); git(&root, &["commit", "-m", "fixture"]);

    git(&root, &["config", "core.logAllRefUpdates", "false"]);
    fs::remove_file(root.join(".git/logs/HEAD")).unwrap();
    let out = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["prove", "g"]);
    assert_eq!(out.status.code(), Some(1), "1: no reflog: {out:?}");
    assert!(String::from_utf8_lossy(&out.stderr).contains("core.logAllRefUpdates"), "1: {out:?}");
    assert!(!root.join(".whosaidso/proofs").exists(), "1: no proof without movement evidence");
    git(&root, &["config", "core.logAllRefUpdates", "true"]);
    let out = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["prove", "g"]);
    assert_eq!(out.status.code(), Some(1), "1: enabled but absent reflog: {out:?}");
    assert!(!root.join(".whosaidso/proofs").exists());
    git(&root, &["checkout", "--detach", "HEAD"]); git(&root, &["checkout", "main"]);
    assert!(cli(&root, &["prove", "g"]).contains(" pass "), "1: readable reflog permits proofs");

    assert!(cli(&root, &["code", "defs", "main"]).contains("main.rs:1"));
    let mut doc = field(1, b"main.rs");
    for (range, symbol, role) in [([0, 3, 7], "scip-test pkg project 1 main().", 1), ([0, 12, 15], "scip-test pkg project 1 old().", 0)] {
        let mut o = field(1, &range); o.extend(field(2, symbol.as_bytes())); o.extend([24, role]); doc.extend(field(2, &o));
    }
    let mut index = field(1, &[]); index.extend(field(2, &doc));
    write(&root, ".whosaidso/cache/index.scip", index);
    write(&root, "main.rs", "fn main() { new(); }\n");
    assert!(cli(&root, &["code", "import-scip", ".whosaidso/cache/index.scip"]).contains("1 stale documents"), "2: reference mismatch expires document");
    assert!(!cli(&root, &["code", "refs", "old"]).contains("[precise]"), "2: stale reference cannot be precise");
    assert_eq!(cli(&root, &["code", "refs", "new"]), "main.rs:1  in main  [syntactic]\n");
    assert_eq!(cli(&root, &["code", "defs", "main"]), "main.rs:1  fn main  [syntactic]\n");

    write(&root, "implicit.cpp", "void anchor() {}\nmake(name);\nClock c;\ndecltype(c) d;\n__sync_add_and_fetch(p, 1);\n");
    for role in [0, 1] {
        let mut doc = field(1, b"implicit.cpp");
        for (range, symbol, role) in [
            ([0, 5, 11], "cxx . . $ anchor().", 1),
            ([1, 5, 9], "cxx . . $ ns/`Box`#`Box`(hash).", role),
            ([2, 0, 5], "cxx . . $ std/chrono/steady_clock#", role),
            ([3, 0, 8], "cxx . . $ ns/Type#", role),
            ([4, 0, 20], "cxx . . $ __sync_add_and_fetch_4().", role),
        ] {
            let mut o = field(1, &range); o.extend(field(2, symbol.as_bytes())); o.extend([24, role]); doc.extend(field(2, &o));
        }
        let mut index = field(1, &[]); index.extend(field(2, &doc));
        write(&root, ".whosaidso/cache/index.scip", index);
        let out = command(&root, env!("CARGO_BIN_EXE_whosaidso"), &["code", "import-scip", ".whosaidso/cache/index.scip"])
            .env("WHOSAIDSO_TIMING", "1").output().unwrap();
        let coverage = String::from_utf8(out.stderr.clone()).unwrap();
        let imported = success(out);
        assert!(imported.contains(&format!("{role} stale documents")), "reference exemptions: {imported}");
        assert!(coverage.contains(if role == 0 { "implicit.cpp: 1/5 occurrences, fresh" } else { "implicit.cpp: 5/5 occurrences, stale" }), "{coverage}");
        let answer = cli(&root, &["code", "defs", "anchor"]);
        assert_eq!(answer, if role == 0 { "implicit.cpp:1  [precise]\n" } else { "implicit.cpp:1  function anchor  [syntactic]\n" });
    }

    assert!(cli(&root, &["find", "kind:goal", "validatorneedle"]).contains("goal g"));
    write(&root, ".whosaidso/goals/g.json", String::from_utf8_lossy(goal).replace("\"check\":\"test\"", "\"check\":\"missing\""));
    let out = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]);
    assert_eq!(out.status.code(), Some(1), "3: {out:?}");
    assert!(String::from_utf8_lossy(&out.stdout).lines().any(|l| l == "goal g: unknown instrument missing"), "3: {out:?}");
    assert_eq!(cli(&root, &["find", "kind:goal", "validatorneedle"]), "find: no indexed match\n", "3: evict previously valid row");
    write(&root, ".whosaidso/goals/g.json", goal);
    assert!(cli(&root, &["check"]).contains("check: ok"));
    assert!(cli(&root, &["find", "kind:goal", "validatorneedle"]).contains("goal g"));

    write(&root, "include.cpp", "#include \"dir/target.h\"\n");
    assert_eq!(cli(&root, &["code", "includers", "target.h"]), "included by include.cpp:1  [syntactic, by name]\n");
    // Populate only this fixture's disposable cache; a warm query must bound its answer storage.
    let db = rusqlite::Connection::open(root.join(".whosaidso/cache/index.db")).unwrap();
    db.execute_batch("CREATE INDEX fix4_includes_order ON includes(path,line);
        WITH RECURSIVE n(i) AS (VALUES(2) UNION ALL SELECT i+1 FROM n WHERE i<400000)
        INSERT INTO includes SELECT 'include.cpp','dir/target.h',i FROM n;").unwrap();
    drop(db);
    let exe = env!("CARGO_BIN_EXE_whosaidso");
    let out = run(&root, "sh", &["-c", "ulimit -v 45000; exec \"$1\" code includers target.h", "sh", exe]);
    assert!(out.status.success(), "4: includers must fit bounded memory: {out:?}");
    let answer = String::from_utf8(out.stdout).unwrap();
    let expected: String = (1..=20).map(|i| format!("included by include.cpp:{i}  [syntactic, by name]\n")).collect();
    assert_eq!(answer, format!("{expected}shown 20 of 400000\n"), "4: retain order, cap and exact count");
    fs::remove_dir_all(root).unwrap();
}
