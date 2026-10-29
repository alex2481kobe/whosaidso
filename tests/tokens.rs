// Concise search answers in one isolated Git project.
use std::{fs, path::Path, process::{Command, Output}, time::{SystemTime, UNIX_EPOCH}};
fn run(root: &Path, program: &str, args: &[&str]) -> Output {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args).output().unwrap()
}
fn ok(root: &Path, program: &str, args: &[&str]) -> String {
    let out = run(root, program, args);
    assert!(out.status.success(), "{args:?}: {} {}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr));
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) -> String { ok(root, "git", args) }
fn cli(root: &Path, args: &[&str]) -> String { ok(root, env!("CARGO_BIN_EXE_whosaidso"), args) }
fn write(root: &Path, path: &str, body: &str) {
    let path = root.join(path); fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, body).unwrap();
}
fn field(n: usize, data: &[u8]) -> Vec<u8> {
    fn varint(mut n: usize) -> Vec<u8> { let mut b = Vec::new(); while n >= 128 { b.push(n as u8 | 128); n >>= 7; } b.push(n as u8); b }
    let mut b = varint(n * 8 + 2); b.extend(varint(data.len())); b.extend(data); b
}
fn document(path: &str, symbol: &str) -> Vec<u8> {
    let mut d = field(1, path.as_bytes());
    for (line, role) in [(0, 1), (1, 0)] {
        let mut o = field(1, &[line, if role == 1 { 3 } else { 14 }, if role == 1 { 8 } else { 19 }]); o.extend(field(2, symbol.as_bytes())); o.extend([24, role]); d.extend(field(2, &o));
    }
    let mut info = field(1, symbol.as_bytes()); info.extend(field(6, b"misleading_display")); d.extend(field(3, &info));
    field(2, &d)
}
#[test]
fn search_answers_save_tokens_without_losing_scope() {
    let root = std::env::temp_dir().join(format!("tokens-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init"]);
    for i in 0..12 { write(&root, &format!("docs/quartz{i:02}.md"), "# Quartz\nscopeneedle\n"); }
    write(&root, ".whosaidso/evidence.txt", "scopeneedle\n");
    write(&root, "outside.txt", "scopeneedle\n");
    write(&root, "long.txt", &format!("cutneedle{}", "é".repeat(240)));
    let long_path = format!("{}.md", "longpath".repeat(28)); write(&root, &long_path, "# longpath\n");
    let long_name = format!("longfn{}", "x".repeat(220));
    write(&root, "long.rs", &format!("fn {long_name}() {{}}\n"));
    write(&root, "target.h", "#include <cmath>\n#include \"base.h\"\n");
    for i in 0..24 { write(&root, &format!("include{i:02}.cpp"), "#include \"target.h\"\n"); }
    write(&root, "scope.cpp", "namespace outer { namespace physics {\nint cross() { return 1; }\nint use() { return cross(); }\nstruct Type { int cross() { return 1; } };\n} namespace other {\nint cross() { return 2; }\nint use() { return cross(); }\n} }\nint direct() { return outer::physics::cross(); }\n");
    write(&root, "scope.rs", "mod outer { mod physics {\nfn cross() {}\nfn use_it() { cross(); }\nstruct Type;\nimpl Type { fn cross() {} fn use_it() { cross(); } }\ntrait Trait { fn cross() {} }\n} mod other {\nfn cross() {}\nfn use_it() { cross(); }\n} }\n");
    write(&root, "precise.rs", "fn cross() {}\nfn use_it() { cross(); }\n");
    write(&root, "other.rs", "fn cross() {}\nfn use_it() { cross(); }\n");
    let subject = format!("commitneedle {}", "é".repeat(100));
    git(&root, &["add", "."]); git(&root, &["commit", "-m", &subject]);
    git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    let found = cli(&root, &["find", "quartz", "kind:file"]);
    assert_eq!(found.lines().next(), Some("docs/quartz00.md"), "path once: {found}");
    assert_eq!(found.lines().count(), 9, "default 8: {found}");
    assert!(found.ends_with("shown 8 of 12\n"), "default 8: {found}");
    assert!(cli(&root, &["find", "quartz", "--limit", "11", "kind:file"]).ends_with("shown 11 of 12\n"));
    let commit = cli(&root, &["find", "commitneedle", "kind:commit"]);
    let sha = git(&root, &["rev-parse", "HEAD"]);
    assert_eq!(commit.lines().next(), Some(format!("{}  commitneedle {}...", &sha[..10], "é".repeat(56)).as_str()));
    let scoped = cli(&root, &["grep", "scopeneedle", "--in", "docs/quartz00.md", "--in", "outside"]);
    assert_eq!(scoped, "docs/quartz00.md:2: scopeneedle\noutside.txt:1: scopeneedle\n", "path scoping");
    assert_eq!(cli(&root, &["grep", "scopeneedle", "--in", "missing"]), "grep: no match\n");
    let ordered = cli(&root, &["grep", "scopeneedle"]);
    assert_eq!(ordered.lines().last(), Some(".whosaidso/evidence.txt:1: scopeneedle"), "ledger last: {ordered}");
    let cut = cli(&root, &["grep", "cutneedle"]);
    assert!(cut.trim_end().ends_with("...") && cut.trim_end().chars().count() == 160, "grep cutting: {cut}");
    let cut = cli(&root, &["find", "longpath", "kind:file"]);
    assert!(cut.lines().next().unwrap().ends_with("...") && cut.lines().next().unwrap().chars().count() == 200, "answer cutting: {cut}");
    // A pre-scope cache must be rebuilt without discarding the text index.
    let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    db.execute_batch("CREATE TABLE files(path TEXT PRIMARY KEY,blob TEXT); CREATE TABLE defs(path TEXT,name TEXT,kind TEXT,line INTEGER); CREATE TABLE calls(path TEXT,callee TEXT,line INTEGER,caller TEXT); CREATE TABLE includes(path TEXT,target TEXT,line INTEGER);").unwrap(); drop(db);
    let code_cut = cli(&root, &["code", "defs", &long_name]);
    assert!(code_cut.trim_end().ends_with("...") && code_cut.trim_end().chars().count() == 200, "code cutting: {code_cut}");
    let deps = cli(&root, &["code", "deps", "target.h"]);
    assert_eq!(deps.lines().count(), 2, "deps only: {deps}");
    assert!(deps.contains("includes cmath") && deps.contains("includes base.h") && !deps.contains("included by"), "deps only: {deps}");
    let includers = cli(&root, &["code", "includers", "target.h"]);
    assert_eq!(includers.lines().count(), 21, "includers cap: {includers}");
    assert!(includers.ends_with("shown 20 of 24\n") && includers.lines().take(20).all(|l| l.starts_with("included by")), "includers: {includers}");
    assert_eq!(cli(&root, &["code", "defs", "physics::cross"]), "scope.cpp:2  function cross  [syntactic]\nscope.rs:2  fn cross  [syntactic]\n", "qualified C++ and Rust defs");
    assert_eq!(cli(&root, &["code", "defs", "physics::Type::cross"]), "scope.cpp:4  method cross  [syntactic]\nscope.rs:5  fn cross  [syntactic]\n");
    assert_eq!(cli(&root, &["code", "defs", "Trait.cross"]), "scope.rs:6  fn cross  [syntactic]\n");
    assert_eq!(cli(&root, &["code", "refs", "physics::cross"]), "scope.cpp:3  in use  [syntactic]\nscope.cpp:9  in direct  [syntactic]\nscope.rs:3  in use_it  [syntactic]\n");
    let mut scip = field(1, &[]);
    scip.extend(document("precise.rs", "scip-test pkg project 1 outer/physics/cross()."));
    scip.extend(document("other.rs", "scip-test pkg project 1 outer/other/cross()."));
    fs::write(root.join("index.scip"), scip).unwrap();
    assert!(cli(&root, &["code", "import-scip", "index.scip"]).contains("0 stale documents"));
    let precise = cli(&root, &["code", "defs", "physics::cross"]);
    assert!(precise.contains("precise.rs:1  [precise]") && !precise.contains("other.rs"), "descriptor qualification: {precise}");
    let refs = cli(&root, &["code", "refs", "physics.cross"]);
    assert!(refs.contains("precise.rs:2  [precise]") && !refs.contains("other.rs"), "descriptor refs: {refs}");
    for i in 0..210 { write(&root, &format!("many/manyhit{i:03}.md"), "# manyhit\n"); }
    assert!(cli(&root, &["find", "manyhit", "kind:file", "--limit", "205"]).ends_with("shown 205 of 210\n"));
    for args in [&["find", "quartz", "--limit"][..], &["find", "quartz", "--limit", "0"], &["find", "quartz", "--limit", "bad"], &["grep", "needle", "--in"]] {
        assert!(!run(&root, env!("CARGO_BIN_EXE_whosaidso"), args).status.success(), "invalid arguments: {args:?}");
    }
    fs::remove_dir_all(root).unwrap();
}
